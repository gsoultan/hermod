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
