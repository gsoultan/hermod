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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/ml/builtin"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// engineStatus is the worker status's report of each engine.
type engineStatus struct {
	Builtin struct {
		Available  bool     `json:"available"`
		Algorithms []string `json:"algorithms"`
	} `json:"builtin"`
	Worker struct {
		Configured  bool              `json:"configured"`
		Ready       bool              `json:"ready"`
		Source      string            `json:"source"`
		Error       string            `json:"error"`
		Algorithms  []string          `json:"algorithms"`
		Unavailable map[string]string `json:"unavailable"`
	} `json:"worker"`
}

func engines(t *testing.T, body []byte) engineStatus {
	t.Helper()
	var s struct {
		Engines engineStatus `json:"engines"`
	}
	if err := json.Unmarshal(body, &s); err != nil {
		t.Fatal(err)
	}
	return s.Engines
}

func TestWorkerStatusReportsBothEngines(t *testing.T) {
	h, _ := newModelAPI()
	e, err := builtin.New(builtin.Config{Dir: t.TempDir(), Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/health/ready":
			_, _ = w.Write([]byte(`{"ready":true}`))
		case "/v1/capabilities":
			_, _ = w.Write([]byte(`{"tasks":["auto"],"algorithms":["auto","xgboost"],"unavailable":{"keras_mlp":"TensorFlow is not installed"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	h.worker = e.Route(worker.New(srv.URL, "", srv.Client()))

	w := do(h.WorkerStatus, viewerA, http.MethodGet, "/api/ml/worker", nil, nil)
	var status struct {
		Ready       bool              `json:"ready"`
		Source      string            `json:"source"`
		Algorithms  []string          `json:"algorithms"`
		Unavailable map[string]string `json:"unavailable"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Ready || status.Source != "saved" || !slices.Contains(status.Algorithms, "mlp") || !slices.Contains(status.Algorithms, "xgboost") {
		t.Errorf("status = %+v; want both engines' algorithms", status)
	}
	if _, ok := status.Unavailable["xgboost"]; ok || status.Unavailable["keras_mlp"] == "" {
		t.Errorf("unavailable = %v", status.Unavailable)
	}
	got := engines(t, w.Body.Bytes())
	if !got.Builtin.Available || !got.Worker.Configured || !got.Worker.Ready || got.Worker.Source != "saved" ||
		!slices.Contains(got.Worker.Algorithms, "xgboost") || got.Worker.Unavailable["keras_mlp"] == "" {
		t.Errorf("engines = %+v", got)
	}

	// A worker that stops answering leaves the built-in engine ready.
	srv.Close()
	w = do(h.WorkerStatus, viewerA, http.MethodGet, "/api/ml/worker", nil, nil)
	got = engines(t, w.Body.Bytes())
	if !strings.Contains(w.Body.String(), `"ready":true`) || got.Worker.Ready || got.Worker.Error == "" || !got.Builtin.Available {
		t.Errorf("with the worker down: %s", w.Body)
	}
}

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
	if e := engines(t, w.Body.Bytes()); !e.Builtin.Available || e.Worker.Configured || !slices.Contains(e.Builtin.Algorithms, "mlp") {
		t.Errorf("engines = %+v", e)
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
	if !trained.Live || trained.Version.Version != "b1" || trained.Version.Metrics["r2"] < 0.999 {
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
