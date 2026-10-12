package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/ml/builtin"
)

// TestTheBuiltinEngineServesAModelEndToEnd runs the whole path with no
// hermod-ml worker: upload a dataset, train, go live, make a serving key and
// call the model over its REST route.
func TestTheBuiltinEngineServesAModelEndToEnd(t *testing.T) {
	h, _ := newModelAPI()
	e, err := builtin.New(builtin.Config{Dir: t.TempDir(), Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	h.worker = e.Client()

	w := do(h.WorkerStatus, viewerA, http.MethodGet, "/api/ml/worker", nil, nil)
	var status struct {
		Configured  bool              `json:"configured"`
		Ready       bool              `json:"ready"`
		Source      string            `json:"source"`
		Algorithms  []string          `json:"algorithms"`
		Unavailable map[string]string `json:"unavailable"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Configured || !status.Ready || status.Source != "builtin" || status.Unavailable["xgboost"] != builtin.NeedsWorker {
		t.Errorf("worker status = %+v", status)
	}

	var csv strings.Builder
	csv.WriteString("rooms,area,city,price\n")
	for i := range 120 {
		rooms, area := 1+i%5, 40+(i*7)%90
		city := []string{"north", "south"}[i%2]
		price := 1000*rooms + 20*area
		if city == "south" {
			price += 500
		}
		fmt.Fprintf(&csv, "%d,%d,%s,%d\n", rooms, area, city, price)
	}
	w = do(h.UploadDataset, editorA, http.MethodPut, "/api/vhosts/tenant-a/ml/datasets/homes?format=csv",
		strings.NewReader(csv.String()), map[string]string{"vhost": "tenant-a", "name": "homes"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"rows":120`) {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}

	body := `{"dataset":"homes","target":"price","task":"regression","algorithm":"linear","go_live":{"mode":"always"}}`
	w = call(h.TrainModel, editorA, http.MethodPost, "tenant-a", "price", body)
	if w.Code != http.StatusOK {
		t.Fatalf("train: %d %s", w.Code, w.Body)
	}
	var trained struct {
		Live    bool `json:"live"`
		Version struct {
			Version string             `json:"version"`
			Metrics map[string]float64 `json:"metrics"`
		} `json:"version"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &trained); err != nil {
		t.Fatal(err)
	}
	if !trained.Live || trained.Version.Version != "1" || trained.Version.Metrics["r2"] < 0.999 {
		t.Fatalf("trained = %+v (%s)", trained, w.Body)
	}

	w = call(h.RotateServingKey, editorA, http.MethodPost, "tenant-a", "price", "")
	var made struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &made); err != nil || made.Key == "" {
		t.Fatalf("rotate: %d %s", w.Code, w.Body)
	}
	w = call(h.Serve, nil, http.MethodPost, "tenant-a", "price", `{"instances":[{"rooms":2,"area":50,"city":"south"}]}`,
		"X-API-Key", made.Key)
	if w.Code != http.StatusOK {
		t.Fatalf("serve: %d %s", w.Code, w.Body)
	}
	var served struct {
		Predictions []map[string]float64 `json:"predictions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &served); err != nil || len(served.Predictions) != 1 {
		t.Fatalf("serve: %s", w.Body)
	}
	if got := served.Predictions[0]["value"]; got < 3480 || got > 3520 {
		t.Errorf("predicted %v, want 3500", got)
	}

	trainAndServeARecommender(t, h)

	// A worker-only algorithm is 503: connecting a worker is the way.
	body = `{"dataset":"homes","target":"price","algorithm":"xgboost"}`
	if w = call(h.TrainModel, editorA, http.MethodPost, "tenant-a", "price", body); w.Code != http.StatusServiceUnavailable ||
		!strings.Contains(w.Body.String(), builtin.NeedsWorker) {
		t.Errorf("xgboost: %d %s", w.Code, w.Body)
	}
}

// trainAndServeARecommender trains an item_similarity model on the built-in
// engine and asks it for a person's items: people o0..o19 buy the s items,
// the rest the t items.
func trainAndServeARecommender(t *testing.T, h *Handler) {
	t.Helper()
	var csv strings.Builder
	csv.WriteString("customer,sku\n")
	for u := range 40 {
		group := "s"
		if u >= 20 {
			group = "t"
		}
		for n := range 4 {
			fmt.Fprintf(&csv, "o%d,%s%d\n", u, group, (u+n)%6)
		}
	}
	w := do(h.UploadDataset, editorA, http.MethodPut, "/api/vhosts/tenant-a/ml/datasets/orders?format=csv",
		strings.NewReader(csv.String()), map[string]string{"vhost": "tenant-a", "name": "orders"})
	if w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	body := `{"dataset":"orders","task":"recommendation","algorithm":"item_similarity",` +
		`"recommend":{"user":"customer","item":"sku"},"go_live":{"mode":"always"}}`
	if w = call(h.TrainModel, editorA, http.MethodPost, "tenant-a", "recs", body); w.Code != http.StatusOK {
		t.Fatalf("train recommender: %d %s", w.Code, w.Body)
	}
	w = call(h.Predict, editorA, http.MethodPost, "tenant-a", "recs", `{"user":"o3","k":2}`)
	if w.Code != http.StatusOK {
		t.Fatalf("recommend: %d %s", w.Code, w.Body)
	}
	items := recommended(t, w.Body.Bytes())
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	for _, it := range items {
		if !strings.HasPrefix(it.Item, "s") {
			t.Errorf("o3 (s taste) was recommended %+v", items)
		}
	}
}
