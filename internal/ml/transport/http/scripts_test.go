package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// ---------------------------------------------------------------------------
// Custom training scripts over the API: off unless the server turns them on;
// an Administrator saves and deletes them, an Editor of the vhost reads them
// and trains with them, a Viewer sees only their names.
// ---------------------------------------------------------------------------

type scriptAPIStore struct {
	*modelAPIStore
	scripts map[string][]storage.MLScript // "vhost/name", oldest first
}

func (s *scriptAPIStore) ListMLScripts(_ context.Context, vhost string) ([]storage.MLScript, error) {
	var out []storage.MLScript
	for _, vs := range s.scripts {
		if latest := vs[len(vs)-1]; latest.VHost == vhost {
			latest.Source = ""
			out = append(out, latest)
		}
	}
	return out, nil
}
func (s *scriptAPIStore) ListMLScriptVersions(_ context.Context, vhost, name string) ([]storage.MLScript, error) {
	var out []storage.MLScript
	for _, v := range s.scripts[vhost+"/"+name] {
		v.Source = ""
		out = append([]storage.MLScript{v}, out...)
	}
	return out, nil
}
func (s *scriptAPIStore) GetMLScript(_ context.Context, vhost, name string) (storage.MLScript, error) {
	vs := s.scripts[vhost+"/"+name]
	if len(vs) == 0 {
		return storage.MLScript{}, storage.ErrNotFound
	}
	return vs[len(vs)-1], nil
}
func (s *scriptAPIStore) PutMLScript(_ context.Context, sc storage.MLScript) (storage.MLScript, error) {
	if err := storage.ValidateMLScript(sc); err != nil {
		return storage.MLScript{}, err
	}
	key := sc.VHost + "/" + sc.Name
	sc.SHA256, sc.Version = storage.MLScriptSHA256(sc.Source), len(s.scripts[key])+1
	s.scripts[key] = append(s.scripts[key], sc)
	return sc, nil
}
func (s *scriptAPIStore) DeleteMLScript(_ context.Context, vhost, name string) error {
	if len(s.scripts[vhost+"/"+name]) == 0 {
		return storage.ErrNotFound
	}
	delete(s.scripts, vhost+"/"+name)
	return nil
}
func (s *scriptAPIStore) DeleteMLScripts(context.Context, string) error { return nil }

func newScriptsAPI(t *testing.T, pools ml.Pools) (*Handler, *scriptAPIStore) {
	t.Helper()
	h, models := newModelAPI()
	store := &scriptAPIStore{modelAPIStore: models, scripts: map[string][]storage.MLScript{}}
	h.Storage, h.LogStorage = store, store
	h.worker = (&workerStub{}).start(t)
	h.pools = &pools
	return h, store
}

const trees = "def train(df, spec):\n    ...\n\ndef export_onnx(model, spec):\n    ...\n"

func TestScriptsAreForbiddenWhileTheServerHasThemOff(t *testing.T) {
	h, _ := newScriptsAPI(t, ml.Pools{Custom: worker.New("http://custom", "", nil)})
	vals := map[string]string{"vhost": "tenant-a", "name": "trees"}
	for name, w := range map[string]interface{ Result() *http.Response }{
		"list": do(h.ListScripts, admin, http.MethodGet, "/x", nil, vals),
		"get":  do(h.GetScript, admin, http.MethodGet, "/x", nil, vals),
		"put":  do(h.PutScript, admin, http.MethodPut, "/x", strings.NewReader(`{"source":"x"}`), vals),
		"train": do(h.TrainModel, editorA, http.MethodPost, "/x",
			strings.NewReader(`{"dataset":"customers","target":"churned","algorithm":"custom:trees"}`),
			map[string]string{"vhost": "tenant-a", "name": "churn"}),
	} {
		resp := w.Result()
		var body struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden || !strings.Contains(body.Error, "HERMOD_ML_CUSTOM_SCRIPTS") {
			t.Errorf("%s = %d %q, want 403 naming HERMOD_ML_CUSTOM_SCRIPTS", name, resp.StatusCode, body.Error)
		}
	}
}

func TestOnlyAnAdministratorSavesOrDeletesAScript(t *testing.T) {
	h, store := newScriptsAPI(t, ml.Pools{CustomScripts: true, Custom: worker.New("http://custom", "", nil)})
	vals := map[string]string{"vhost": "tenant-a", "name": "trees"}
	body := `{"source":` + mustJSON(trees) + `,"description":"extra trees"}`

	if w := do(h.PutScript, editorA, http.MethodPut, "/x", strings.NewReader(body), vals); w.Code != http.StatusForbidden {
		t.Errorf("an Editor saved a script: %d %s", w.Code, w.Body)
	}
	w := do(h.PutScript, admin, http.MethodPut, "/x", strings.NewReader(body), vals)
	if w.Code != http.StatusOK {
		t.Fatalf("put = %d %s", w.Code, w.Body)
	}
	var saved storage.MLScript
	_ = json.Unmarshal(w.Body.Bytes(), &saved)
	if saved.Version != 1 || saved.SHA256 != storage.MLScriptSHA256(trees) || saved.CreatedBy != "root" || saved.Source != "" {
		t.Errorf("saved = %+v, want version 1 by root, without its source echoed", saved)
	}
	if !hasAudit(store.modelAPIStore, `"sha256":"`+saved.SHA256+`"`) {
		t.Error("saving a script was not audited with its SHA-256")
	}

	if w := do(h.ListScripts, viewerA, http.MethodGet, "/x", nil, vals); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `"trees"`) || strings.Contains(w.Body.String(), "export_onnx") {
		t.Errorf("a Viewer's list = %d %s, want names without source", w.Code, w.Body)
	}
	if w := do(h.GetScript, viewerA, http.MethodGet, "/x", nil, vals); w.Code != http.StatusForbidden {
		t.Errorf("a Viewer read a script's source: %d", w.Code)
	}
	if w := do(h.GetScript, editorB, http.MethodGet, "/x", nil, vals); w.Code != http.StatusForbidden {
		t.Errorf("another vhost's Editor read the script: %d", w.Code)
	}
	w = do(h.GetScript, editorA, http.MethodGet, "/x", nil, vals)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "export_onnx") || !strings.Contains(w.Body.String(), `"versions"`) {
		t.Errorf("an Editor's get = %d %s", w.Code, w.Body)
	}

	if w := do(h.DeleteScript, editorA, http.MethodDelete, "/x", nil, vals); w.Code != http.StatusForbidden {
		t.Errorf("an Editor deleted a script: %d", w.Code)
	}
	if w := do(h.DeleteScript, admin, http.MethodDelete, "/x", nil, vals); w.Code != http.StatusNoContent {
		t.Errorf("delete = %d %s", w.Code, w.Body)
	}
	if !hasAudit(store.modelAPIStore, `"script":"trees"`) {
		t.Error("deleting a script was not audited")
	}
	if w := do(h.DeleteScript, admin, http.MethodDelete, "/x", nil, vals); w.Code != http.StatusNotFound {
		t.Errorf("deleting twice = %d", w.Code)
	}
}

func TestAnInvalidScriptIsABadRequest(t *testing.T) {
	h, _ := newScriptsAPI(t, ml.Pools{CustomScripts: true})
	for name, body := range map[string]string{
		"empty source": `{"source":"   "}`,
		"not json":     `source=x`,
	} {
		w := do(h.PutScript, admin, http.MethodPut, "/x", strings.NewReader(body), map[string]string{"vhost": "tenant-a", "name": "trees"})
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", name, w.Code, w.Body)
		}
	}
	w := do(h.PutScript, admin, http.MethodPut, "/x", strings.NewReader(`{"source":"x"}`), map[string]string{"vhost": "tenant-a", "name": "../x"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("a path as a name = %d %s", w.Code, w.Body)
	}
}

func TestTrainingIsRefusedClearlyWhenItsPoolIsMissing(t *testing.T) {
	h, _ := newScriptsAPI(t, ml.Pools{CustomScripts: true})
	vals := map[string]string{"vhost": "tenant-a", "name": "churn"}
	for _, tc := range []struct {
		body     string
		status   int
		fragment string
	}{
		{`{"dataset":"customers","target":"churned","algorithm":"custom:trees"}`, http.StatusServiceUnavailable, "HERMOD_ML_CUSTOM_WORKER_URL"},
		{`{"dataset":"customers","target":"churned","device":"gpu"}`, http.StatusServiceUnavailable, "HERMOD_ML_GPU_WORKER_URL"},
		{`{"dataset":"customers","target":"churned","device":"tpu"}`, http.StatusBadRequest, "tpu"},
	} {
		w := do(h.TrainModel, editorA, http.MethodPost, "/x", strings.NewReader(tc.body), vals)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.fragment) {
			t.Errorf("%s = %d %s, want %d naming %s", tc.body, w.Code, w.Body, tc.status, tc.fragment)
		}
	}
}

func TestWorkerStatusSaysWhichTrainingOptionsExist(t *testing.T) {
	h, _ := newScriptsAPI(t, ml.Pools{CustomScripts: true, Custom: worker.New("http://custom", "", nil)})
	w := do(h.WorkerStatus, viewerA, http.MethodGet, "/api/ml/worker", nil, nil)
	var status struct {
		Capabilities ml.Capabilities `json:"capabilities"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &status)
	if !status.Capabilities.CustomScripts || !status.Capabilities.ScriptsEnabled || status.Capabilities.GPU {
		t.Errorf("status = %s", w.Body)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
