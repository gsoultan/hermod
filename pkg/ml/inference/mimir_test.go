// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package inference

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	mimirsdk "github.com/gsoultan/mimir-server-sdk"
	mimirv1 "github.com/gsoultan/mimir-server/gen/mimir/v1"
	"github.com/gsoultan/mimir-server/gen/mimir/v1/mimirv1connect"
)

// fakeMimir is a mimir-server that answers from a table of rows by one
// column, and records what it was asked.
type fakeMimir struct {
	mimirv1connect.UnimplementedPredictServiceHandler
	mimirv1connect.UnimplementedModelServiceHandler

	mu       sync.Mutex
	auth     []string
	modelIDs []string
	fields   []map[string]string
	answer   func(fields map[string]string) *mimirv1.PredictResponse
	models   []*mimirv1.Model
	fail     error
}

func (f *fakeMimir) Predict(_ context.Context, req *connect.Request[mimirv1.PredictRequest]) (*connect.Response[mimirv1.PredictResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = append(f.auth, req.Header().Get("Authorization"))
	f.modelIDs = append(f.modelIDs, req.Msg.GetModelId())
	f.fields = append(f.fields, req.Msg.GetFields())
	if f.fail != nil {
		return nil, f.fail
	}
	return connect.NewResponse(f.answer(req.Msg.GetFields())), nil
}

func (f *fakeMimir) ListModels(_ context.Context, req *connect.Request[mimirv1.ListModelsRequest]) (*connect.Response[mimirv1.ListModelsResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = append(f.auth, req.Header().Get("Authorization"))
	if f.fail != nil {
		return nil, f.fail
	}
	return connect.NewResponse(&mimirv1.ListModelsResponse{Models: f.models}), nil
}

func (f *fakeMimir) serve(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(mimirv1connect.NewPredictServiceHandler(f))
	mux.Handle(mimirv1connect.NewModelServiceHandler(f))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func labelAnswer(fields map[string]string) *mimirv1.PredictResponse {
	if fields["plan"] == "monthly" {
		return &mimirv1.PredictResponse{
			PredictionId: "p-" + fields["tenure"],
			Guesses:      []*mimirv1.Guess{{Label: "left", Probability: 0.8}, {Label: "stayed", Probability: 0.2}},
		}
	}
	return &mimirv1.PredictResponse{
		PredictionId: "p-" + fields["tenure"],
		Guesses:      []*mimirv1.Guess{{Label: "stayed", Probability: 0.9}, {Label: "left", Probability: 0.1}},
	}
}

func TestMimirAsksOneRowAtATimeByColumnNameAndKeepsTheOrder(t *testing.T) {
	f := &fakeMimir{answer: labelAnswer}
	url := f.serve(t)
	target := Target{Backend: BackendMimir, URL: url, Model: "m-1", Token: "mimir_k", Features: []string{"plan", "tenure"}}

	rows := []Row{
		{"plan": "monthly", "tenure": 3, "ignored": "x"},
		{"plan": "yearly", "tenure": 12.5, "ignored": "y"},
		{"plan": "monthly", "tenure": true},
	}
	out, err := NewClient(nil).Predict(context.Background(), target, rows)
	if err != nil {
		t.Fatal(err)
	}
	want := []Row{
		{"prediction": "left", "confidence": float64(float32(0.8)), "prediction_id": "p-3",
			"guesses": []map[string]any{{"label": "left", "probability": float64(float32(0.8))}, {"label": "stayed", "probability": float64(float32(0.2))}}},
		{"prediction": "stayed", "confidence": float64(float32(0.9)), "prediction_id": "p-12.5",
			"guesses": []map[string]any{{"label": "stayed", "probability": float64(float32(0.9))}, {"label": "left", "probability": float64(float32(0.1))}}},
		{"prediction": "left", "confidence": float64(float32(0.8)), "prediction_id": "p-true",
			"guesses": []map[string]any{{"label": "left", "probability": float64(float32(0.8))}, {"label": "stayed", "probability": float64(float32(0.2))}}},
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("predictions = %#v\nwant %#v", out, want)
	}
	for _, a := range f.auth {
		if a != "Bearer mimir_k" {
			t.Errorf("Authorization = %q, want the API key as a bearer token", a)
		}
	}
	for _, id := range f.modelIDs {
		if id != "m-1" {
			t.Errorf("model id = %q, want m-1", id)
		}
	}
	for _, sent := range f.fields {
		if _, leaked := sent["ignored"]; leaked {
			t.Errorf("a field outside Features was sent: %v", sent)
		}
		if len(sent) != 2 {
			t.Errorf("sent %v, want plan and tenure only", sent)
		}
	}
}

func TestMimirSendsEveryFieldWhenNoFeaturesAreDeclaredAndReadsANumber(t *testing.T) {
	f := &fakeMimir{answer: func(fields map[string]string) *mimirv1.PredictResponse {
		return &mimirv1.PredictResponse{Number: true, Value: 41.5, PredictionId: "n-1"}
	}}
	target := Target{Backend: BackendMimir, URL: f.serve(t), Model: "m-2", Token: "k"}
	out, err := NewClient(nil).Predict(context.Background(), target, []Row{{"a": nil, "b": "x", "c": 2}})
	if err != nil {
		t.Fatal(err)
	}
	want := []Row{{"prediction": 41.5, "prediction_id": "n-1"}}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("predictions = %#v, want %#v", out, want)
	}
	if got := f.fields[0]; !reflect.DeepEqual(got, map[string]string{"a": "", "b": "x", "c": "2"}) {
		t.Fatalf("sent %v; a nil must read as empty and a number as its text", got)
	}
}

func TestMimirReportsTheServersRefusalInItsOwnWords(t *testing.T) {
	f := &fakeMimir{fail: connect.NewError(connect.CodeUnauthenticated, errors.New("the API key is wrong or revoked"))}
	target := Target{Backend: BackendMimir, URL: f.serve(t), Model: "m", Token: "bad"}
	_, err := NewClient(nil).Predict(context.Background(), target, []Row{{"a": 1}})
	if err == nil || !strings.Contains(err.Error(), "wrong or revoked") {
		t.Fatalf("err = %v, want the server's message", err)
	}
	if !errors.Is(err, mimirsdk.ErrUnauthenticated) {
		t.Fatalf("err = %v, want it to match mimirsdk.ErrUnauthenticated", err)
	}
}

func TestMimirTargetNeedsAModelIDAndAKeyAndTakesNoInputName(t *testing.T) {
	cases := []struct {
		name string
		t    Target
		ok   bool
	}{
		{"ok", Target{Backend: BackendMimir, URL: "http://m:8080", Model: "abc", Token: "k"}, true},
		{"no model", Target{Backend: BackendMimir, URL: "http://m:8080", Token: "k"}, false},
		{"model with a slash", Target{Backend: BackendMimir, URL: "http://m:8080", Model: "a/b", Token: "k"}, false},
		{"input name", Target{Backend: BackendMimir, URL: "http://m:8080", Model: "abc", Token: "k", InputName: "x", Features: []string{"a"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.t.Validate(); (err == nil) != tc.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestListMimirModelsReadsWhatTheServerOffers(t *testing.T) {
	f := &fakeMimir{models: []*mimirv1.Model{
		{Id: "m-1", Name: "churn", Architecture: "table", Status: mimirv1.ModelStatus_MODEL_STATUS_READY, Accuracy: 0.94,
			Description: "who leaves", Labels: []string{"stayed", "left"},
			Columns: []*mimirv1.Column{{Name: "plan", Kind: "category"}, {Name: "tenure", Kind: "number"}}},
		{Id: "m-2", Name: "broken", Architecture: "mim", Status: mimirv1.ModelStatus_MODEL_STATUS_FAILED, StatusDetail: "the file is not a model"},
	}}
	got, err := NewClient(nil).ListMimirModels(context.Background(), f.serve(t), "mimir_k")
	if err != nil {
		t.Fatal(err)
	}
	want := []RemoteModel{
		{ID: "m-1", Name: "churn", Architecture: "table", Description: "who leaves", Ready: true, Accuracy: 0.94,
			Labels: []string{"stayed", "left"}, Columns: []RemoteColumn{{Name: "plan", Kind: "category"}, {Name: "tenure", Kind: "number"}}},
		{ID: "m-2", Name: "broken", Architecture: "mim", Status: "the file is not a model"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("models = %#v\nwant %#v", got, want)
	}
	if f.auth[0] != "Bearer mimir_k" {
		t.Fatalf("Authorization = %q", f.auth[0])
	}
}

func TestListMimirModelsRefusesABadAddress(t *testing.T) {
	if _, err := NewClient(nil).ListMimirModels(context.Background(), "ftp://m", "k"); err == nil {
		t.Fatal("an ftp address was accepted")
	}
	if _, err := NewClient(nil).ListMimirModels(context.Background(), "http://m:8080", ""); err == nil {
		t.Fatal("a call without a key was attempted")
	}
}
