package grpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	_ "modernc.org/sqlite"

	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/storage"
	sqlstore "github.com/gsoultan/hermod/internal/storage/sql"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/gsoultan/hermod/pkg/ml/proto"
)

// dialJobs serves the InferenceService over a SQLite store holding models
// "double" and "other" of vhost "a", and returns a client, both models' keys
// and the service.
func dialJobs(t *testing.T) (proto.InferenceServiceClient, string, string, *ml.Service) {
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

	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "jobs.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := sqlstore.NewSQLStorage(db, "sqlite")
	if err := st.Init(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"double", "other"} {
		if err := st.(storage.MLModelStore).PutMLModel(t.Context(), storage.MLModel{
			VHost: "a", Name: name, Backend: inference.BackendMLflow, URL: modelSrv.URL}); err != nil {
			t.Fatal(err)
		}
	}
	svc := ml.NewService(func() any { return st }, nil, nil)
	key, err := svc.RotateServingKey(t.Context(), "a", "double")
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := svc.RotateServingKey(t.Context(), "a", "other")
	if err != nil {
		t.Fatal(err)
	}

	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	proto.RegisterInferenceServiceServer(srv, &Server{ServiceFunc: func() *ml.Service { return svc }, watchEvery: 10 * time.Millisecond})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return proto.NewInferenceServiceClient(conn), key, otherKey, svc
}

func submitPredict(t *testing.T, c proto.InferenceServiceClient, key string) *proto.Job {
	t.Helper()
	resp, err := c.SubmitJob(withKey(key), &proto.SubmitJobRequest{Call: &proto.SubmitJobRequest_Predict{
		Predict: &proto.PredictRequest{Vhost: "a", Model: "double", Instances: rows(t, 3, 10)}}})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	return resp.GetJob()
}

func TestSubmitJobThenGetJobAnswersWithThePredictions(t *testing.T) {
	c, key, _, svc := dialJobs(t)
	j := submitPredict(t, c, key)
	if j.GetStatus() != storage.MLJobQueued || j.GetKind() != storage.MLJobPredict || j.GetModel() != "double" || j.GetCreatedAt() == nil {
		t.Fatalf("submitted = %v", j)
	}
	ml.NewJobRunner(func() *ml.Service { return svc }, nil).Tick(t.Context())

	got, err := c.GetJob(withKey(key), &proto.GetJobRequest{Vhost: "a", JobId: j.GetId()})
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	preds := got.GetJob().GetPredictResult().GetPredictions()
	if got.GetJob().GetStatus() != storage.MLJobSucceeded || got.GetJob().GetProgress() != 1 || len(preds) != 2 ||
		preds[1].AsMap()["prediction"] != 20.0 || got.GetJob().GetFinishedAt() == nil {
		t.Errorf("job = %v", got.GetJob())
	}
}

func TestJobRPCsRefuseAnotherModelsKey(t *testing.T) {
	c, key, otherKey, _ := dialJobs(t)
	j := submitPredict(t, c, key)

	if _, err := c.GetJob(context.Background(), &proto.GetJobRequest{Vhost: "a", JobId: j.GetId()}); status.Code(err) != codes.Unauthenticated {
		t.Errorf("no key = %v", err)
	}
	for _, k := range []string{otherKey, "hml_wrong"} {
		if _, err := c.GetJob(withKey(k), &proto.GetJobRequest{Vhost: "a", JobId: j.GetId()}); status.Code(err) != codes.NotFound {
			t.Errorf("GetJob with key %.8s = %v, want NotFound", k, err)
		}
		if _, err := c.CancelJob(withKey(k), &proto.CancelJobRequest{Vhost: "a", JobId: j.GetId()}); status.Code(err) != codes.NotFound {
			t.Errorf("CancelJob with key %.8s = %v, want NotFound", k, err)
		}
	}
	if _, err := c.SubmitJob(withKey(otherKey), &proto.SubmitJobRequest{Call: &proto.SubmitJobRequest_Predict{
		Predict: &proto.PredictRequest{Vhost: "a", Model: "double", Instances: rows(t, 1)}}}); status.Code(err) != codes.Unauthenticated {
		t.Errorf("SubmitJob with another model's key = %v", err)
	}
	if _, err := c.SubmitJob(withKey(key), &proto.SubmitJobRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("SubmitJob without a call = %v", err)
	}
	if _, err := c.SubmitJob(withKey(key), &proto.SubmitJobRequest{Call: &proto.SubmitJobRequest_Predict{
		Predict: &proto.PredictRequest{Vhost: "a", Model: "double", Instances: rows(t, 1)}},
		CallbackUrl: "https://hooks.example.com/done"}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("a callback without the vhost's signing secret = %v", err)
	}
	if _, err := c.SubmitJob(withKey(key), &proto.SubmitJobRequest{Call: &proto.SubmitJobRequest_Predict{
		Predict: &proto.PredictRequest{Vhost: "a", Model: "double", Instances: rows(t, 1)}},
		CallbackUrl: "http://169.254.169.254/latest"}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("a callback to a link-local address = %v", err)
	}
}

func TestCancelJobAndWatchJobFollowTheJobToItsEnd(t *testing.T) {
	c, key, _, svc := dialJobs(t)
	cancelled := submitPredict(t, c, key)
	resp, err := c.CancelJob(withKey(key), &proto.CancelJobRequest{Vhost: "a", JobId: cancelled.GetId()})
	if err != nil || resp.GetJob().GetStatus() != storage.MLJobCancelled {
		t.Fatalf("CancelJob = %v, %v", resp, err)
	}

	j := submitPredict(t, c, key)
	stream, err := c.WatchJob(withKey(key), &proto.WatchJobRequest{Vhost: "a", JobId: j.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	first, err := stream.Recv()
	if err != nil || first.GetJob().GetStatus() != storage.MLJobQueued {
		t.Fatalf("first = %v, %v; want the job as it is now", first, err)
	}
	go ml.NewJobRunner(func() *ml.Service { return svc }, nil).Tick(context.Background())
	var last *proto.Job
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		last = msg.GetJob()
	}
	if last.GetStatus() != storage.MLJobSucceeded || len(last.GetPredictResult().GetPredictions()) != 2 {
		t.Errorf("the stream ended on %v; want the succeeded job with its result", last)
	}

	// A finished job is sent once and the stream ends.
	stream, err = c.WatchJob(withKey(key), &proto.WatchJobRequest{Vhost: "a", JobId: cancelled.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	if msg, err := stream.Recv(); err != nil || msg.GetJob().GetStatus() != storage.MLJobCancelled {
		t.Errorf("watching a cancelled job = %v, %v", msg, err)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Errorf("the stream went on after a finished job: %v", err)
	}
}
