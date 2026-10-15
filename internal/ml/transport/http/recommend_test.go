// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package http

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/recommend"
)

// recommenderAPI is the model API over a worker holding version 1 of the
// item_similarity parity fixture of pkg/ml/recommend as model "recs".
func recommenderAPI(t *testing.T) (*Handler, *modelAPIStore) {
	t.Helper()
	h, store, stub := newTrainingAPI(t)
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "pkg", "ml", "recommend", "testdata", "item_similarity", "model.rec"))
	if err != nil {
		t.Fatal(err)
	}
	stub.rec = raw
	store.models["tenant-a/recs"] = storage.MLModel{VHost: "tenant-a", Name: "recs", Backend: storage.MLBackendWorker,
		RemoteVersion: "1", Task: storage.MLTaskRecommendation}
	store.models["tenant-a/double"] = storage.MLModel{VHost: "tenant-a", Name: "double", Backend: "mlflow", URL: doubler(t)}
	return h, store
}

func recommended(t *testing.T, body []byte) []recommend.Item {
	t.Helper()
	var out struct {
		Items []recommend.Item `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	return out.Items
}

func TestPredictAnswersARecommendationModelWithItems(t *testing.T) {
	h, _ := recommenderAPI(t)
	cases := []struct {
		name, body string
		n          int
		popular    bool
	}{
		{"a person's items", `{"user":"1000","k":3}`, 3, false},
		{"a numeric id", `{"user":1000,"k":3,"exclude_seen":false}`, 3, false},
		{"items like one", `{"item":"sku-0","k":2}`, 2, false},
		{"the default k", `{"user":"1000"}`, recommend.DefaultK, false},
		{"an unknown person", `{"user":"nobody","k":4}`, 4, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := call(h.Predict, editorA, http.MethodPost, "tenant-a", "recs", tc.body)
			if w.Code != http.StatusOK {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			items := recommended(t, w.Body.Bytes())
			if len(items) != tc.n {
				t.Fatalf("%d items, want %d: %s", len(items), tc.n, w.Body)
			}
			if popular := items[0].Reason == recommend.ReasonPopular; popular != tc.popular {
				t.Errorf("first item %+v, want popular %v", items[0], tc.popular)
			}
		})
	}
}

func TestPredictRefusesTheWrongCallForTheModel(t *testing.T) {
	h, _ := recommenderAPI(t)
	cases := []struct {
		name, model, body, want string
	}{
		{"rows to a recommender", "recs", `{"instances":[{"user":"1000"}]}`, "recommendation model"},
		{"a person to a predictor", "double", `{"user":"1000"}`, "applies only to a recommendation model"},
		{"rows and a person", "recs", `{"user":"1000","instances":[{"x":1}]}`, "not both"},
		{"a person and an item", "recs", `{"user":"1000","item":"sku-0"}`, "not both"},
		{"a negative k", "recs", `{"user":"1000","k":-2}`, "at least 1"},
		{"an object id", "recs", `{"item":{"id":1}}`, "ids"},
		{"exclude_seen for an item", "recs", `{"item":"sku-0","exclude_seen":true}`, "exclude_seen"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := call(h.Predict, editorA, http.MethodPost, "tenant-a", tc.model, tc.body)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), tc.want) {
				t.Errorf("%d %s, want 400 mentioning %q", w.Code, w.Body, tc.want)
			}
		})
	}
	if w := call(h.Predict, viewerA, http.MethodPost, "tenant-a", "recs", `{"user":"1000"}`); w.Code != http.StatusForbidden {
		t.Errorf("a viewer called the recommender: %d", w.Code)
	}
}

// A recommendation model is trained from who/what columns, with no target.
func TestTrainARecommenderWithoutATarget(t *testing.T) {
	h, store, stub := newTrainingAPI(t)
	vals := map[string]string{"vhost": "tenant-a", "name": "recs"}
	body := `{"dataset":"orders","task":"recommendation","recommend":{"user":"customer","item":"product","time":"at"},
		"go_live":{"mode":"always"}}`
	w := do(h.TrainModel, editorA, http.MethodPost, "/x", strings.NewReader(body), vals)
	if w.Code != http.StatusOK {
		t.Fatalf("train = %d %s", w.Code, w.Body)
	}
	if !strings.Contains(stub.trained, `"recommend":{"user":"customer","item":"product","time":"at"}`) {
		t.Errorf("worker got %s", stub.trained)
	}
	if m := store.models["tenant-a/recs"]; !m.IsRecommender() || m.RemoteVersion != "1" {
		t.Errorf("stored model = %+v", m)
	}

	for name, body := range map[string]string{
		"no columns":              `{"dataset":"orders","task":"recommendation","go_live":{"mode":"never"}}`,
		"no target, no task":      `{"dataset":"orders","go_live":{"mode":"never"}}`,
		"columns for a predictor": `{"dataset":"orders","target":"churned","recommend":{"user":"u","item":"i"},"go_live":{"mode":"never"}}`,
	} {
		if w := do(h.TrainModel, editorA, http.MethodPost, "/x", strings.NewReader(body), vals); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
}

// An application calls a recommender with its serving key, as any model.
func TestServeAnswersARecommendationModelWithItsKey(t *testing.T) {
	h, _ := recommenderAPI(t)
	w := call(h.RotateServingKey, editorA, http.MethodPost, "tenant-a", "recs", "")
	var made struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &made); err != nil || made.Key == "" {
		t.Fatalf("rotate: %d %s", w.Code, w.Body)
	}
	w = call(h.Serve, nil, http.MethodPost, "tenant-a", "recs", `{"item":"sku-31","k":5}`, "X-API-Key", made.Key)
	if w.Code != http.StatusOK || len(recommended(t, w.Body.Bytes())) != 5 {
		t.Errorf("serve = %d %s", w.Code, w.Body)
	}
	if w := call(h.Serve, nil, http.MethodPost, "tenant-a", "recs", `{"user":"1000"}`, "X-API-Key", "hml_guess"); w.Code != http.StatusUnauthorized {
		t.Errorf("a wrong key: %d", w.Code)
	}
}
