package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/ml/monitor"
	"github.com/gsoultan/hermod/internal/storage"
)

// ---------------------------------------------------------------------------
// Monitoring over the API: anyone with a role on the vhost reads a model's
// prediction log and drift; an Editor sets how the model is monitored.
// ---------------------------------------------------------------------------

// monitoredStore is modelAPIStore with a prediction log.
type monitoredStore struct {
	*modelAPIStore
	mu   sync.Mutex
	logs []storage.MLPredictionLog
}

func (s *monitoredStore) InsertMLPredictionLogs(_ context.Context, logs []storage.MLPredictionLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, logs...)
	return nil
}
func (s *monitoredStore) ListMLPredictionLogs(_ context.Context, vhost, model string, limit int) ([]storage.MLPredictionLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []storage.MLPredictionLog
	for i := len(s.logs) - 1; i >= 0 && len(out) < limit; i-- {
		if s.logs[i].VHost == vhost && s.logs[i].Model == model {
			out = append(out, s.logs[i])
		}
	}
	return out, nil
}
func (s *monitoredStore) PurgeMLPredictionLogs(context.Context, string, string, time.Time) error {
	return nil
}
func (s *monitoredStore) DeleteMLPredictionLogs(context.Context, string, string) error { return nil }

func (s *monitoredStore) logged() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.logs)
}

// newMonitoredAPI is the model API with a running monitor that writes at once.
func newMonitoredAPI(t *testing.T) (*Handler, *monitoredStore) {
	t.Helper()
	store := &monitoredStore{modelAPIStore: &modelAPIStore{models: map[string]storage.MLModel{}}}
	h := NewHandler(&handlers.Handler{Storage: store, LogStorage: store})
	mon := monitor.New(monitor.Config{Flush: 5 * time.Millisecond}, monitor.Deps{Logs: func() any { return store }})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		mon.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	h.monitor = mon
	return h, store
}

func TestMonitoringAPIRefusesWhoHasNoBusinessThere(t *testing.T) {
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
			h, store := newMonitoredAPI(t)
			store.models["tenant-a/m"] = storage.MLModel{VHost: "tenant-a", Name: "m", Backend: "mlflow", URL: "http://x"}

			if w := call(h.ListPredictionLogs, tc.user, http.MethodGet, "tenant-a", "m", ""); (w.Code == http.StatusOK) != tc.read {
				t.Errorf("predictions: %d, read allowed = %v", w.Code, tc.read)
			}
			if w := call(h.GetDrift, tc.user, http.MethodGet, "tenant-a", "m", ""); (w.Code == http.StatusOK) != tc.read {
				t.Errorf("drift: %d, read allowed = %v", w.Code, tc.read)
			}
			if w := call(h.PutMonitoring, tc.user, http.MethodPut, "tenant-a", "m", `{"log_sample_rate":0.5}`); (w.Code == http.StatusOK) != tc.write {
				t.Errorf("monitoring: %d, write allowed = %v", w.Code, tc.write)
			}
			if got := store.models["tenant-a/m"].Monitoring.LogSampleRate; (got == 0.5) != tc.write {
				t.Errorf("sample rate = %v after a write allowed = %v", got, tc.write)
			}
		})
	}
}

func TestPutMonitoringValidatesAndIsAudited(t *testing.T) {
	h, store := newMonitoredAPI(t)
	store.models["tenant-a/m"] = storage.MLModel{VHost: "tenant-a", Name: "m", Backend: "mlflow", URL: "http://x"}
	for name, body := range map[string]string{
		"not json":         `{`,
		"rate above one":   `{"log_sample_rate":3}`,
		"alert below warn": `{"drift_warn":0.5,"drift_alert":0.2}`,
		"bad retention":    `{"log_retention":"forever"}`,
	} {
		if w := call(h.PutMonitoring, editorA, http.MethodPut, "tenant-a", "m", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", name, w.Code, w.Body)
		}
	}
	if w := call(h.PutMonitoring, editorA, http.MethodPut, "tenant-a", "nope", `{}`); w.Code != http.StatusNotFound {
		t.Errorf("an unknown model: %d", w.Code)
	}
	w := call(h.PutMonitoring, editorA, http.MethodPut, "tenant-a", "m",
		`{"log_sample_rate":1,"log_mask_fields":["email"],"log_mask_type":"email","log_retention":"14d","drift_alert":0.3}`)
	if w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	got := store.models["tenant-a/m"].Monitoring
	if got.LogSampleRate != 1 || got.LogMaskType != "email" || got.LogRetention != "14d" || got.DriftAlert != 0.3 || len(got.LogMaskFields) != 1 {
		t.Errorf("monitoring = %+v", got)
	}
	if len(store.audits) != 1 {
		t.Errorf("audits = %d, want 1", len(store.audits))
	}
}

// Editing a model's definition is not a change to how it is monitored.
func TestPutModelKeepsTheModelsMonitoring(t *testing.T) {
	h, store := newModelAPI()
	url := doubler(t)
	store.models["tenant-a/m"] = storage.MLModel{VHost: "tenant-a", Name: "m", Backend: "mlflow", URL: url,
		Monitoring: storage.MLMonitoring{LogSampleRate: 0.2}}
	if w := call(h.PutModel, editorA, http.MethodPut, "tenant-a", "m", modelBody(url)); w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	if got := store.models["tenant-a/m"].Monitoring.LogSampleRate; got != 0.2 {
		t.Errorf("sample rate = %v after an edit, want 0.2 kept", got)
	}
}

func TestPredictionLogsRecordWhoCalled(t *testing.T) {
	h, store := newMonitoredAPI(t)
	store.models["tenant-a/double"] = storage.MLModel{VHost: "tenant-a", Name: "double", Backend: "mlflow", URL: doubler(t),
		Monitoring: storage.MLMonitoring{LogSampleRate: 1}}

	if w := call(h.Predict, editorA, http.MethodPost, "tenant-a", "double", `{"instances":[{"x":1}]}`); w.Code != http.StatusOK {
		t.Fatalf("predict: %d %s", w.Code, w.Body)
	}
	w := call(h.RotateServingKey, editorA, http.MethodPost, "tenant-a", "double", "")
	var made struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &made)
	if w := call(h.Serve, nil, http.MethodPost, "tenant-a", "double", `{"instances":[{"x":2}]}`, "X-API-Key", made.Key); w.Code != http.StatusOK {
		t.Fatalf("serve: %d %s", w.Code, w.Body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for store.logged() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	w = call(h.ListPredictionLogs, viewerA, http.MethodGet, "tenant-a", "double", "")
	var page struct {
		Data []storage.MLPredictionLog `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if len(page.Data) != 2 || page.Data[0].CallerKind != storage.MLCallerREST || page.Data[1].CallerKind != storage.MLCallerUI {
		t.Fatalf("logs = %+v, want the serving call then the UI's", page.Data)
	}
	if page.Data[0].Outputs["prediction"] != 4.0 {
		t.Errorf("outputs = %v", page.Data[0].Outputs)
	}

	r := httptest.NewRequestWithContext(context.WithValue(context.Background(), handlers.UserContextKey, viewerA),
		http.MethodGet, "/api/vhosts/tenant-a/ml/models/double/predictions?limit=1", nil)
	r.SetPathValue("vhost", "tenant-a")
	r.SetPathValue("name", "double")
	rec := httptest.NewRecorder()
	h.ListPredictionLogs(rec, r)
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Data) != 1 {
		t.Errorf("limit=1 returned %d rows", len(page.Data))
	}
}

func TestDriftOfAModelWithNoReportSaysWhy(t *testing.T) {
	h, store := newMonitoredAPI(t)
	store.models["tenant-a/m"] = storage.MLModel{VHost: "tenant-a", Name: "m", Backend: "mlflow", URL: "http://x"}
	w := call(h.GetDrift, viewerA, http.MethodGet, "tenant-a", "m", "")
	var got struct {
		Report *monitor.Report `json:"report"`
		Reason string          `json:"reason"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK || got.Report != nil || got.Reason == "" {
		t.Errorf("drift: %d %s", w.Code, w.Body)
	}
	if w := call(h.GetDrift, viewerA, http.MethodGet, "tenant-a", "nope", ""); w.Code != http.StatusNotFound {
		t.Errorf("an unknown model: %d", w.Code)
	}
}
