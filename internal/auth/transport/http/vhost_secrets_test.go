package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

// ---------------------------------------------------------------------------
// A vhost's secrets over the API.
//
// The API is how a secret gets into Hermod, so it is also where one could get
// out. It lists names and never values; a write is for an Administrator or an
// Editor who has the vhost; and only a worker -- which already receives
// decrypted connector configs to run a workflow -- can read a value back.
// ---------------------------------------------------------------------------

const secretValue = "s3cret-value-do-not-leak"

type secretAPIStore struct {
	storage.Storage
	secrets map[string]storage.VHostSecret
	logs    []storage.Log
	audits  []storage.AuditLog
}

func newSecretAPI() (*AuthHandler, *secretAPIStore) {
	store := &secretAPIStore{secrets: map[string]storage.VHostSecret{}}
	return &AuthHandler{Handler: &handlers.Handler{Storage: store, LogStorage: store}}, store
}

func (s *secretAPIStore) CreateLog(_ context.Context, l storage.Log) error {
	s.logs = append(s.logs, l)
	return nil
}

func (s *secretAPIStore) CreateAuditLog(_ context.Context, l storage.AuditLog) error {
	s.audits = append(s.audits, l)
	return nil
}

func (s *secretAPIStore) ListVHostSecrets(_ context.Context, vhost string) ([]storage.VHostSecret, error) {
	var out []storage.VHostSecret
	for _, sec := range s.secrets {
		if sec.VHost == vhost {
			sec.Value = ""
			out = append(out, sec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *secretAPIStore) GetVHostSecret(_ context.Context, vhost, name string) (storage.VHostSecret, error) {
	sec, ok := s.secrets[vhost+"/"+name]
	if !ok {
		return storage.VHostSecret{}, storage.ErrNotFound
	}
	return sec, nil
}

func (s *secretAPIStore) PutVHostSecret(_ context.Context, sec storage.VHostSecret) error {
	if err := storage.ValidateVHostSecret(sec); err != nil {
		return err
	}
	sec.UpdatedAt = time.Now()
	s.secrets[sec.VHost+"/"+sec.Name] = sec
	return nil
}

func (s *secretAPIStore) DeleteVHostSecret(_ context.Context, vhost, name string) error {
	if _, ok := s.secrets[vhost+"/"+name]; !ok {
		return storage.ErrNotFound
	}
	delete(s.secrets, vhost+"/"+name)
	return nil
}

func (s *secretAPIStore) DeleteVHostSecrets(context.Context, string) error { return nil }

var (
	secretAdmin   = &storage.User{ID: "u-admin", Username: "root", Role: storage.RoleAdministrator, VHosts: []string{"*"}}
	secretEditorA = &storage.User{ID: "u-ed-a", Username: "ada", Role: storage.RoleEditor, VHosts: []string{"tenant-a"}}
	secretEditorB = &storage.User{ID: "u-ed-b", Username: "bob", Role: storage.RoleEditor, VHosts: []string{"tenant-b"}}
	secretViewerA = &storage.User{ID: "u-vw-a", Username: "vic", Role: storage.RoleViewer, VHosts: []string{"tenant-a"}}
	secretWorker  = &storage.User{ID: "worker:w1", Username: "worker:w1", Role: storage.RoleEditor, VHosts: []string{"*"}}
)

// secretCall runs one handler as user and returns the response.
func secretCall(h http.HandlerFunc, user *storage.User, method, vhost, name, body string) *httptest.ResponseRecorder {
	ctx := context.Background()
	if user != nil {
		ctx = context.WithValue(ctx, handlers.UserContextKey, user)
	}
	r := httptest.NewRequestWithContext(ctx, method, "/api/vhosts/"+vhost+"/secrets/"+name, bytes.NewBufferString(body))
	r.SetPathValue("vhost", vhost)
	r.SetPathValue("name", name)
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func putBody(value string) string {
	b, _ := json.Marshal(map[string]string{"value": value})
	return string(b)
}

func TestVHostSecretAPICreatesRotatesListsAndDeletes(t *testing.T) {
	h, store := newSecretAPI()

	if w := secretCall(h.PutVHostSecret, secretEditorA, http.MethodPut, "tenant-a", "API_KEY", putBody("first")); w.Code != http.StatusNoContent {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if w := secretCall(h.PutVHostSecret, secretAdmin, http.MethodPut, "tenant-a", "API_KEY", putBody(secretValue)); w.Code != http.StatusNoContent {
		t.Fatalf("rotate: %d %s", w.Code, w.Body)
	}
	if got := store.secrets["tenant-a/API_KEY"]; got.Value != secretValue || got.UpdatedBy != "root" {
		t.Errorf("stored %q by %q, want the rotated value by root", got.Value, got.UpdatedBy)
	}

	w := secretCall(h.ListVHostSecrets, secretEditorA, http.MethodGet, "tenant-a", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	var list struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("list body: %v", err)
	}
	if len(list.Data) != 1 || list.Data[0]["name"] != "API_KEY" || list.Data[0]["updated_by"] != "root" {
		t.Errorf("list = %v, want one entry API_KEY updated by root", list.Data)
	}
	if _, has := list.Data[0]["value"]; has {
		t.Error("the list has a value field")
	}

	if w := secretCall(h.DeleteVHostSecret, secretEditorA, http.MethodDelete, "tenant-a", "API_KEY", ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w := secretCall(h.DeleteVHostSecret, secretEditorA, http.MethodDelete, "tenant-a", "API_KEY", ""); w.Code != http.StatusNotFound {
		t.Errorf("deleting what is not there: %d, want 404", w.Code)
	}
}

// Who may touch a vhost's secrets. An Editor of another vhost and a Viewer are
// refused everything, the list included: the names are a map of what a tenant
// integrates with.
func TestVHostSecretAPIRefusesWhoHasNoBusinessThere(t *testing.T) {
	tests := []struct {
		name string
		user *storage.User
		want int
	}{
		{"an administrator", secretAdmin, http.StatusNoContent},
		{"an editor of the vhost", secretEditorA, http.StatusNoContent},
		{"an editor of another vhost", secretEditorB, http.StatusForbidden},
		{"a viewer of the vhost", secretViewerA, http.StatusForbidden},
		{"nobody", nil, http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, store := newSecretAPI()
			store.secrets["tenant-a/EXISTING"] = storage.VHostSecret{VHost: "tenant-a", Name: "EXISTING", Value: secretValue}
			allowed := tc.want == http.StatusNoContent

			if w := secretCall(h.PutVHostSecret, tc.user, http.MethodPut, "tenant-a", "API_KEY", putBody("v")); w.Code != tc.want {
				t.Errorf("put: %d, want %d", w.Code, tc.want)
			}
			if _, saved := store.secrets["tenant-a/API_KEY"]; saved != allowed {
				t.Errorf("put stored the secret = %v, want %v", saved, allowed)
			}

			w := secretCall(h.ListVHostSecrets, tc.user, http.MethodGet, "tenant-a", "", "")
			if (w.Code == http.StatusOK) != allowed {
				t.Errorf("list: %d, allowed = %v", w.Code, allowed)
			}
			if !allowed && strings.Contains(w.Body.String(), "EXISTING") {
				t.Errorf("a refused list still named a secret: %s", w.Body)
			}

			if w := secretCall(h.DeleteVHostSecret, tc.user, http.MethodDelete, "tenant-a", "EXISTING", ""); w.Code != tc.want {
				t.Errorf("delete: %d, want %d", w.Code, tc.want)
			}
			if _, kept := store.secrets["tenant-a/EXISTING"]; kept == allowed {
				t.Errorf("delete left the secret = %v, want %v", kept, !allowed)
			}
		})
	}
}

func TestVHostSecretAPIRefusesABadRequest(t *testing.T) {
	tests := []struct {
		name, vhost, secret, body string
	}{
		{"a name with a dash", "tenant-a", "API-KEY", putBody("v")},
		{"a name starting with a digit", "tenant-a", "1KEY", putBody("v")},
		{"a name with a slash", "tenant-a", "A/B", putBody("v")},
		{"an empty value", "tenant-a", "API_KEY", putBody("")},
		{"a body that is not JSON", "tenant-a", "API_KEY", "value=v"},
		{"an oversize value", "tenant-a", "API_KEY", putBody(strings.Repeat("x", storage.MaxVHostSecretValueLen+1))},
		{"every vhost at once", "all", "API_KEY", putBody("v")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, store := newSecretAPI()
			w := secretCall(h.PutVHostSecret, secretAdmin, http.MethodPut, tc.vhost, tc.secret, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status %d, want 400: %s", w.Code, w.Body)
			}
			if len(store.secrets) != 0 {
				t.Errorf("a refused request stored %v", store.secrets)
			}
		})
	}
}

// Nothing the API says, and nothing it logs, contains a value: not a
// response, not the activity log, not the audit trail.
func TestVHostSecretAPINeverRepeatsAValue(t *testing.T) {
	h, store := newSecretAPI()
	var said []string
	record := func(w *httptest.ResponseRecorder) { said = append(said, w.Body.String()) }

	record(secretCall(h.PutVHostSecret, secretEditorA, http.MethodPut, "tenant-a", "API_KEY", putBody(secretValue)))
	record(secretCall(h.ListVHostSecrets, secretEditorA, http.MethodGet, "tenant-a", "", ""))
	record(secretCall(h.GetVHostSecretForWorker, secretEditorA, http.MethodGet, "tenant-a", "API_KEY", ""))
	record(secretCall(h.GetVHostSecretForWorker, secretAdmin, http.MethodGet, "tenant-a", "API_KEY", ""))
	record(secretCall(h.DeleteVHostSecret, secretEditorA, http.MethodDelete, "tenant-a", "API_KEY", ""))

	for _, l := range store.logs {
		said = append(said, l.Message, l.Data)
	}
	for _, a := range store.audits {
		said = append(said, a.Payload, a.EntityID)
	}
	for _, text := range said {
		if strings.Contains(text, secretValue) {
			t.Errorf("a value was repeated: %s", text)
		}
	}
	if len(store.audits) != 2 {
		t.Errorf("%d audit entries, want one for the save and one for the delete", len(store.audits))
	}
	for _, a := range store.audits {
		if !strings.Contains(a.Payload, "API_KEY") || a.Username != "ada" {
			t.Errorf("audit entry does not say who changed which secret: %+v", a)
		}
	}
}

// A worker runs workflows in its own process and reads a secret's value to do
// so. No person can: the route answers a worker's token and nothing else.
func TestOnlyAWorkerReadsAVHostSecretsValue(t *testing.T) {
	h, store := newSecretAPI()
	store.secrets["tenant-a/API_KEY"] = storage.VHostSecret{VHost: "tenant-a", Name: "API_KEY", Value: secretValue}

	w := secretCall(h.GetVHostSecretForWorker, secretWorker, http.MethodGet, "tenant-a", "API_KEY", "")
	if w.Code != http.StatusOK {
		t.Fatalf("a worker: %d %s", w.Code, w.Body)
	}
	var got struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Value != secretValue {
		t.Errorf("a worker read %q (%v), want the value", got.Value, err)
	}
	if w := secretCall(h.GetVHostSecretForWorker, secretWorker, http.MethodGet, "tenant-a", "MISSING", ""); w.Code != http.StatusNotFound {
		t.Errorf("a worker asking for a missing secret: %d, want 404", w.Code)
	}

	for _, user := range []*storage.User{secretAdmin, secretEditorA, secretViewerA, nil} {
		w := secretCall(h.GetVHostSecretForWorker, user, http.MethodGet, "tenant-a", "API_KEY", "")
		if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), secretValue) {
			t.Errorf("%v read a value: %d %s", user, w.Code, w.Body)
		}
	}
}
