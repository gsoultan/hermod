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

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
)

// ---------------------------------------------------------------------------
// Models over the API: an Editor of a vhost defines and tests its models, a
// Viewer of it may read them, nobody else may do either; an application calls
// a model with that model's own serving key and nothing else.
// ---------------------------------------------------------------------------

type modelAPIStore struct {
	storage.Storage
	models map[string]storage.MLModel
	quotas map[string]storage.MLQuotas
	audits []storage.AuditLog
	// claimedBy holds the one training claim (retrain_test.go).
	claimedBy string
}

func (s *modelAPIStore) CreateAuditLog(_ context.Context, l storage.AuditLog) error {
	s.audits = append(s.audits, l)
	return nil
}
func (s *modelAPIStore) CreateLog(context.Context, storage.Log) error { return nil }

func (s *modelAPIStore) ListMLModels(_ context.Context, vhost string) ([]storage.MLModel, error) {
	var out []storage.MLModel
	for _, m := range s.models {
		if m.VHost == vhost {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (s *modelAPIStore) GetMLModel(_ context.Context, vhost, name string) (storage.MLModel, error) {
	m, ok := s.models[vhost+"/"+name]
	if !ok {
		return storage.MLModel{}, storage.ErrNotFound
	}
	return m, nil
}
func (s *modelAPIStore) PutMLModel(_ context.Context, m storage.MLModel) error {
	if err := storage.ValidateMLModel(m); err != nil {
		return err
	}
	if old, ok := s.models[m.VHost+"/"+m.Name]; ok {
		m.ServingKeyHash, m.Serving = old.ServingKeyHash, old.Serving
	}
	s.models[m.VHost+"/"+m.Name] = m
	return nil
}
func (s *modelAPIStore) SetMLModelServingKey(_ context.Context, vhost, name, hash string) error {
	m, ok := s.models[vhost+"/"+name]
	if !ok {
		return storage.ErrNotFound
	}
	m.ServingKeyHash, m.Serving = hash, hash != ""
	s.models[vhost+"/"+name] = m
	return nil
}
func (s *modelAPIStore) DeleteMLModel(_ context.Context, vhost, name string) error {
	if _, ok := s.models[vhost+"/"+name]; !ok {
		return storage.ErrNotFound
	}
	delete(s.models, vhost+"/"+name)
	return nil
}
func (s *modelAPIStore) DeleteMLModels(context.Context, string) error { return nil }

func newModelAPI() (*Handler, *modelAPIStore) {
	store := &modelAPIStore{models: map[string]storage.MLModel{}}
	return NewHandler(&handlers.Handler{Storage: store, LogStorage: store}), store
}

var (
	admin   = &storage.User{ID: "u-admin", Username: "root", Role: storage.RoleAdministrator, VHosts: []string{"*"}}
	editorA = &storage.User{ID: "u-ed-a", Username: "ada", Role: storage.RoleEditor, VHosts: []string{"tenant-a"}}
	editorB = &storage.User{ID: "u-ed-b", Username: "bob", Role: storage.RoleEditor, VHosts: []string{"tenant-b"}}
	viewerA = &storage.User{ID: "u-vw-a", Username: "vic", Role: storage.RoleViewer, VHosts: []string{"tenant-a"}}
)

// call runs one handler as user. path values are vhost and name.
func call(h http.HandlerFunc, user *storage.User, method, vhost, name, body string, header ...string) *httptest.ResponseRecorder {
	ctx := context.Background()
	if user != nil {
		ctx = context.WithValue(ctx, handlers.UserContextKey, user)
	}
	r := httptest.NewRequestWithContext(ctx, method, "/api/vhosts/"+vhost+"/ml/models/"+name, bytes.NewBufferString(body))
	r.SetPathValue("vhost", vhost)
	r.SetPathValue("name", name)
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

// doubler is an MLflow server that doubles x.
func doubler(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Records []map[string]float64 `json:"dataframe_records"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		preds := make([]float64, len(body.Records))
		for i, rec := range body.Records {
			preds[i] = rec["x"] * 2
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"predictions": preds})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func modelBody(url string) string {
	b, _ := json.Marshal(map[string]any{"backend": "mlflow", "url": url, "description": "doubles x"})
	return string(b)
}

func TestModelAPIDefinesListsTestsAndDeletes(t *testing.T) {
	h, store := newModelAPI()
	url := doubler(t)

	if w := call(h.PutModel, editorA, http.MethodPut, "tenant-a", "double", modelBody(url)); w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	if got := store.models["tenant-a/double"]; got.URL != url || got.UpdatedBy != "ada" || got.VHost != "tenant-a" {
		t.Errorf("stored %+v", got)
	}
	if len(store.audits) == 0 {
		t.Error("defining a model left no audit entry")
	}

	w := call(h.ListModels, viewerA, http.MethodGet, "tenant-a", "", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"double"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}

	w = call(h.Predict, editorA, http.MethodPost, "tenant-a", "double", `{"instances":[{"x":2},{"x":21}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("predict: %d %s", w.Code, w.Body)
	}
	var out struct {
		Model       string           `json:"model"`
		Predictions []map[string]any `json:"predictions"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Model != "double" || len(out.Predictions) != 2 || out.Predictions[1]["prediction"] != 42.0 {
		t.Errorf("predict body = %s", w.Body)
	}

	if w := call(h.DeleteModel, editorA, http.MethodDelete, "tenant-a", "double", ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w := call(h.GetModel, editorA, http.MethodGet, "tenant-a", "double", ""); w.Code != http.StatusNotFound {
		t.Errorf("get after delete: %d, want 404", w.Code)
	}
}

func TestModelAPIRefusesAnInvalidDefinition(t *testing.T) {
	h, store := newModelAPI()
	for name, body := range map[string]string{
		"not json":          `{`,
		"unknown backend":   `{"backend":"pickle","url":"http://m"}`,
		"file url":          `{"backend":"mlflow","url":"file:///etc/passwd"}`,
		"oip without model": `{"backend":"oip","url":"http://m"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if w := call(h.PutModel, editorA, http.MethodPut, "tenant-a", "m", body); w.Code != http.StatusBadRequest {
				t.Errorf("%d %s, want 400", w.Code, w.Body)
			}
		})
	}
	if w := call(h.PutModel, editorA, http.MethodPut, "tenant-a", "bad;name", modelBody("http://m")); w.Code != http.StatusBadRequest {
		t.Errorf("bad name: %d, want 400", w.Code)
	}
	if len(store.models) != 0 {
		t.Errorf("an invalid model was stored: %+v", store.models)
	}
}

func TestModelAPIRefusesWhoHasNoBusinessThere(t *testing.T) {
	tests := []struct {
		name        string
		user        *storage.User
		read, write bool
	}{
		{"an administrator", admin, true, true},
		{"an editor of the vhost", editorA, true, true},
		{"a viewer of the vhost", viewerA, true, false},
		{"an editor of another vhost", editorB, false, false},
		{"nobody", nil, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, store := newModelAPI()
			url := doubler(t)
			store.models["tenant-a/m"] = storage.MLModel{VHost: "tenant-a", Name: "m", Backend: "mlflow", URL: url}

			if w := call(h.ListModels, tc.user, http.MethodGet, "tenant-a", "", ""); (w.Code == http.StatusOK) != tc.read {
				t.Errorf("list: %d, read allowed = %v", w.Code, tc.read)
			}
			if w := call(h.GetModel, tc.user, http.MethodGet, "tenant-a", "m", ""); (w.Code == http.StatusOK) != tc.read {
				t.Errorf("get: %d, read allowed = %v", w.Code, tc.read)
			}
			if w := call(h.Predict, tc.user, http.MethodPost, "tenant-a", "m", `{"instances":[{"x":1}]}`); (w.Code == http.StatusOK) != tc.write {
				t.Errorf("predict: %d, write allowed = %v", w.Code, tc.write)
			}
			if w := call(h.RotateServingKey, tc.user, http.MethodPost, "tenant-a", "m", ""); (w.Code == http.StatusOK) != tc.write {
				t.Errorf("serving key: %d, write allowed = %v", w.Code, tc.write)
			}
			if w := call(h.PutModel, tc.user, http.MethodPut, "tenant-a", "n", modelBody(url)); (w.Code == http.StatusOK) != tc.write {
				t.Errorf("put: %d, write allowed = %v", w.Code, tc.write)
			}
			if w := call(h.DeleteModel, tc.user, http.MethodDelete, "tenant-a", "m", ""); (w.Code == http.StatusNoContent) != tc.write {
				t.Errorf("delete: %d, write allowed = %v", w.Code, tc.write)
			}
		})
	}
}

func TestServeEndpointNeedsTheModelsOwnKey(t *testing.T) {
	h, store := newModelAPI()
	url := doubler(t)
	store.models["tenant-a/double"] = storage.MLModel{VHost: "tenant-a", Name: "double", Backend: "mlflow", URL: url}
	store.models["tenant-a/other"] = storage.MLModel{VHost: "tenant-a", Name: "other", Backend: "mlflow", URL: url}
	body := `{"instances":[{"x":4}]}`

	if w := call(h.Serve, nil, http.MethodPost, "tenant-a", "double", body, "X-API-Key", "hml_guess"); w.Code != http.StatusUnauthorized {
		t.Errorf("before serving is on: %d, want 401", w.Code)
	}

	w := call(h.RotateServingKey, editorA, http.MethodPost, "tenant-a", "double", "")
	if w.Code != http.StatusOK {
		t.Fatalf("rotate: %d %s", w.Code, w.Body)
	}
	var made struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &made)
	if made.Key == "" {
		t.Fatalf("no key in %s", w.Body)
	}
	for _, a := range store.audits {
		if strings.Contains(a.Payload, made.Key) {
			t.Error("the serving key was written to the audit log")
		}
	}

	if w := call(h.Serve, nil, http.MethodPost, "tenant-a", "double", body, "X-API-Key", made.Key); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "8") {
		t.Errorf("with the key: %d %s", w.Code, w.Body)
	}
	if w := call(h.Serve, nil, http.MethodPost, "tenant-a", "double", body, "Authorization", "Bearer "+made.Key); w.Code != http.StatusOK {
		t.Errorf("with the key as a bearer token: %d %s", w.Code, w.Body)
	}
	if w := call(h.Serve, nil, http.MethodPost, "tenant-a", "double", body); w.Code != http.StatusUnauthorized {
		t.Errorf("without a key: %d, want 401", w.Code)
	}
	if w := call(h.Serve, nil, http.MethodPost, "tenant-a", "other", body, "X-API-Key", made.Key); w.Code != http.StatusUnauthorized {
		t.Errorf("one model's key on another: %d, want 401", w.Code)
	}
	if w := call(h.Serve, nil, http.MethodPost, "tenant-b", "double", body, "X-API-Key", made.Key); w.Code != http.StatusUnauthorized {
		t.Errorf("the key on another vhost: %d, want 401", w.Code)
	}

	if w := call(h.DisableServing, editorA, http.MethodDelete, "tenant-a", "double", ""); w.Code != http.StatusNoContent {
		t.Fatalf("disable: %d %s", w.Code, w.Body)
	}
	if w := call(h.Serve, nil, http.MethodPost, "tenant-a", "double", body, "X-API-Key", made.Key); w.Code != http.StatusUnauthorized {
		t.Errorf("after disabling: %d, want 401", w.Code)
	}
}

func TestPredictRefusesABadBody(t *testing.T) {
	h, store := newModelAPI()
	store.models["tenant-a/m"] = storage.MLModel{VHost: "tenant-a", Name: "m", Backend: "mlflow", URL: doubler(t)}
	for name, body := range map[string]string{
		"not json":     `{`,
		"no instances": `{}`,
		"too many":     `{"instances":[` + strings.Repeat(`{"x":1},`, 1000) + `{"x":1}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if w := call(h.Predict, editorA, http.MethodPost, "tenant-a", "m", body); w.Code != http.StatusBadRequest {
				t.Errorf("%d %s, want 400", w.Code, w.Body)
			}
		})
	}
}
