package registry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"

	_ "github.com/gsoultan/hermod/pkg/comm/transformer/ml"
)

// The Predict node end to end: a model registered in the vhost's registry is
// called through the engine's transformation path, and its answer lands on the
// message.
func TestMLPredictCallsTheRegisteredModel(t *testing.T) {
	reg := newSimRegistry(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Records []map[string]float64 `json:"dataframe_records"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"predictions": []float64{body.Records[0]["amount"] / 1000}})
	}))
	t.Cleanup(srv.Close)

	models, ok := reg.store().(storage.MLModelStore)
	if !ok {
		t.Fatal("the registry's store holds no models")
	}
	if err := models.PutMLModel(t.Context(), storage.MLModel{
		VHost: "default", Name: "fraud", Backend: inference.BackendMLflow, URL: srv.URL,
	}); err != nil {
		t.Fatalf("PutMLModel: %v", err)
	}

	got := transform(t, reg,
		map[string]any{"total": 250.0, "customer": "C-1"},
		map[string]any{
			"transType":   "ml_predict",
			"model":       "fraud",
			"inputs":      `{"amount":"total"}`,
			"outputField": "fraud_score",
		})

	if got["fraud_score"] != 0.25 {
		t.Errorf("fraud_score = %v, want 0.25 from the model", got["fraud_score"])
	}
	if got["customer"] != "C-1" {
		t.Errorf("customer = %v, the record lost a field", got["customer"])
	}
}
