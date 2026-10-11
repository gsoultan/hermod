package worker

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeWorker records what the client sent and answers with reply.
type fakeWorker struct {
	method, path, query, auth, contentType string
	body                                   []byte
	status                                 int
	reply                                  string
}

func (f *fakeWorker) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.method, f.path, f.query = r.Method, r.URL.EscapedPath(), r.URL.RawQuery
		f.auth, f.contentType = r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		f.body, _ = io.ReadAll(r.Body)
		status := f.status
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, f.reply)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFromEnvIsOffWithoutAURL(t *testing.T) {
	t.Setenv("HERMOD_ML_WORKER_URL", "")
	if c := FromEnv(); c != nil {
		t.Fatalf("FromEnv() = %+v, want nil when no worker is configured", c)
	}
	t.Setenv("HERMOD_ML_WORKER_URL", "http://ml:8090/")
	t.Setenv("HERMOD_ML_WORKER_TOKEN", "tok")
	c := FromEnv()
	if c == nil || c.URL != "http://ml:8090" || c.Token != "tok" {
		t.Fatalf("FromEnv() = %+v, want the URL without its trailing slash and the token", c)
	}
}

func TestAppendRowsSendsRowsAndReplace(t *testing.T) {
	f := &fakeWorker{reply: `{"name":"orders","rows":12}`}
	c := New(f.server(t).URL, "secret", nil)

	n, err := c.AppendRows(t.Context(), "tenant-a", "orders", []map[string]any{{"a": 1.0}}, true)
	if err != nil {
		t.Fatalf("AppendRows: %v", err)
	}
	if n != 12 {
		t.Errorf("rows = %d, want the worker's total 12", n)
	}
	if f.method != http.MethodPost || f.path != "/v1/datasets/tenant-a/orders/rows" {
		t.Errorf("called %s %s", f.method, f.path)
	}
	if f.auth != "Bearer secret" {
		t.Errorf("Authorization = %q", f.auth)
	}
	var sent struct {
		Rows    []map[string]any `json:"rows"`
		Replace bool             `json:"replace"`
	}
	if err := json.Unmarshal(f.body, &sent); err != nil || len(sent.Rows) != 1 || !sent.Replace {
		t.Errorf("body = %s (%v)", f.body, err)
	}
}

func TestUploadFileStreamsTheBodyWithItsFormat(t *testing.T) {
	f := &fakeWorker{reply: `{"name":"sales","rows":3,"columns":[{"name":"x","type":"number"}],"updated_at":"2026-10-10T00:00:00Z"}`}
	c := New(f.server(t).URL, "", nil)

	info, err := c.UploadFile(t.Context(), "v", "sales", "xlsx", strings.NewReader("PK..."))
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if f.method != http.MethodPut || f.path != "/v1/datasets/v/sales/file" || f.query != "format=xlsx" {
		t.Errorf("called %s %s?%s", f.method, f.path, f.query)
	}
	if string(f.body) != "PK..." || f.auth != "" {
		t.Errorf("body %q auth %q", f.body, f.auth)
	}
	if info.Rows != 3 || len(info.Columns) != 1 || info.Columns[0].Type != "number" {
		t.Errorf("info = %+v", info)
	}
	if _, err := c.UploadFile(t.Context(), "v", "sales", "xls", strings.NewReader("")); err == nil {
		t.Error("an .xls upload was sent; only csv and xlsx are read")
	}
}

func TestTrainSendsTheSpecAndReadsTheVersion(t *testing.T) {
	f := &fakeWorker{reply: `{"model":"churn","version":"3","task":"classification","algorithm":"random_forest",
		"dataset":"customers","target":"churned","features":["age","plan"],"feature_types":{"age":"number","plan":"string"},
		"labels":["no","yes"],"metrics":{"accuracy":0.9,"score":0.9},"rows":{"train":80,"test":20},"created_at":"2026-10-10T00:00:00Z"}`}
	c := New(f.server(t).URL, "", nil)

	v, err := c.Train(t.Context(), "v", "churn", TrainSpec{Dataset: "customers", Target: "churned", Task: "auto", Algorithm: "auto"})
	if err != nil {
		t.Fatalf("Train: %v", err)
	}
	if f.path != "/v1/models/v/churn/train" {
		t.Errorf("path = %s", f.path)
	}
	if !strings.Contains(string(f.body), `"dataset":"customers"`) || !strings.Contains(string(f.body), `"target":"churned"`) {
		t.Errorf("spec not sent: %s", f.body)
	}
	if v.Version != "3" || v.Metrics["score"] != 0.9 || v.Rows.Train != 80 || len(v.Features) != 2 || v.FeatureTypes["plan"] != "string" {
		t.Errorf("version = %+v", v)
	}
}

func TestWorkerErrorsKeepTheirMessageAndNotFoundIsRecognisable(t *testing.T) {
	f := &fakeWorker{status: http.StatusNotFound, reply: `{"error":"no dataset named nope"}`}
	c := New(f.server(t).URL, "", nil)
	_, err := c.Dataset(t.Context(), "v", "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if err == nil || !strings.Contains(err.Error(), "no dataset named nope") {
		t.Errorf("err = %v, want the worker's message", err)
	}

	f.status, f.reply = http.StatusTooManyRequests, `{"error":"a training is already running"}`
	if _, err := c.Train(t.Context(), "v", "m", TrainSpec{Dataset: "d", Target: "t"}); !errors.Is(err, ErrBusy) {
		t.Errorf("err = %v, want ErrBusy", err)
	}
}

func TestNamesAreRefusedBeforeAnythingIsSent(t *testing.T) {
	f := &fakeWorker{reply: `{}`}
	c := New(f.server(t).URL, "", nil)
	for _, bad := range []string{"", "..", "a/b", "a b", "../etc"} {
		if _, err := c.Dataset(t.Context(), bad, "d"); err == nil {
			t.Errorf("vhost %q was sent", bad)
		}
		if _, err := c.Versions(t.Context(), "v", bad); err == nil {
			t.Errorf("model %q was sent", bad)
		}
	}
	if f.path != "" {
		t.Errorf("the worker was called at %s", f.path)
	}
}

func TestDatasetsVersionsAndDeletes(t *testing.T) {
	f := &fakeWorker{reply: `{"datasets":[{"name":"a","rows":1,"columns":[],"updated_at":"2026-10-10T00:00:00Z"}]}`}
	c := New(f.server(t).URL, "", nil)
	list, err := c.Datasets(t.Context(), "v")
	if err != nil || len(list) != 1 || list[0].Name != "a" || f.path != "/v1/datasets/v" {
		t.Errorf("Datasets = %+v, %v at %s", list, err, f.path)
	}

	f.reply = `{"versions":[{"model":"m","version":"2"},{"model":"m","version":"1"}]}`
	vs, err := c.Versions(t.Context(), "v", "m")
	if err != nil || len(vs) != 2 || vs[0].Version != "2" || f.path != "/v1/models/v/m/versions" {
		t.Errorf("Versions = %+v, %v at %s", vs, err, f.path)
	}

	f.status, f.reply = http.StatusNoContent, ""
	if err := c.DeleteDataset(t.Context(), "v", "a"); err != nil || f.method != http.MethodDelete || f.path != "/v1/datasets/v/a" {
		t.Errorf("DeleteDataset: %v, %s %s", err, f.method, f.path)
	}
	if err := c.DeleteModel(t.Context(), "v", "m"); err != nil || f.path != "/v1/models/v/m" {
		t.Errorf("DeleteModel: %v at %s", err, f.path)
	}
}

func TestServingURLIsPerVHost(t *testing.T) {
	c := New("http://ml:8090", "", nil)
	if got := c.ServingURL("tenant-a"); got != "http://ml:8090/vhosts/tenant-a" {
		t.Errorf("ServingURL = %q", got)
	}
}

func TestReadyAsksTheHealthRoute(t *testing.T) {
	f := &fakeWorker{reply: `{}`}
	c := New(f.server(t).URL, "", nil)
	if err := c.Ready(t.Context()); err != nil || f.path != "/v2/health/ready" {
		t.Errorf("Ready: %v at %s", err, f.path)
	}
	f.status = http.StatusServiceUnavailable
	if err := c.Ready(t.Context()); err == nil {
		t.Error("a 503 read as ready")
	}
}

func TestModelFileReadsTheVersionsGraph(t *testing.T) {
	f := &fakeWorker{reply: "\x08\x0a\x12\x00onnx bytes"}
	c := New(f.server(t).URL, "secret", nil)

	raw, err := c.ModelFile(t.Context(), "tenant-a", "churn", "3")
	if err != nil {
		t.Fatalf("ModelFile: %v", err)
	}
	if string(raw) != f.reply {
		t.Errorf("ModelFile = %q, want the bytes the worker sent", raw)
	}
	if f.method != http.MethodGet || f.path != "/v1/models/tenant-a/churn/versions/3/model.onnx" || f.auth != "Bearer secret" {
		t.Errorf("called %s %s with %q", f.method, f.path, f.auth)
	}

	f.status, f.reply = http.StatusNotFound, `{"error":"Model 'churn' has no version '9'."}`
	if _, err := c.ModelFile(t.Context(), "tenant-a", "churn", "9"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ModelFile of a missing version = %v, want ErrNotFound", err)
	}
	for _, bad := range []string{"", "..", "1/2"} {
		if _, err := c.ModelFile(t.Context(), "tenant-a", "churn", bad); err == nil {
			t.Errorf("ModelFile accepted version %q", bad)
		}
	}
}

func TestVersionCarriesWhatScoringNeeds(t *testing.T) {
	f := &fakeWorker{reply: `{"versions":[{"model":"m","version":"2","task":"classification",
		"features":["x","city"],"feature_types":{"x":"number","city":"string"},"fill":{"x":0.5},"labels":["no","yes"]}]}`}
	vs, err := New(f.server(t).URL, "", nil).Versions(t.Context(), "v", "m")
	if err != nil || len(vs) != 1 {
		t.Fatalf("Versions = %+v, %v", vs, err)
	}
	if vs[0].Fill["x"] != 0.5 || vs[0].FeatureTypes["city"] != "string" {
		t.Errorf("version = %+v, want its fill values and feature types", vs[0])
	}
}

// A version carries the statistics of the rows it was trained on, which is
// what Hermod measures drift against.
func TestAVersionCarriesItsTrainingStats(t *testing.T) {
	f := &fakeWorker{reply: `{"versions":[{"model":"churn","version":"2","features":["age","plan"],"metrics":{},
		"feature_stats":{
			"age":{"kind":"numeric","count":80,"null_fraction":0.05,"mean":41.5,"std":9.2,"min":18,"max":90,
				"edges":[30,40,50],"fractions":[0.2,0.3,0.3,0.2]},
			"plan":{"kind":"categorical","count":80,"null_fraction":0,"top":[{"value":"pro","fraction":0.6},{"value":"free","fraction":0.3}],
				"other_fraction":0.1}}}]}`}
	c := New(f.server(t).URL, "", nil)

	vs, err := c.Versions(t.Context(), "v", "churn")
	if err != nil || len(vs) != 1 {
		t.Fatalf("Versions = %+v, %v", vs, err)
	}
	age, plan := vs[0].FeatureStats["age"], vs[0].FeatureStats["plan"]
	if age.Kind != StatsNumeric || age.NullFraction != 0.05 || len(age.Edges) != 3 || len(age.Fractions) != 4 ||
		age.Mean == nil || *age.Mean != 41.5 || age.Std == nil || *age.Std != 9.2 {
		t.Errorf("age = %+v", age)
	}
	if plan.Kind != StatsCategorical || len(plan.Top) != 2 || plan.Top[0].Value != "pro" || plan.Top[0].Fraction != 0.6 ||
		plan.OtherFraction != 0.1 {
		t.Errorf("plan = %+v", plan)
	}
}

func TestTrainSendsDeepLearningParamsOnlyWhenSet(t *testing.T) {
	f := &fakeWorker{reply: `{"model":"m","version":"1"}`}
	c := New(f.server(t).URL, "", nil)

	if _, err := c.Train(t.Context(), "v", "m", TrainSpec{Dataset: "d", Target: "t", Algorithm: "random_forest"}); err != nil {
		t.Fatalf("Train: %v", err)
	}
	if strings.Contains(string(f.body), `"params"`) {
		t.Errorf("params sent for a spec without them: %s", f.body)
	}

	spec := TrainSpec{Dataset: "d", Target: "t", Algorithm: "pytorch_mlp", Params: &TrainParams{
		HiddenLayers: []int{16, 8}, Epochs: 50, BatchSize: 64, LearningRate: 0.01, Patience: 5,
	}}
	if _, err := c.Train(t.Context(), "v", "m", spec); err != nil {
		t.Fatalf("Train: %v", err)
	}
	if !strings.Contains(string(f.body), `"params":{"hidden_layers":[16,8],"epochs":50,"batch_size":64,"learning_rate":0.01,"patience":5}`) {
		t.Errorf("body = %s, want every param", f.body)
	}

	// Unset fields are left for the worker's defaults.
	spec.Params = &TrainParams{Epochs: 10}
	if _, err := c.Train(t.Context(), "v", "m", spec); err != nil {
		t.Fatalf("Train: %v", err)
	}
	if !strings.Contains(string(f.body), `"params":{"epochs":10}`) {
		t.Errorf("body = %s, want only epochs in params", f.body)
	}
}

func TestCapabilitiesSaysWhatTheWorkerCanTrain(t *testing.T) {
	f := &fakeWorker{reply: `{"tasks":["auto","classification","regression"],
		"algorithms":["auto","random_forest","gradient_boosting","linear","xgboost"],
		"unavailable":{"pytorch_mlp":"the torch package is not installed","keras_mlp":"the tensorflow package is not installed"}}`}
	c := New(f.server(t).URL, "secret", nil)

	caps, err := c.Capabilities(t.Context())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if f.method != http.MethodGet || f.path != "/v1/capabilities" || f.auth != "Bearer secret" {
		t.Errorf("called %s %s with %q", f.method, f.path, f.auth)
	}
	if len(caps.Algorithms) != 5 || caps.Algorithms[4] != "xgboost" || len(caps.Tasks) != 3 {
		t.Errorf("capabilities = %+v", caps)
	}
	if caps.Unavailable["pytorch_mlp"] != "the torch package is not installed" {
		t.Errorf("unavailable = %v", caps.Unavailable)
	}

	// A worker from before capabilities existed answers 404.
	f.status, f.reply = http.StatusNotFound, `{"error":"Not found."}`
	if _, err := c.Capabilities(t.Context()); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
