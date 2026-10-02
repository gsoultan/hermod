package http

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/storage"
	sqlstorage "github.com/gsoultan/hermod/internal/storage/sql"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// A preview shows what an expression produced, so a preview that resolves
// secret("NAME") shows a secret. The API never returns a stored value; this is
// the one place a value can still be seen, and so the place that has to check
// whose it is. An Editor of tenant-b who names tenant-a in a Test request would
// otherwise read tenant-a's secrets off the Live Preview panel.
// ---------------------------------------------------------------------------

func newSecretPreviewHandler(t *testing.T) *WorkflowHandler {
	t.Helper()
	t.Cleanup(func() { evaluator.SetSecretSource(nil) })
	db, err := sql.Open("sqlite", "file:secret_preview_"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := sqlstorage.NewSQLStorage(db, "sqlite")
	if err := store.Init(t.Context()); err != nil {
		t.Fatalf("init store: %v", err)
	}
	if err := store.(storage.VHostSecretStore).PutVHostSecret(t.Context(),
		storage.VHostSecret{VHost: "tenant-a", Name: "API_KEY", Value: "a-key-do-not-leak"}); err != nil {
		t.Fatalf("PutVHostSecret: %v", err)
	}
	return &WorkflowHandler{Handler: &handlers.Handler{Storage: store, Registry: registry.NewRegistry(store)}}
}

func previewAs(t *testing.T, h http.HandlerFunc, user *storage.User, path string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	ctx := context.WithValue(t.Context(), handlers.UserContextKey, user)
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

var (
	previewAdmin   = &storage.User{ID: "u-admin", Username: "root", Role: storage.RoleAdministrator, VHosts: []string{"*"}}
	previewEditorA = &storage.User{ID: "u-a", Username: "ada", Role: storage.RoleEditor, VHosts: []string{"tenant-a"}}
	previewEditorB = &storage.User{ID: "u-b", Username: "bob", Role: storage.RoleEditor, VHosts: []string{"tenant-b"}}
)

func secretNodeRequest(vhost string) map[string]any {
	req := map[string]any{
		"transformation": map[string]any{"type": "set", "config": map[string]any{
			"transType": "set", "column.out": "secret('API_KEY')",
		}},
		"message": map[string]any{"id": 1},
	}
	if vhost != "" {
		req["vhost"] = vhost
	}
	return req
}

func TestNodePreviewReadsAVHostsSecretsOnlyForWhoHasTheVHost(t *testing.T) {
	tests := []struct {
		name   string
		user   *storage.User
		vhost  string
		status int
		want   any
	}{
		{"an editor of the vhost", previewEditorA, "tenant-a", http.StatusOK, "a-key-do-not-leak"},
		{"an administrator", previewAdmin, "tenant-a", http.StatusOK, "a-key-do-not-leak"},
		{"an editor of another vhost", previewEditorB, "tenant-a", http.StatusForbidden, nil},
		{"a preview that names no vhost", previewEditorB, "", http.StatusOK, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newSecretPreviewHandler(t)
			rec := previewAs(t, h.TestTransformation, tc.user, "/api/transformations/test", secretNodeRequest(tc.vhost))
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.status, rec.Body)
			}
			if tc.status != http.StatusOK {
				if strings.Contains(rec.Body.String(), "a-key-do-not-leak") {
					t.Errorf("a refused preview carried the secret: %s", rec.Body)
				}
				return
			}
			var resp map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode %q: %v", rec.Body, err)
			}
			if got := previewedField(t, resp, "out"); got != tc.want {
				t.Errorf("out = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// Run Simulation sends the whole workflow, vhost included. Naming a vhost the
// caller does not have is refused before anything runs.
func TestSimulationOfAnotherVHostsWorkflowIsRefused(t *testing.T) {
	workflow := map[string]any{
		"id": "wf-1", "vhost": "tenant-a",
		"nodes": []map[string]any{
			{"id": "src", "type": "source", "ref_id": "src-1"},
			{"id": "t1", "type": "transformation", "config": map[string]any{
				"transType": "set", "column.out": "secret('API_KEY')",
			}},
			{"id": "snk", "type": "sink", "ref_id": "snk-1"},
		},
		"edges": []map[string]any{
			{"id": "e1", "source_id": "src", "target_id": "t1"},
			{"id": "e2", "source_id": "t1", "target_id": "snk"},
		},
	}
	h := newSecretPreviewHandler(t)
	rec := previewAs(t, h.TestWorkflow, previewEditorB, "/api/workflows/test",
		map[string]any{"workflow": workflow, "message": map[string]any{"id": 1}})
	if rec.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403: %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "a-key-do-not-leak") {
		t.Errorf("the simulation carried tenant-a's secret to an editor of tenant-b: %s", rec.Body)
	}
}
