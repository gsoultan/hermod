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
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	mimirv1 "github.com/gsoultan/mimir-server/gen/mimir/v1"
	"github.com/gsoultan/mimir-server/gen/mimir/v1/mimirv1connect"
	"github.com/xitongsys/parquet-go-source/buffer"
	"github.com/xitongsys/parquet-go/writer"
)

// mimirModels is a mimir-server that lists two models to the holder of one key.
type mimirModels struct {
	mimirv1connect.UnimplementedModelServiceHandler
	key string
}

func (m *mimirModels) ListModels(_ context.Context, req *connect.Request[mimirv1.ListModelsRequest]) (*connect.Response[mimirv1.ListModelsResponse], error) {
	if req.Header().Get("Authorization") != "Bearer "+m.key {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("the API key is wrong or revoked"))
	}
	return connect.NewResponse(&mimirv1.ListModelsResponse{Models: []*mimirv1.Model{
		{Id: "m-1", Name: "churn", Status: mimirv1.ModelStatus_MODEL_STATUS_READY, Columns: []*mimirv1.Column{{Name: "plan", Kind: "category"}}},
		{Id: "m-2", Name: "fraud", Status: mimirv1.ModelStatus_MODEL_STATUS_FAILED, StatusDetail: "the file is not a model"},
	}}), nil
}

func serveMimir(t *testing.T, key string) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(mimirv1connect.NewModelServiceHandler(&mimirModels{key: key}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

type vhostSecrets map[string]string

func (s vhostSecrets) GetScoped(_ context.Context, vhost, key string) (string, error) {
	return s[vhost+"/"+key], nil
}

func TestListMimirModelsReadsTheKeyFromTheVHostsSecretsAndNeverReturnsIt(t *testing.T) {
	h, _ := newModelAPI()
	h.secrets = vhostSecrets{"tenant-a/MIMIR_API_KEY": "mimir_k"}
	url := serveMimir(t, "mimir_k")
	body := `{"url":"` + url + `","token_secret":"MIMIR_API_KEY"}`

	w := call(h.ListMimirModels, editorA, http.MethodPost, "tenant-a", "", body)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "mimir_k") {
		t.Fatal("the API key was returned to the browser")
	}
	var reply struct {
		Models []struct {
			ID      string                  `json:"id"`
			Name    string                  `json:"name"`
			Ready   bool                    `json:"ready"`
			Status  string                  `json:"status"`
			Columns []struct{ Name string } `json:"columns"`
		} `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if len(reply.Models) != 2 || reply.Models[0].ID != "m-1" || !reply.Models[0].Ready || reply.Models[0].Columns[0].Name != "plan" {
		t.Fatalf("models = %+v", reply.Models)
	}
	if reply.Models[1].Ready || reply.Models[1].Status != "the file is not a model" {
		t.Fatalf("the failed model = %+v, want not ready with the server's reason", reply.Models[1])
	}
}

func TestListMimirModelsSaysWhatIsWrong(t *testing.T) {
	h, _ := newModelAPI()
	h.secrets = vhostSecrets{"tenant-a/MIMIR_API_KEY": "wrong"}
	url := serveMimir(t, "mimir_k")

	cases := []struct {
		name, body string
		code       int
		want       string
	}{
		{"wrong key", `{"url":"` + url + `","token_secret":"MIMIR_API_KEY"}`, http.StatusBadRequest, "wrong or revoked"},
		{"missing secret", `{"url":"` + url + `","token_secret":"OTHER"}`, http.StatusBadRequest, `does not hold`},
		{"not a secret name", `{"url":"` + url + `","token_secret":"mimir_abc def"}`, http.StatusBadRequest, "not the name of a secret"},
		{"no url", `{"token_secret":"MIMIR_API_KEY"}`, http.StatusBadRequest, "address"},
		{"not json", `{`, http.StatusBadRequest, "body must be"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := call(h.ListMimirModels, editorA, http.MethodPost, "tenant-a", "", tc.body)
			if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("%d %s, want %d containing %q", w.Code, w.Body, tc.code, tc.want)
			}
		})
	}
	if w := call(h.ListMimirModels, viewerA, http.MethodPost, "tenant-a", "", `{}`); w.Code != http.StatusForbidden {
		t.Errorf("a viewer was allowed: %d", w.Code)
	}
	if w := call(h.ListMimirModels, editorB, http.MethodPost, "tenant-a", "", `{}`); w.Code != http.StatusForbidden {
		t.Errorf("another vhost's editor was allowed: %d", w.Code)
	}
}

// mimirPredictor is a mimir server that explains one model's answer.
type mimirPredictor struct {
	mimirv1connect.UnimplementedPredictServiceHandler
	key   string
	asked *mimirv1.ExplainRequest
}

func (m *mimirPredictor) Explain(_ context.Context, req *connect.Request[mimirv1.ExplainRequest]) (*connect.Response[mimirv1.ExplainResponse], error) {
	if req.Header().Get("Authorization") != "Bearer "+m.key {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("the API key is wrong or revoked"))
	}
	m.asked = req.Msg
	return connect.NewResponse(&mimirv1.ExplainResponse{
		Label: "left", Value: 0.8, Typical: 0.3, Exact: true,
		Contributions: []*mimirv1.Contribution{{Column: "plan", Value: "monthly", Typical: "yearly", Effect: 0.4}},
	}), nil
}

func TestExplainAsksTheMimirModelAboutOneRow(t *testing.T) {
	h, _ := newModelAPI()
	h.secrets = vhostSecrets{"tenant-a/MIMIR_API_KEY": "mimir_k"}
	server := &mimirPredictor{key: "mimir_k"}
	mux := http.NewServeMux()
	mux.Handle(mimirv1connect.NewPredictServiceHandler(server))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	def, _ := json.Marshal(map[string]any{"backend": "mimir", "url": srv.URL, "remote_model": "m-1", "token_secret": "MIMIR_API_KEY"})
	if w := call(h.PutModel, editorA, http.MethodPut, "tenant-a", "churn", string(def)); w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}

	w := call(h.Explain, editorA, http.MethodPost, "tenant-a", "churn", `{"instance":{"plan":"monthly","tenure":3},"label":"left"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("explain: %d %s", w.Code, w.Body)
	}
	if server.asked.GetModelId() != "m-1" || server.asked.GetFields()["tenure"] != "3" || server.asked.GetLabel() != "left" {
		t.Errorf("the server was asked %+v", server.asked)
	}
	var reply struct {
		Model       string `json:"model"`
		Explanation struct {
			Label         string  `json:"label"`
			Typical       float64 `json:"typical"`
			Contributions []struct {
				Column string  `json:"column"`
				Effect float64 `json:"effect"`
			} `json:"contributions"`
		} `json:"explanation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Model != "churn" || reply.Explanation.Label != "left" || reply.Explanation.Typical != 0.3 ||
		len(reply.Explanation.Contributions) != 1 || reply.Explanation.Contributions[0].Column != "plan" {
		t.Errorf("reply = %s", w.Body)
	}

	if w := call(h.Explain, viewerA, http.MethodPost, "tenant-a", "churn", `{"instance":{}}`); w.Code != http.StatusForbidden {
		t.Errorf("a viewer was allowed: %d", w.Code)
	}
	if w := call(h.Explain, editorA, http.MethodPost, "tenant-a", "churn", `{"label":"x"}`); w.Code != http.StatusBadRequest {
		t.Errorf("no instance: %d %s", w.Code, w.Body)
	}
	if w := call(h.Explain, editorA, http.MethodPost, "tenant-a", "nobody", `{"instance":{}}`); w.Code != http.StatusNotFound {
		t.Errorf("unknown model: %d %s", w.Code, w.Body)
	}
}

func TestExplainRefusesAModelThatIsNotOnAMimirServer(t *testing.T) {
	h, _ := newModelAPI()
	if w := call(h.PutModel, editorA, http.MethodPut, "tenant-a", "double", modelBody(doubler(t))); w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	w := call(h.Explain, editorA, http.MethodPost, "tenant-a", "double", `{"instance":{"x":1}}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "mimir") {
		t.Fatalf("%d %s, want 400 saying only a mimir model explains", w.Code, w.Body)
	}
}

// mimirDatasets is a mimir server that takes dataset uploads.
type mimirDatasets struct {
	mimirv1connect.UnimplementedDatasetServiceHandler
	key   string
	got   *mimirv1.CreateDatasetRequest
	auth  string
	count int
}

func (m *mimirDatasets) CreateDataset(_ context.Context, req *connect.Request[mimirv1.CreateDatasetRequest]) (*connect.Response[mimirv1.CreateDatasetResponse], error) {
	m.auth = req.Header().Get("Authorization")
	if m.auth != "Bearer "+m.key {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("the API key is wrong or revoked"))
	}
	m.got = req.Msg
	m.count++
	rows := int64(strings.Count(string(req.Msg.GetCsv()), "\n") - 1)
	return connect.NewResponse(&mimirv1.CreateDatasetResponse{
		Dataset: &mimirv1.Dataset{Id: "ds-9", Name: req.Msg.GetName(), Rows: rows, Target: "churned",
			Columns: []*mimirv1.Column{{Name: "age", Kind: "number"}, {Name: "plan", Kind: "category"}}},
		Check: &mimirv1.DataCheck{Verdict: "good"},
	}), nil
}

// customersParquet is a three-row table as the worker exports one.
func customersParquet(t *testing.T) []byte {
	t.Helper()
	type row struct {
		Age     float64 `parquet:"name=age, type=DOUBLE"`
		Plan    string  `parquet:"name=plan, type=BYTE_ARRAY, convertedtype=UTF8"`
		Churned bool    `parquet:"name=churned, type=BOOLEAN"`
	}
	fw := buffer.NewBufferFile()
	pw, err := writer.NewParquetWriter(fw, new(row), 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []row{{30, "monthly", true}, {41.5, "yearly", false}, {58, "say \"hi\", twice", false}} {
		if err := pw.Write(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := pw.WriteStop(); err != nil {
		t.Fatal(err)
	}
	return fw.Bytes()
}

func TestSendDatasetToMimirUploadsTheRowsAsCSVAndNeverReturnsTheKey(t *testing.T) {
	h, _, stub := newTrainingAPI(t)
	stub.exported = customersParquet(t)
	h.secrets = vhostSecrets{"tenant-a/MIMIR_API_KEY": "mimir_k"}
	server := &mimirDatasets{key: "mimir_k"}
	mux := http.NewServeMux()
	mux.Handle(mimirv1connect.NewDatasetServiceHandler(server))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	body := `{"url":"` + srv.URL + `","token_secret":"MIMIR_API_KEY","target":"churned"}`
	w := do(h.SendDatasetToMimir, editorA, http.MethodPost, "/api/vhosts/tenant-a/ml/datasets/customers/mimir",
		strings.NewReader(body), map[string]string{"vhost": "tenant-a", "name": "customers"})
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "mimir_k") {
		t.Fatal("the API key was returned to the browser")
	}
	if server.got.GetName() != "customers" || server.got.GetTarget() != "churned" || server.got.GetKind() != mimirv1.DatasetKind_DATASET_KIND_TABLE {
		t.Errorf("the server was asked for %+v", server.got)
	}
	want := "age,plan,churned\n30,monthly,true\n41.5,yearly,false\n58,\"say \"\"hi\"\", twice\",false\n"
	if got := string(server.got.GetCsv()); got != want {
		t.Errorf("csv sent:\n%s\nwant:\n%s", got, want)
	}
	var reply struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Rows    int64  `json:"rows"`
		Verdict string `json:"verdict"`
		Columns []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"columns"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.ID != "ds-9" || reply.Name != "customers" || reply.Rows != 3 || reply.Verdict != "good" || len(reply.Columns) != 2 {
		t.Errorf("reply = %s", w.Body)
	}

	// A name of its own on the server, and the same rows again: mimir has
	// no append, so each send is a dataset of its own.
	w = do(h.SendDatasetToMimir, editorA, http.MethodPost, "/api/vhosts/tenant-a/ml/datasets/customers/mimir",
		strings.NewReader(`{"url":"`+srv.URL+`","token_secret":"MIMIR_API_KEY","name":"customers-2026-10"}`),
		map[string]string{"vhost": "tenant-a", "name": "customers"})
	if w.Code != http.StatusOK || server.got.GetName() != "customers-2026-10" || server.count != 2 {
		t.Errorf("second send: %d %s, server asked for %q", w.Code, w.Body, server.got.GetName())
	}
}

func TestSendDatasetToMimirSaysWhatIsWrong(t *testing.T) {
	h, _, stub := newTrainingAPI(t)
	stub.exported = customersParquet(t)
	h.secrets = vhostSecrets{"tenant-a/MIMIR_API_KEY": "wrong"}
	mux := http.NewServeMux()
	mux.Handle(mimirv1connect.NewDatasetServiceHandler(&mimirDatasets{key: "mimir_k"}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cases := []struct {
		name, body string
		code       int
		want       string
	}{
		{"wrong key", `{"url":"` + srv.URL + `","token_secret":"MIMIR_API_KEY"}`, http.StatusBadRequest, "wrong or revoked"},
		{"missing secret", `{"url":"` + srv.URL + `","token_secret":"OTHER"}`, http.StatusBadRequest, "does not hold"},
		{"no url", `{"token_secret":"MIMIR_API_KEY"}`, http.StatusBadRequest, "address"},
		{"not json", `{`, http.StatusBadRequest, "body must be"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := do(h.SendDatasetToMimir, editorA, http.MethodPost, "/api/vhosts/tenant-a/ml/datasets/customers/mimir",
				strings.NewReader(tc.body), map[string]string{"vhost": "tenant-a", "name": "customers"})
			if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("%d %s, want %d containing %q", w.Code, w.Body, tc.code, tc.want)
			}
		})
	}
	if w := do(h.SendDatasetToMimir, viewerA, http.MethodPost, "/api/vhosts/tenant-a/ml/datasets/customers/mimir",
		strings.NewReader(`{}`), map[string]string{"vhost": "tenant-a", "name": "customers"}); w.Code != http.StatusForbidden {
		t.Errorf("a viewer was allowed: %d", w.Code)
	}
	// Without a worker there is nothing to read the rows from.
	h2, _ := newModelAPI()
	h2.secrets = vhostSecrets{"tenant-a/MIMIR_API_KEY": "mimir_k"}
	if w := do(h2.SendDatasetToMimir, editorA, http.MethodPost, "/api/vhosts/tenant-a/ml/datasets/customers/mimir",
		strings.NewReader(`{"url":"`+srv.URL+`","token_secret":"MIMIR_API_KEY"}`), map[string]string{"vhost": "tenant-a", "name": "customers"}); w.Code != http.StatusServiceUnavailable {
		t.Errorf("without a worker: %d %s", w.Code, w.Body)
	}
}
