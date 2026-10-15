// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package grpc

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/proto"
	"github.com/gsoultan/hermod/pkg/ml/recommend"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// asRecommender makes "a/double" version 1 of a worker model serving the
// item_similarity parity fixture of pkg/ml/recommend.
func asRecommender(t *testing.T) func(*ml.Service, *storage.MLModel) {
	t.Helper()
	return servingFixture(t, filepath.Join("item_similarity", "model.rec"))
}

// servingFixture makes "a/double" version 1 of a worker model serving the
// fixture at path under pkg/ml/recommend/testdata.
func servingFixture(t *testing.T, path string) func(*ml.Service, *storage.MLModel) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "pkg", "ml", "recommend", "testdata", path))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/versions/1/model.rec") {
			_, _ = w.Write(raw)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return func(svc *ml.Service, m *storage.MLModel) {
		svc.WithWorker(worker.New(srv.URL, "", nil))
		m.Backend, m.URL, m.RemoteVersion, m.Task = storage.MLBackendWorker, "", "1", storage.MLTaskRecommendation
	}
}

func TestRecommendAnswersWithItems(t *testing.T) {
	c, key := dial(t, asRecommender(t))
	no := false
	cases := []struct {
		name    string
		req     *proto.RecommendRequest
		n       int
		popular bool
	}{
		{"a person's items", &proto.RecommendRequest{Subject: &proto.RecommendRequest_User{User: "1000"}, K: 3}, 3, false},
		{"seen items too", &proto.RecommendRequest{Subject: &proto.RecommendRequest_User{User: "1000"}, K: 3, ExcludeSeen: &no}, 3, false},
		{"items like one", &proto.RecommendRequest{Subject: &proto.RecommendRequest_Item{Item: "sku-0"}, K: 2}, 2, false},
		{"the default k", &proto.RecommendRequest{Subject: &proto.RecommendRequest_User{User: "1000"}}, recommend.DefaultK, false},
		{"an unknown person", &proto.RecommendRequest{Subject: &proto.RecommendRequest_User{User: "nobody"}, K: 4}, 4, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.req.Vhost, tc.req.Model = "a", "double"
			resp, err := c.Recommend(withKey(key), tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if resp.GetModel() != "double" || len(resp.GetItems()) != tc.n {
				t.Fatalf("response = %v", resp)
			}
			if popular := resp.GetItems()[0].GetReason() == recommend.ReasonPopular; popular != tc.popular {
				t.Errorf("first item %v, want popular %v", resp.GetItems()[0], tc.popular)
			}
		})
	}
}

func TestRecommendRefusals(t *testing.T) {
	c, key := dial(t, asRecommender(t))
	yes := true
	cases := []struct {
		name string
		key  string
		req  *proto.RecommendRequest
		code codes.Code
	}{
		{"no key", "", &proto.RecommendRequest{Vhost: "a", Model: "double", Subject: &proto.RecommendRequest_User{User: "1"}}, codes.Unauthenticated},
		{"no subject", key, &proto.RecommendRequest{Vhost: "a", Model: "double"}, codes.InvalidArgument},
		{"a negative k", key, &proto.RecommendRequest{Vhost: "a", Model: "double", K: -1,
			Subject: &proto.RecommendRequest_User{User: "1"}}, codes.InvalidArgument},
		{"exclude_seen for an item", key, &proto.RecommendRequest{Vhost: "a", Model: "double", ExcludeSeen: &yes,
			Subject: &proto.RecommendRequest_Item{Item: "sku-0"}}, codes.InvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.Recommend(withKey(tc.key), tc.req)
			if status.Code(err) != tc.code {
				t.Errorf("err = %v, want %v", err, tc.code)
			}
		})
	}
	// Rows sent to a recommender are the caller's mistake, not the server's.
	_, err := c.Predict(withKey(key), &proto.PredictRequest{Vhost: "a", Model: "double", Instances: rows(t, 1)})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("rows to a recommender: %v", err)
	}
}

// A person sent to a model that predicts rows is refused as an argument.
func TestRecommendRefusesAModelThatPredictsRows(t *testing.T) {
	c, key := dial(t)
	_, err := c.Recommend(withKey(key), &proto.RecommendRequest{Vhost: "a", Model: "double",
		Subject: &proto.RecommendRequest_User{User: "1"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("err = %v", err)
	}
}
