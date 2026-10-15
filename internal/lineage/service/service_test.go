// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package service

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/lineage"
	"github.com/gsoultan/hermod/internal/storage"
)

// fakeLoader returns fixed inputs and records which vhost it was asked for.
type fakeLoader struct {
	in    lineage.Inputs
	asked []string
	err   error
}

func (f *fakeLoader) Load(_ context.Context, vhost string) (lineage.Inputs, error) {
	f.asked = append(f.asked, vhost)
	return f.in, f.err
}

func twoTenants() lineage.Inputs {
	return lineage.Inputs{
		Sources: []storage.Source{
			{ID: "sa", VHost: "tenant-a", Type: "kafka", Config: hermod.StringMap{"brokers": "b", "topic": "a"}},
			{ID: "sb", VHost: "tenant-b", Type: "kafka", Config: hermod.StringMap{"brokers": "b", "topic": "b"}},
		},
		Sinks: []storage.Sink{
			{ID: "kb", VHost: "tenant-b", Type: "kafka", Config: hermod.StringMap{"brokers": "b", "topic": "a"}},
		},
		Workflows: []storage.Workflow{
			{ID: "wa", VHost: "tenant-a", Nodes: []storage.WorkflowNode{{ID: "n", Type: "source", RefID: "sa"}}},
			// tenant-b's workflow writes the topic tenant-a reads, and also
			// names tenant-a's source: neither may surface for tenant-a's
			// callers, nor may tenant-a's records surface for tenant-b's.
			{ID: "wb", VHost: "tenant-b", Nodes: []storage.WorkflowNode{{ID: "n", Type: "source", RefID: "sb"}, {ID: "m", Type: "sink", RefID: "kb"}, {ID: "o", Type: "source", RefID: "sa"}}},
		},
		Models: []storage.MLModel{{VHost: "tenant-a", Name: "ma"}, {VHost: "tenant-b", Name: "mb"}},
	}
}

func onlyVHosts(vs ...string) func(string) bool {
	return func(v string) bool { return slices.Contains(vs, v) }
}

func ids(g lineage.Graph) []string {
	var out []string
	for _, n := range g.Nodes {
		out = append(out, n.ID)
	}
	return out
}

func TestGraphOfAllVHostsKeepsOnlyWhatTheCallerMaySee(t *testing.T) {
	f := &fakeLoader{in: twoTenants()}
	g, err := New(f).Graph(t.Context(), Scope{VHost: "all", Allows: onlyVHosts("tenant-a")})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"model:ml://tenant-a/ma", "topic:kafka://b/a", "workflow:wa"}
	if got := ids(g); !slices.Equal(got, want) {
		t.Fatalf("nodes = %v, want %v", got, want)
	}
	for _, n := range g.Nodes {
		for _, r := range n.Refs {
			if r.ID == "kb" || r.ID == "sb" {
				t.Errorf("tenant-b record %s leaked into %s", r.ID, n.ID)
			}
		}
	}
	if !slices.Equal(f.asked, []string{"all"}) {
		t.Errorf("loaded %v", f.asked)
	}
}

func TestGraphOfOneVHostRefusesACallerWithoutIt(t *testing.T) {
	f := &fakeLoader{in: twoTenants()}
	_, err := New(f).Graph(t.Context(), Scope{VHost: "tenant-b", Allows: onlyVHosts("tenant-a")})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if len(f.asked) != 0 {
		t.Errorf("storage was read for a refused caller: %v", f.asked)
	}
}

func TestGraphOfOneVHostLoadsThatVHost(t *testing.T) {
	f := &fakeLoader{in: twoTenants()}
	g, err := New(f).Graph(t.Context(), Scope{VHost: "tenant-b", Allows: onlyVHosts("tenant-b")})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.asked, []string{"tenant-b"}) {
		t.Errorf("loaded %v", f.asked)
	}
	// The loader may return more than asked (a backend that ignores the
	// filter); the service still keeps the one vhost.
	want := []string{"model:ml://tenant-b/mb", "topic:kafka://b/a", "topic:kafka://b/b", "workflow:wb"}
	if got := ids(g); !slices.Equal(got, want) {
		t.Fatalf("nodes = %v, want %v", got, want)
	}
}

func TestAScopeWithoutAnAccessRuleIsRefused(t *testing.T) {
	if _, err := New(&fakeLoader{}).Graph(t.Context(), Scope{VHost: "all"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestImpactWithinTheScope(t *testing.T) {
	svc := New(&fakeLoader{in: twoTenants()})
	imp, err := svc.Impact(t.Context(), Scope{VHost: "all", Allows: onlyVHosts("tenant-b")}, "topic:kafka://b/b")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range imp.Impacted {
		got = append(got, it.Node.ID)
	}
	if want := []string{"workflow:wb", "topic:kafka://b/a"}; !slices.Equal(got, want) {
		t.Fatalf("impacted = %v, want %v", got, want)
	}
	// Downstream of tenant-b's topic is tenant-a's workflow; it is not
	// tenant-b's caller's to see.
	if _, err := svc.Impact(t.Context(), Scope{VHost: "all", Allows: onlyVHosts("tenant-b")}, "workflow:wa"); !errors.Is(err, lineage.ErrUnknownNode) {
		t.Fatalf("another vhost's workflow: err = %v, want ErrUnknownNode", err)
	}
}

func TestImpactOfWorkflowIsTheDownstreamOfThatWorkflowInItsVHost(t *testing.T) {
	svc := New(&fakeLoader{in: twoTenants()})
	imp, err := svc.WorkflowImpact(t.Context(), Scope{VHost: "tenant-b", Allows: onlyVHosts("tenant-b")}, "wb")
	if err != nil {
		t.Fatal(err)
	}
	if len(imp.Impacted) != 1 || imp.Impacted[0].Node.ID != "topic:kafka://b/a" {
		t.Fatalf("impacted = %+v", imp.Impacted)
	}
}

func TestLoadErrorsAreReturned(t *testing.T) {
	boom := errors.New("db down")
	if _, err := New(&fakeLoader{err: boom}).Graph(t.Context(), Scope{VHost: "all", Allows: onlyVHosts()}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}
