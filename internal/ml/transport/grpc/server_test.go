package grpc

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/proto"
)

type store struct {
	storage.Storage
	models map[string]storage.MLModel
}

func (s *store) ListMLModels(context.Context, string) ([]storage.MLModel, error) { return nil, nil }
func (s *store) GetMLModel(_ context.Context, vhost, name string) (storage.MLModel, error) {
	m, ok := s.models[vhost+"/"+name]
	if !ok {
		return storage.MLModel{}, storage.ErrNotFound
	}
	return m, nil
}
func (s *store) PutMLModel(context.Context, storage.MLModel) error { return nil }
func (s *store) SetMLModelServingKey(_ context.Context, vhost, name, hash string) error {
	m := s.models[vhost+"/"+name]
	m.ServingKeyHash = hash
	s.models[vhost+"/"+name] = m
	return nil
}
func (s *store) DeleteMLModel(context.Context, string, string) error { return nil }
func (s *store) DeleteMLModels(context.Context, string) error        { return nil }

// dial serves the InferenceService in memory and returns a client for it, with
// a model "double" in vhost "a" whose serving key is returned too.
func dial(t *testing.T) (proto.InferenceServiceClient, string) {
	t.Helper()
	modelSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Records []map[string]float64 `json:"dataframe_records"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		preds := make([]float64, len(body.Records))
		for i, rec := range body.Records {
			preds[i] = rec["x"] * 2
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"predictions": preds})
	}))
	t.Cleanup(modelSrv.Close)

	st := &store{models: map[string]storage.MLModel{
		"a/double": {VHost: "a", Name: "double", Backend: inference.BackendMLflow, URL: modelSrv.URL},
		"a/off":    {VHost: "a", Name: "off", Backend: inference.BackendMLflow, URL: modelSrv.URL},
	}}
	svc := ml.NewService(func() any { return st }, nil, nil)
	key, err := svc.RotateServingKey(context.Background(), "a", "double")
	if err != nil {
		t.Fatal(err)
	}

	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	proto.RegisterInferenceServiceServer(srv, &Server{ServiceFunc: func() *ml.Service { return svc }})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return proto.NewInferenceServiceClient(conn), key
}

func rows(t *testing.T, xs ...float64) []*structpb.Struct {
	t.Helper()
	out := make([]*structpb.Struct, len(xs))
	for i, x := range xs {
		s, err := structpb.NewStruct(map[string]any{"x": x})
		if err != nil {
			t.Fatal(err)
		}
		out[i] = s
	}
	return out
}

func withKey(key string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), "x-api-key", key)
}

func TestPredictAnswersWithTheModelsPredictions(t *testing.T) {
	c, key := dial(t)
	resp, err := c.Predict(withKey(key), &proto.PredictRequest{Vhost: "a", Model: "double", Instances: rows(t, 3, 10)})
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if resp.GetModel() != "double" || len(resp.GetPredictions()) != 2 {
		t.Fatalf("resp = %v", resp)
	}
	if got := resp.GetPredictions()[1].AsMap()["prediction"]; got != 20.0 {
		t.Errorf("second prediction = %v, want 20", got)
	}
}

func TestPredictRefusesWithoutTheModelsKey(t *testing.T) {
	c, key := dial(t)
	cases := []struct {
		name string
		ctx  context.Context
		req  *proto.PredictRequest
	}{
		{"no key", context.Background(), &proto.PredictRequest{Vhost: "a", Model: "double", Instances: rows(t, 1)}},
		{"wrong key", withKey(key + "x"), &proto.PredictRequest{Vhost: "a", Model: "double", Instances: rows(t, 1)}},
		{"serving off", withKey(key), &proto.PredictRequest{Vhost: "a", Model: "off", Instances: rows(t, 1)}},
		{"no such model", withKey(key), &proto.PredictRequest{Vhost: "a", Model: "nope", Instances: rows(t, 1)}},
		{"another vhost", withKey(key), &proto.PredictRequest{Vhost: "b", Model: "double", Instances: rows(t, 1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.Predict(tc.ctx, tc.req)
			if status.Code(err) != codes.Unauthenticated {
				t.Fatalf("err = %v, want Unauthenticated", err)
			}
		})
	}
}

func TestPredictRefusesNoRowsAndTooMany(t *testing.T) {
	c, key := dial(t)
	if _, err := c.Predict(withKey(key), &proto.PredictRequest{Vhost: "a", Model: "double"}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("no rows: err = %v, want InvalidArgument", err)
	}
	many := make([]float64, ml.MaxRowsPerCall+1)
	if _, err := c.Predict(withKey(key), &proto.PredictRequest{Vhost: "a", Model: "double", Instances: rows(t, many...)}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("too many rows: err = %v, want InvalidArgument", err)
	}
}
