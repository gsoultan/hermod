// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"context"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/testutil"
)

type countingConnectorStore struct {
	testutil.BaseMockStorage
	sources     map[string]storage.Source
	sinks       map[string]storage.Sink
	sourceReads int
	sinkReads   int
}

func (s *countingConnectorStore) GetSource(_ context.Context, id string) (storage.Source, error) {
	s.sourceReads++
	return s.sources[id], nil
}

func (s *countingConnectorStore) GetSink(_ context.Context, id string) (storage.Sink, error) {
	s.sinkReads++
	return s.sinks[id], nil
}

// A cached connector whose stored vhost, type or config moved is read again;
// one that only ran (its state changed) or that the lists did not include
// stays cached.
func TestForgetChangedResourcesDropsOnlyWhatChanged(t *testing.T) {
	store := &countingConnectorStore{
		sources: map[string]storage.Source{
			"renamed":  {ID: "renamed", Type: "postgres", VHost: "sales"},
			"ran":      {ID: "ran", Type: "postgres", VHost: "ops", State: map[string]string{"lsn": "1"}},
			"unlisted": {ID: "unlisted", Type: "postgres", VHost: "sales"},
		},
		sinks: map[string]storage.Sink{
			"edited": {ID: "edited", Type: "http", VHost: "ops", Config: map[string]string{"url": "a"}},
		},
	}
	reg := NewRegistry(store)
	ctx := t.Context()
	for _, id := range []string{"renamed", "ran", "unlisted"} {
		if _, err := reg.GetSourceConfig(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := reg.GetSinkConfig(ctx, "edited"); err != nil {
		t.Fatal(err)
	}

	store.sources["renamed"] = storage.Source{ID: "renamed", Type: "postgres", VHost: "revenue"}
	store.sources["ran"] = storage.Source{ID: "ran", Type: "postgres", VHost: "ops", State: map[string]string{"lsn": "2"}}
	store.sinks["edited"] = storage.Sink{ID: "edited", Type: "http", VHost: "ops", Config: map[string]string{"url": "b"}}
	reg.ForgetChangedResources(
		map[string]storage.Source{"renamed": store.sources["renamed"], "ran": store.sources["ran"]},
		map[string]storage.Sink{"edited": store.sinks["edited"]},
	)
	store.sourceReads, store.sinkReads = 0, 0

	if src, _ := reg.GetSourceConfig(ctx, "renamed"); src.VHost != "revenue" {
		t.Errorf("renamed source vhost = %q, want revenue", src.VHost)
	}
	if snk, _ := reg.GetSinkConfig(ctx, "edited"); snk.Config["url"] != "b" {
		t.Errorf("edited sink url = %q, want b", snk.Config["url"])
	}
	_, _ = reg.GetSourceConfig(ctx, "ran")
	_, _ = reg.GetSourceConfig(ctx, "unlisted")
	if store.sourceReads != 1 || store.sinkReads != 1 {
		t.Errorf("store reads after forgetting: sources %d, sinks %d; want 1 and 1 (only the changed ones)", store.sourceReads, store.sinkReads)
	}
}
