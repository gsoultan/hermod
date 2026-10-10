package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/ml/monitor"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// modelStore is the fixture's store with a model registry and vhosts.
type modelStore struct {
	*store
	models map[string]storage.MLModel
	vhosts []string
}

func (s *modelStore) ListVHosts(context.Context, storage.CommonFilter) ([]storage.VHost, int, error) {
	out := make([]storage.VHost, len(s.vhosts))
	for i, v := range s.vhosts {
		out[i] = storage.VHost{ID: "vh-" + v, Name: v}
	}
	return out, len(out), nil
}

func (s *modelStore) ListMLModels(_ context.Context, vhost string) ([]storage.MLModel, error) {
	var out []storage.MLModel
	for _, m := range s.models {
		if m.VHost == vhost {
			out = append(out, m)
		}
	}
	return out, nil
}
func (s *modelStore) GetMLModel(_ context.Context, vhost, name string) (storage.MLModel, error) {
	m, ok := s.models[vhost+"/"+name]
	if !ok {
		return storage.MLModel{}, storage.ErrNotFound
	}
	return m, nil
}
func (s *modelStore) PutMLModel(_ context.Context, m storage.MLModel) error {
	s.models[m.VHost+"/"+m.Name] = m
	return nil
}
func (s *modelStore) SetMLModelServingKey(_ context.Context, vhost, name, hash string) error {
	m := s.models[vhost+"/"+name]
	m.ServingKeyHash, m.Serving = hash, hash != ""
	s.models[vhost+"/"+name] = m
	return nil
}
func (s *modelStore) DeleteMLModel(context.Context, string, string) error { return nil }
func (s *modelStore) DeleteMLModels(context.Context, string) error        { return nil }

// scorer is an MLflow scoring server that answers amount * 2.
func scorer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Records []map[string]any `json:"dataframe_records"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		preds := make([]any, len(body.Records))
		for i, rec := range body.Records {
			amount, _ := rec["amount"].(float64)
			preds[i] = amount * 2
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"predictions": preds})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// modelFixture serves /api/mcp over a store holding models in two vhosts.
func modelFixture(t *testing.T) (*httptest.Server, *modelStore) {
	t.Helper()
	withJWTConfig(t)
	url := scorer(t)
	model := func(vhost, name string, exposed bool) storage.MLModel {
		return storage.MLModel{
			VHost: vhost, Name: name, Backend: inference.BackendMLflow, URL: url, MCPExposed: exposed,
			Features: []string{"amount"}, FeatureTypes: map[string]string{"amount": "number"},
		}
	}
	st := &modelStore{
		store:  &store{sources: map[string]storage.Source{}},
		vhosts: []string{"tenant-a", "tenant-b"},
		models: map[string]storage.MLModel{},
	}
	for _, m := range []storage.MLModel{
		model("tenant-a", "fraud", true), model("tenant-a", "payroll", false),
		model("tenant-b", "churn", true), model("tenant-b", "fraud", true),
	} {
		st.models[m.VHost+"/"+m.Name] = m
	}
	h := &handlers.Handler{Storage: st, LogStorage: st}
	mux := http.NewServeMux()
	NewMCPHandler(h).RegisterMCPRoutes(mux)
	srv := httptest.NewServer(h.AuthMiddleware(mux))
	t.Cleanup(srv.Close)
	return srv, st
}

func listTools(t *testing.T, cs *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("listing tools: %v", err)
	}
	out := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		out[tool.Name] = tool
	}
	return out
}

func TestExposedModelsArePredictToolsInTheCallersVHostsOnly(t *testing.T) {
	srv, _ := modelFixture(t)
	cs := connect(t, srv, mint(t, "Viewer", "tenant-a"))

	tools := listTools(t, cs)
	fraud, ok := tools["predict_fraud"]
	if !ok {
		t.Fatalf("predict_fraud is not offered; tools: %v", tools)
	}
	for _, hidden := range []string{"predict_payroll", "predict_churn", "predict_fraud__tenant-b"} {
		if _, ok := tools[hidden]; ok {
			t.Errorf("%s is offered to a viewer of tenant-a only", hidden)
		}
	}
	if fraud.Annotations == nil || !fraud.Annotations.ReadOnlyHint {
		t.Error("predict_fraud is not marked read-only")
	}
	schema, _ := json.Marshal(fraud.InputSchema)
	if !strings.Contains(string(schema), `"amount":{"type":"number"}`) || !strings.Contains(string(schema), `"required":["amount"]`) {
		t.Errorf("input schema = %s, want the model's feature, typed and required", schema)
	}

	var got struct {
		Model      string         `json:"model"`
		VHost      string         `json:"vhost"`
		Prediction map[string]any `json:"prediction"`
	}
	structured(t, call(t, cs, "predict_fraud", map[string]any{"amount": 21}), &got)
	if got.Model != "fraud" || got.VHost != "tenant-a" || got.Prediction["prediction"] != 42.0 {
		t.Errorf("prediction = %+v", got)
	}

	if res := call(t, cs, "predict_fraud", map[string]any{"amount": "lots"}); !res.IsError {
		t.Errorf("a wrongly typed feature was sent to the model: %+v", res.StructuredContent)
	}
	if _, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "predict_churn", Arguments: map[string]any{"amount": 1}}); err == nil {
		t.Error("a model of another vhost could be called by name")
	}
}

func TestAnAdministratorSeesEachVHostsModelUnderItsOwnName(t *testing.T) {
	srv, _ := modelFixture(t)
	cs := connect(t, srv, mint(t, "Administrator"))

	tools := listTools(t, cs)
	for _, want := range []string{"predict_fraud__tenant-a", "predict_fraud__tenant-b", "predict_churn"} {
		if _, ok := tools[want]; !ok {
			t.Errorf("%s is not offered to an administrator; tools: %v", want, tools)
		}
	}
	var got struct {
		VHost string `json:"vhost"`
	}
	structured(t, call(t, cs, "predict_fraud__tenant-b", map[string]any{"amount": 1}), &got)
	if got.VHost != "tenant-b" {
		t.Errorf("predict_fraud__tenant-b ran in %q", got.VHost)
	}
}

// A serving key is a credential for one model's serving endpoint. It is not a
// Hermod session, so it opens no MCP tool, that model's included.
func TestAServingKeyDoesNotOpenTheMCPServer(t *testing.T) {
	srv, st := modelFixture(t)
	svc := ml.NewService(func() any { return st }, nil, nil)
	key, err := svc.RotateServingKey(t.Context(), "tenant-a", "fraud")
	if err != nil {
		t.Fatal(err)
	}
	for _, header := range []string{"Authorization", "X-API-Key"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/api/mcp",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"predict_fraud","arguments":{"amount":1}}}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if header == "Authorization" {
			req.Header.Set(header, "Bearer "+key)
		} else {
			req.Header.Set(header, key)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("a serving key in %s reached the MCP server: %d", header, resp.StatusCode)
		}
	}
}

// predictionLog is a prediction log in memory.
type predictionLog struct {
	mu   sync.Mutex
	rows []storage.MLPredictionLog
}

func (l *predictionLog) InsertMLPredictionLogs(_ context.Context, rows []storage.MLPredictionLog) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rows = append(l.rows, rows...)
	return nil
}
func (l *predictionLog) ListMLPredictionLogs(context.Context, string, string, int) ([]storage.MLPredictionLog, error) {
	return nil, nil
}
func (l *predictionLog) PurgeMLPredictionLogs(context.Context, string, string, time.Time) error {
	return nil
}
func (l *predictionLog) DeleteMLPredictionLogs(context.Context, string, string) error { return nil }

// A model tool's prediction is logged like any other, as made over MCP.
func TestAModelToolsPredictionIsLoggedAsMCP(t *testing.T) {
	logs := &predictionLog{}
	mon := monitor.New(monitor.Config{Flush: 5 * time.Millisecond}, monitor.Deps{Logs: func() any { return logs }})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		mon.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	st := &modelStore{store: &store{}, models: map[string]storage.MLModel{
		"a/fraud": {VHost: "a", Name: "fraud", Backend: inference.BackendMLflow, URL: scorer(t),
			Monitoring: storage.MLMonitoring{LogSampleRate: 1}},
	}}
	svc := ml.NewService(func() any { return st }, nil, nil).WithMonitor(mon)
	if _, err := (models{svc: svc}).Predict(t.Context(), "a", "fraud", []map[string]any{{"amount": 2.0}}); err != nil {
		t.Fatalf("Predict: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		logs.mu.Lock()
		rows := slices.Clone(logs.rows)
		logs.mu.Unlock()
		if len(rows) > 0 {
			if rows[0].CallerKind != storage.MLCallerMCP {
				t.Errorf("caller = %q, want %q", rows[0].CallerKind, storage.MLCallerMCP)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the prediction was never logged")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
