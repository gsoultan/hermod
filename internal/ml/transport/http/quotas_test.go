package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

func (s *modelAPIStore) GetMLQuotas(_ context.Context, vhost string) (storage.MLQuotas, error) {
	q, ok := s.quotas[vhost]
	if !ok {
		return storage.MLQuotas{}, storage.ErrNotFound
	}
	return q, nil
}
func (s *modelAPIStore) PutMLQuotas(_ context.Context, q storage.MLQuotas) error {
	if s.quotas == nil {
		s.quotas = map[string]storage.MLQuotas{}
	}
	s.quotas[q.VHost] = q
	return nil
}
func (s *modelAPIStore) DeleteMLQuotas(_ context.Context, vhost string) error {
	delete(s.quotas, vhost)
	return nil
}

func TestQuotasAreReadByTheVHostAndSetByAnAdministrator(t *testing.T) {
	h, store := newModelAPI()
	vals := map[string]string{"vhost": "tenant-a"}

	w := do(h.GetQuotas, viewerA, http.MethodGet, "/api/vhosts/tenant-a/ml/quotas", nil, vals)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"effective"`) {
		t.Fatalf("a viewer of the vhost reading its quotas: %d %s", w.Code, w.Body)
	}
	if w := do(h.GetQuotas, editorB, http.MethodGet, "/x", nil, vals); w.Code != http.StatusForbidden {
		t.Errorf("an editor of another vhost read tenant-a's quotas: %d", w.Code)
	}

	body := `{"max_models": 2, "max_predictions_per_second": 50, "max_datasets": null}`
	if w := do(h.PutQuotas, editorA, http.MethodPut, "/x", strings.NewReader(body), vals); w.Code != http.StatusForbidden {
		t.Errorf("an editor set quotas: %d %s", w.Code, w.Body)
	}
	w = do(h.PutQuotas, admin, http.MethodPut, "/x", strings.NewReader(body), vals)
	if w.Code != http.StatusOK {
		t.Fatalf("an administrator setting quotas: %d %s", w.Code, w.Body)
	}
	q := store.quotas["tenant-a"]
	if q.VHost != "tenant-a" || q.MaxModels == nil || *q.MaxModels != 2 || q.MaxDatasets != nil || q.UpdatedBy != "root" {
		t.Errorf("stored = %+v", q)
	}
	if !hasAudit(store, `"max_models":2`) {
		t.Error("setting quotas was not audited")
	}

	var got struct {
		Quotas    storage.MLQuotas `json:"quotas"`
		Effective struct {
			MaxModels int64 `json:"max_models"`
		} `json:"effective"`
	}
	w = do(h.GetQuotas, viewerA, http.MethodGet, "/x", nil, vals)
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Effective.MaxModels != 2 || got.Quotas.MaxModels == nil {
		t.Errorf("read back = %s", w.Body)
	}

	if w := do(h.PutQuotas, admin, http.MethodPut, "/x", strings.NewReader(`{"max_models": -1}`), vals); w.Code != http.StatusBadRequest {
		t.Errorf("a negative quota: %d %s", w.Code, w.Body)
	}
}

func TestAQuotaRefusalIsA403OrA429WithItsReason(t *testing.T) {
	h, store := newModelAPI()
	url := doubler(t)
	store.quotas = map[string]storage.MLQuotas{
		"tenant-q": {VHost: "tenant-q", MaxModels: new(int64(1)), MaxPredictionsPerSecond: new(1.0)},
	}

	if w := call(h.PutModel, admin, http.MethodPut, "tenant-q", "one", modelBody(url)); w.Code != http.StatusOK {
		t.Fatalf("the first model: %d %s", w.Code, w.Body)
	}
	w := call(h.PutModel, admin, http.MethodPut, "tenant-q", "two", modelBody(url))
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "max_models") {
		t.Errorf("a model over the quota: %d %s, want 403 naming the quota", w.Code, w.Body)
	}

	row := `{"instances":[{"x":1}]}`
	if w := call(h.Predict, admin, http.MethodPost, "tenant-q", "one", row); w.Code != http.StatusOK {
		t.Fatalf("the first prediction: %d %s", w.Code, w.Body)
	}
	w = call(h.Predict, admin, http.MethodPost, "tenant-q", "one", row)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" ||
		!strings.Contains(w.Body.String(), "max_predictions_per_second") {
		t.Errorf("a prediction over the rate: %d %s (Retry-After %q)", w.Code, w.Body, w.Header().Get("Retry-After"))
	}
}

func TestAnEditorTurnsMCPExposureOnAndAnEditKeepsIt(t *testing.T) {
	h, store := newModelAPI()
	url := doubler(t)
	if w := call(h.PutModel, editorA, http.MethodPut, "tenant-a", "double", modelBody(url)); w.Code != http.StatusOK {
		t.Fatal(w.Body)
	}
	if store.models["tenant-a/double"].MCPExposed {
		t.Fatal("a new model is exposed to MCP")
	}

	if w := call(h.SetMCPExposure, viewerA, http.MethodPut, "tenant-a", "double", `{"exposed":true}`); w.Code != http.StatusForbidden {
		t.Errorf("a viewer exposed a model: %d", w.Code)
	}
	if w := call(h.SetMCPExposure, editorB, http.MethodPut, "tenant-a", "double", `{"exposed":true}`); w.Code != http.StatusForbidden {
		t.Errorf("another vhost's editor exposed a model: %d", w.Code)
	}
	w := call(h.SetMCPExposure, editorA, http.MethodPut, "tenant-a", "double", `{"exposed":true}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"mcp_exposed":true`) {
		t.Fatalf("exposing: %d %s", w.Code, w.Body)
	}
	if !store.models["tenant-a/double"].MCPExposed || !hasAudit(store, `"mcp_exposed":"true"`) {
		t.Errorf("not stored or not audited: %+v", store.models["tenant-a/double"])
	}
	if w := call(h.SetMCPExposure, editorA, http.MethodPut, "tenant-a", "nope", `{"exposed":true}`); w.Code != http.StatusNotFound {
		t.Errorf("exposing a model that does not exist: %d", w.Code)
	}

	// The model form does not send the flag: saving it keeps what is set.
	if w := call(h.PutModel, editorA, http.MethodPut, "tenant-a", "double", modelBody(url)); w.Code != http.StatusOK {
		t.Fatal(w.Body)
	}
	if !store.models["tenant-a/double"].MCPExposed {
		t.Error("editing the model turned MCP exposure off")
	}
}
