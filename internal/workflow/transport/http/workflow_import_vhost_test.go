// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// A workflow exported from one vhost and imported into another on the same
// instance carries the ids of the original's source and sink. Upserting those
// ids made the import share the original's connections across vhosts — so
// deleting the copy's sink was refused as "used by workflow <original>" — or,
// with the resource's vhost changed, moved the connection out from under the
// original workflow. Either way the import writes nothing and says to import
// the connection as a separate copy.
func TestImportIntoAnotherVHostDoesNotReuseTheOriginalsConnections(t *testing.T) {
	for _, tc := range []struct {
		name string
		// edit turns the exported bundle into what was sent.
		edit func(b *storage.WorkflowExportBundle)
		want string
	}{
		{
			name: "shared: the workflow goes to the other vhost, its connections keep their ids",
			edit: func(b *storage.WorkflowExportBundle) {
				b.Workflow.ID, b.Workflow.VHost = "wf-2", "team-b"
			},
			want: "source src-1",
		},
		{
			name: "moved: the connections are sent to the other vhost under their old ids",
			edit: func(b *storage.WorkflowExportBundle) {
				b.Workflow.ID, b.Workflow.VHost = "wf-2", "team-b"
				for i := range b.Sources {
					b.Sources[i].VHost = "team-b"
				}
				for i := range b.Sinks {
					b.Sinks[i].VHost = "team-b"
				}
			},
			want: "source src-1",
		},
		{
			name: "only the sink is shared",
			edit: func(b *storage.WorkflowExportBundle) {
				b.Workflow.ID, b.Workflow.VHost = "wf-2", "team-b"
				b.Sources[0].ID, b.Sources[0].Name, b.Sources[0].VHost = "src-2", "pg (copy)", "team-b"
				b.Sources[0].Config["password"] = "typed"
				b.Workflow.Nodes[0].RefID = "src-2"
			},
			want: "sink snk-1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, mux, h := credentialFixture()
			b, _ := exportCredentialBundle(t, mux)
			tc.edit(&b)
			body, _ := json.Marshal(b)

			rr := httptest.NewRecorder()
			h.ImportWorkflow(rr, httptest.NewRequestWithContext(t.Context(), "POST", "/api/workflows/import", bytes.NewReader(body)))

			if rr.Code != http.StatusConflict {
				t.Fatalf("import = HTTP %d, want 409; body %s", rr.Code, rr.Body.String())
			}
			msg := rr.Body.String()
			for _, part := range []string{tc.want, "default", "team-b", "separate copy"} {
				if !strings.Contains(msg, part) {
					t.Errorf("error %q does not mention %q", msg, part)
				}
			}
			if got := store.sources["src-1"].VHost; got != "default" {
				t.Errorf("the original's source moved to vhost %q", got)
			}
			if got := store.sinks["snk-1"].VHost; got != "default" {
				t.Errorf("the original's sink moved to vhost %q", got)
			}
			if _, ok := store.workflows["wf-2"]; ok {
				t.Error("a workflow was written although the import was refused")
			}
		})
	}
}

// The control: with its connections imported as copies, the workflow lands in
// the other vhost and the original's connections are left as they were.
func TestImportIntoAnotherVHostWithCopiedConnections(t *testing.T) {
	store, mux, h := credentialFixture()
	b, _ := exportCredentialBundle(t, mux)
	b.Workflow.ID, b.Workflow.VHost = "wf-2", "team-b"
	b.Sources[0].ID, b.Sources[0].Name, b.Sources[0].VHost = "src-2", "pg (copy)", "team-b"
	b.Sources[0].Config["password"] = "typed"
	b.Sinks[0].ID, b.Sinks[0].Name, b.Sinks[0].VHost = "snk-2", "hook (copy)", "team-b"
	b.Sinks[0].Config["url"] = "https://hooks.example.com/b"
	b.Sinks[0].Config["api_key"] = "typed"
	b.Workflow.Nodes[0].RefID, b.Workflow.Nodes[1].RefID = "src-2", "snk-2"
	body, _ := json.Marshal(b)

	rr := httptest.NewRecorder()
	h.ImportWorkflow(rr, httptest.NewRequestWithContext(t.Context(), "POST", "/api/workflows/import", bytes.NewReader(body)))
	if rr.Code != http.StatusCreated {
		t.Fatalf("import = HTTP %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	if got := store.sinks["snk-1"]; got.VHost != "default" || got.Config["api_key"] != "S3cret-key" {
		t.Errorf("the original's sink changed: %+v", got)
	}
}

// Importing into the vhost the workflow came from still updates its own
// connections in place: that is what re-importing an edited bundle is for.
func TestImportIntoTheSameVHostStillUpdatesInPlace(t *testing.T) {
	store, mux, h := credentialFixture()
	b, _ := exportCredentialBundle(t, mux)
	b.Workflow.VHost = "default"
	b.Sinks[0].Config["url"] = "https://hooks.example.com/v2"
	body, _ := json.Marshal(b)

	rr := httptest.NewRecorder()
	h.ImportWorkflow(rr, httptest.NewRequestWithContext(t.Context(), "POST", "/api/workflows/import", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("import = HTTP %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	if got := store.sinks["snk-1"].Config["url"]; got != "https://hooks.example.com/v2" {
		t.Errorf("sink url = %q, want the imported one", got)
	}
}
