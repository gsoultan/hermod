package inference

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

// capture serves one canned JSON reply and records what was asked of it.
type capture struct {
	path   string
	auth   string
	body   map[string]any
	status int
	reply  string
}

func (c *capture) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.path = r.URL.Path
		c.auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		c.body = nil
		_ = json.Unmarshal(raw, &c.body)
		if c.status != 0 {
			w.WriteHeader(c.status)
		}
		_, _ = io.WriteString(w, c.reply)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOIPColumnModeSendsOneTensorPerFeatureAndReadsRowsBack(t *testing.T) {
	c := &capture{reply: `{"model_name":"churn","outputs":[
		{"name":"label","shape":[2],"datatype":"INT64","data":[1,0]},
		{"name":"probabilities","shape":[2,2],"datatype":"FP32","data":[0.1,0.9,0.8,0.2]}]}`}
	srv := c.server(t)

	rows := []Row{{"age": 31.0, "plan": "pro"}, {"age": 52.0, "plan": "free"}}
	got, err := NewClient(nil).Predict(context.Background(), Target{
		Backend: BackendOIP, URL: srv.URL, Model: "churn", Version: "3", Token: "tok",
	}, rows)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}

	if c.path != "/v2/models/churn/versions/3/infer" {
		t.Errorf("path = %q, want the versioned V2 infer path", c.path)
	}
	if c.auth != "Bearer tok" {
		t.Errorf("Authorization = %q, want the bearer token", c.auth)
	}
	inputs, _ := c.body["inputs"].([]any)
	if len(inputs) != 2 {
		t.Fatalf("sent %d input tensors, want one per feature: %v", len(inputs), c.body)
	}
	age := inputs[0].(map[string]any)
	if age["name"] != "age" || age["datatype"] != "FP64" || !reflect.DeepEqual(age["data"], []any{31.0, 52.0}) {
		t.Errorf("age tensor = %v", age)
	}
	plan := inputs[1].(map[string]any)
	if plan["name"] != "plan" || plan["datatype"] != "BYTES" || !reflect.DeepEqual(plan["data"], []any{"pro", "free"}) {
		t.Errorf("plan tensor = %v", plan)
	}

	want := []Row{
		{"label": 1.0, "probabilities": []any{0.1, 0.9}},
		{"label": 0.0, "probabilities": []any{0.8, 0.2}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("predictions = %v, want %v", got, want)
	}
}

func TestOIPMatrixModeSendsOneFP32TensorInFeatureOrder(t *testing.T) {
	c := &capture{reply: `{"outputs":[{"name":"variable","shape":[2,1],"datatype":"FP32","data":[10.5,3]}]}`}
	srv := c.server(t)

	rows := []Row{{"b": 2.0, "a": 1.0}, {"a": 3.0, "b": true}}
	got, err := NewClient(nil).Predict(context.Background(), Target{
		Backend: BackendOIP, URL: srv.URL + "/", Model: "price",
		InputName: "input", Features: []string{"a", "b"},
	}, rows)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if c.path != "/v2/models/price/infer" {
		t.Errorf("path = %q, want the unversioned infer path", c.path)
	}
	inputs := c.body["inputs"].([]any)
	if len(inputs) != 1 {
		t.Fatalf("sent %d tensors, want one matrix", len(inputs))
	}
	in := inputs[0].(map[string]any)
	if in["name"] != "input" || in["datatype"] != "FP32" ||
		!reflect.DeepEqual(in["shape"], []any{2.0, 2.0}) ||
		!reflect.DeepEqual(in["data"], []any{1.0, 2.0, 3.0, 1.0}) {
		t.Errorf("matrix tensor = %v", in)
	}
	want := []Row{{"variable": []any{10.5}}, {"variable": []any{3.0}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("predictions = %v, want %v", got, want)
	}
}

func TestOIPMatrixModeRefusesAFeatureThatIsNotANumber(t *testing.T) {
	_, err := NewClient(nil).Predict(context.Background(), Target{
		Backend: BackendOIP, URL: "http://unused", Model: "m",
		InputName: "input", Features: []string{"a"},
	}, []Row{{"a": "high"}})
	if err == nil || !strings.Contains(err.Error(), `"a"`) {
		t.Fatalf("err = %v, want a refusal naming the feature", err)
	}
}

func TestOIPMatrixModeRefusesAMissingFeature(t *testing.T) {
	_, err := NewClient(nil).Predict(context.Background(), Target{
		Backend: BackendOIP, URL: "http://unused", Model: "m",
		InputName: "input", Features: []string{"a", "b"},
	}, []Row{{"a": 1.0}})
	if err == nil || !strings.Contains(err.Error(), `"b"`) {
		t.Fatalf("err = %v, want a refusal naming the missing feature", err)
	}
}

func TestOIPRejectsAnOutputWhoseRowCountDoesNotMatch(t *testing.T) {
	c := &capture{reply: `{"outputs":[{"name":"y","shape":[3],"datatype":"FP32","data":[1,2,3]}]}`}
	srv := c.server(t)
	_, err := NewClient(nil).Predict(context.Background(), Target{Backend: BackendOIP, URL: srv.URL, Model: "m"},
		[]Row{{"a": 1.0}, {"a": 2.0}})
	if err == nil {
		t.Fatal("a reply for 3 rows to a request for 2 was accepted")
	}
}

func TestMLflowSendsDataframeRecordsAndWrapsScalars(t *testing.T) {
	c := &capture{reply: `{"predictions":[0.25, 0.75]}`}
	srv := c.server(t)

	got, err := NewClient(nil).Predict(context.Background(), Target{Backend: BackendMLflow, URL: srv.URL},
		[]Row{{"x": 1.0}, {"x": 2.0}})
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if c.path != "/invocations" {
		t.Errorf("path = %q, want /invocations", c.path)
	}
	if !reflect.DeepEqual(c.body["dataframe_records"], []any{map[string]any{"x": 1.0}, map[string]any{"x": 2.0}}) {
		t.Errorf("body = %v", c.body)
	}
	want := []Row{{"prediction": 0.25}, {"prediction": 0.75}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("predictions = %v, want %v", got, want)
	}
}

func TestMLflowKeepsObjectPredictionsAsTheyAre(t *testing.T) {
	c := &capture{reply: `{"predictions":[{"label":"a","score":0.9}]}`}
	srv := c.server(t)
	got, err := NewClient(nil).Predict(context.Background(), Target{Backend: BackendMLflow, URL: srv.URL}, []Row{{"x": 1.0}})
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if !reflect.DeepEqual(got, []Row{{"label": "a", "score": 0.9}}) {
		t.Errorf("predictions = %v", got)
	}
}

func TestAServerErrorIsReportedWithItsStatusAndBody(t *testing.T) {
	c := &capture{status: http.StatusBadRequest, reply: `{"error":"input shape mismatch"}`}
	srv := c.server(t)
	_, err := NewClient(nil).Predict(context.Background(), Target{Backend: BackendOIP, URL: srv.URL, Model: "m"}, []Row{{"a": 1.0}})
	if err == nil || !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "input shape mismatch") {
		t.Fatalf("err = %v, want the status and the server's message", err)
	}
}

func TestTheTimeoutBoundsTheCall(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
	t.Cleanup(func() { close(block); srv.Close() })

	start := time.Now()
	_, err := NewClient(nil).Predict(context.Background(), Target{
		Backend: BackendMLflow, URL: srv.URL, Timeout: 50 * time.Millisecond,
	}, []Row{{"a": 1.0}})
	if err == nil {
		t.Fatal("a server that never answered produced no error")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("the call took %v; the timeout did not bound it", time.Since(start))
	}
}

func TestNoRowsMakesNoCall(t *testing.T) {
	got, err := NewClient(nil).Predict(context.Background(), Target{Backend: BackendOIP, URL: "http://127.0.0.1:1", Model: "m"}, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("Predict(nil) = %v, %v; want nothing and no call", got, err)
	}
}

func TestValidateTarget(t *testing.T) {
	cases := []struct {
		name string
		t    Target
		ok   bool
	}{
		{"oip ok", Target{Backend: BackendOIP, URL: "http://m:8080", Model: "churn"}, true},
		{"mlflow ok", Target{Backend: BackendMLflow, URL: "https://m"}, true},
		{"unknown backend", Target{Backend: "pickle", URL: "http://m"}, false},
		{"no url", Target{Backend: BackendMLflow}, false},
		{"not http", Target{Backend: BackendMLflow, URL: "file:///etc/passwd"}, false},
		{"oip needs a model", Target{Backend: BackendOIP, URL: "http://m"}, false},
		{"model with a slash", Target{Backend: BackendOIP, URL: "http://m", Model: "../admin"}, false},
		{"matrix needs features", Target{Backend: BackendOIP, URL: "http://m", Model: "x", InputName: "input"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.t.Validate()
			if (err == nil) != tc.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}
