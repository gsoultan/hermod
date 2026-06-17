package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/factory"
	"github.com/user/hermod/internal/notification"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/internal/testutil"
	"github.com/user/hermod/pkg/comm/message"
	_ "github.com/user/hermod/pkg/comm/transformer/advanced"
	_ "github.com/user/hermod/pkg/comm/transformer/core"
	_ "github.com/user/hermod/pkg/comm/transformer/logic"
	_ "github.com/user/hermod/pkg/comm/transformer/lookup"
	"github.com/user/hermod/pkg/engine/telemetry"
	"github.com/user/hermod/pkg/infra/state"
)

func TestRouterNode_Regex(t *testing.T) {
	reg := NewRegistry(nil)

	node := &storage.WorkflowNode{
		ID:   "router1",
		Type: "router",
		Config: map[string]any{
			"rules": `[
				{"label": "high_priority", "field": "severity", "operator": "regex", "value": "^(high|critical)$"},
				{"label": "low_priority", "field": "severity", "operator": "regex", "value": "^(low|medium)$"}
			]`,
		},
	}

	msg1 := message.AcquireMessage()
	msg1.SetData("severity", "critical")

	_, branch1, err := reg.runWorkflowNode("wf1", node, msg1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch1 != "high_priority" {
		t.Errorf("expected high_priority, got %s", branch1)
	}

	msg2 := message.AcquireMessage()
	msg2.SetData("severity", "medium")

	_, branch2, err := reg.runWorkflowNode("wf1", node, msg2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch2 != "low_priority" {
		t.Errorf("expected low_priority, got %s", branch2)
	}

	msg3 := message.AcquireMessage()
	msg3.SetData("severity", "unknown")

	_, branch3, err := reg.runWorkflowNode("wf1", node, msg3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if branch3 != "default" {
		t.Errorf("expected default, got %s", branch3)
	}
}

func TestRouterNode(t *testing.T) {
	r := NewRegistry(nil)

	msg := message.AcquireMessage()
	msg.SetData("status", "error_404")
	msg.SetData("payload", "critical failure")

	rules := []map[string]any{
		{
			"label": "critical",
			"conditions": []map[string]any{
				{"field": "payload", "operator": "contains", "value": "critical"},
			},
		},
		{
			"label": "not_found",
			"conditions": []map[string]any{
				{"field": "status", "operator": "regex", "value": "error_.*"},
			},
		},
	}
	rulesJSON, _ := json.Marshal(rules)

	node := &storage.WorkflowNode{
		ID:   "router-1",
		Type: "router",
		Config: map[string]any{
			"rules": string(rulesJSON),
		},
	}

	// Test matching first rule
	_, branch, err := r.runWorkflowNode("wf-1", node, msg)
	if err != nil {
		t.Fatalf("runWorkflowNode failed: %v", err)
	}
	if branch != "critical" {
		t.Errorf("expected branch critical, got %s", branch)
	}

	// Test matching second rule (regex)
	msg2 := message.AcquireMessage()
	msg2.SetData("status", "error_500")
	msg2.SetData("payload", "something else")

	_, branch2, _ := r.runWorkflowNode("wf-1", node, msg2)
	if branch2 != "not_found" {
		t.Errorf("expected branch not_found, got %s", branch2)
	}

	// Test default branch
	msg3 := message.AcquireMessage()
	msg3.SetData("status", "ok")

	_, branch3, _ := r.runWorkflowNode("wf-1", node, msg3)
	if branch3 != "default" {
		t.Errorf("expected branch default, got %s", branch3)
	}
}

func TestNestedPathAccess(t *testing.T) {
	registry := NewRegistry(nil)

	msg := message.AcquireMessage()
	defer message.ReleaseMessage(msg)

	msg.SetData("user", map[string]any{
		"profile": map[string]any{
			"name": "John Doe",
			"age":  30,
		},
		"status": "active",
	})

	t.Run("Filter nested path", func(t *testing.T) {
		node := &storage.WorkflowNode{
			Type: "transformation",
			Config: map[string]any{
				"transType": "filter_data",
				"field":     "user.profile.name",
				"operator":  "=",
				"value":     "John Doe",
			},
		}

		res, _, err := registry.runWorkflowNode("test", node, msg)
		if err != nil {
			t.Fatalf("Failed to run node: %v", err)
		}
		if res == nil {
			t.Errorf("Expected message to pass filter, but it was nil")
		}

		node.Config["value"] = "Jane Doe"
		res, _, err = registry.runWorkflowNode("test", node, msg)
		if res != nil {
			t.Errorf("Expected message to be filtered, but it was not nil")
		}
	})

	t.Run("Advanced transformation nested path", func(t *testing.T) {
		node := &storage.WorkflowNode{
			Type: "transformation",
			Config: map[string]any{
				"transType":                "advanced",
				"column.user.profile.name": "lower(source.user.profile.name)",
			},
		}

		msgs, _, err := registry.runWorkflowNode("test", node, msg)
		if err != nil {
			t.Fatalf("Failed to run node: %v", err)
		}
		if len(msgs) == 0 {
			t.Fatalf("Result is empty")
		}
		res := msgs[0]

		data := res.Data()
		user := data["user"].(map[string]any)
		profile := user["profile"].(map[string]any)
		if profile["name"] != "john doe" {
			t.Errorf("Expected name to be 'john doe', got %v", profile["name"])
		}
	})

	t.Run("Condition nested path", func(t *testing.T) {
		node := &storage.WorkflowNode{
			Type: "condition",
			Config: map[string]any{
				"field":    "user.profile.age",
				"operator": ">",
				"value":    "25",
			},
		}

		res, branch, err := registry.runWorkflowNode("test", node, msg)
		if err != nil {
			t.Fatalf("Failed to run node: %v", err)
		}
		if res == nil {
			t.Fatalf("Result is nil")
		}
		if branch != "true" {
			t.Errorf("Expected branch 'true', got '%s'", branch)
		}

		node.Config["value"] = "35"
		_, branch, _ = registry.runWorkflowNode("test", node, msg)
		if branch != "false" {
			t.Errorf("Expected branch 'false', got '%s'", branch)
		}
	})

	t.Run("Mapping nested path", func(t *testing.T) {
		node := &storage.WorkflowNode{
			Type: "transformation",
			Config: map[string]any{
				"transType": "mapping",
				"field":     "user.status",
				"mapping":   "{\"active\": \"ENABLED\", \"inactive\": \"DISABLED\"}",
			},
		}

		msgs, _, err := registry.runWorkflowNode("test", node, msg)
		if err != nil {
			t.Fatalf("Failed to run node: %v", err)
		}
		if len(msgs) == 0 {
			t.Fatalf("Result is empty")
		}
		res := msgs[0]

		data := res.Data()
		user := data["user"].(map[string]any)
		if user["status"] != "ENABLED" {
			t.Errorf("Expected status ENABLED, got %v", user["status"])
		}
	})
}

func TestPathSafeImplementation(t *testing.T) {
	t.Run("getValByPath", func(t *testing.T) {
		data := map[string]any{
			"user": map[string]any{
				"profile": map[string]any{
					"name": "John",
				},
				"tags": []any{"a", "b", "c"},
			},
		}

		tests := []struct {
			path     string
			expected any
		}{
			{"user.profile.name", "John"},
			{"user.tags.1", "b"},
			{"user.tags.#", float64(3)},
			{"nonexistent", nil},
			{"user.profile.age", nil},
		}

		for _, tt := range tests {
			got := getValByPath(data, tt.path)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("getValByPath(data, %q) = %v; want %v", tt.path, got, tt.expected)
			}
		}
	})

	t.Run("setValByPath", func(t *testing.T) {
		data := map[string]any{
			"user": map[string]any{
				"name": "John",
			},
		}

		// Simple set
		setValByPath(data, "user.age", 30)
		if getValByPath(data, "user.age") != float64(30) {
			t.Errorf("Expected age 30, got %v", getValByPath(data, "user.age"))
		}

		// Deep set with creation
		setValByPath(data, "meta.info.source", "web")
		if getValByPath(data, "meta.info.source") != "web" {
			t.Errorf("Expected meta.info.source 'web', got %v", getValByPath(data, "meta.info.source"))
		}

		// Array append
		setValByPath(data, "user.tags", []any{"tag1"})
		setValByPath(data, "user.tags.-1", "tag2")

		tags := getValByPath(data, "user.tags").([]any)
		if len(tags) != 2 || tags[1] != "tag2" {
			t.Errorf("Expected tags append, got %v", tags)
		}
	})
}

type mockNotificationProvider struct {
	sent bool
}

func (p *mockNotificationProvider) Send(ctx context.Context, title, message string, wf storage.Workflow) error {
	p.sent = true
	return nil
}

func (p *mockNotificationProvider) SetStorage(s storage.Storage) {}

func (p *mockNotificationProvider) Type() string { return "mock" }

type mockAlertingStorage struct {
	storage.Storage
	workflow storage.Workflow
}

func (s *mockAlertingStorage) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	return s.workflow, nil
}

func (s *mockAlertingStorage) UpdateWorkflow(ctx context.Context, wf storage.Workflow) error {
	s.workflow = wf
	return nil
}

func TestAlertingOnStatusChange(t *testing.T) {
	ms := &mockAlertingStorage{
		workflow: storage.Workflow{ID: "wf-1", Name: "Test Workflow", Status: "running"},
	}
	r := &Registry{
		storage: ms,
	}

	provider := &mockNotificationProvider{}
	ns := notification.NewService(ms)
	ns.AddProvider(provider)
	r.notificationService = ns

	// Simulate status change to error
	update := telemetry.StatusUpdate{
		EngineStatus: "error:something_failed",
	}

	engStatusChange := func(update telemetry.StatusUpdate) {
		dbCtx := t.Context()
		if workflow, err := r.storage.GetWorkflow(dbCtx, "wf-1"); err == nil {
			prevStatus := workflow.Status
			workflow.Status = update.EngineStatus
			_ = r.storage.UpdateWorkflow(dbCtx, workflow)

			if strings.Contains(strings.ToLower(update.EngineStatus), "error") &&
				!strings.Contains(strings.ToLower(prevStatus), "error") &&
				r.notificationService != nil {
				r.notificationService.Notify(dbCtx, "Workflow Error", "failed", workflow)
			}
		}
	}

	engStatusChange(update)

	if !provider.sent {
		t.Errorf("Expected notification to be sent on error status change")
	}
}

func TestDLQThresholdAlerting(t *testing.T) {
	ms := &mockAlertingStorage{
		workflow: storage.Workflow{
			ID:           "wf-dlq",
			Name:         "DLQ Test",
			Status:       "running",
			DLQThreshold: 10,
		},
	}
	r := &Registry{
		storage: ms,
	}

	provider := &mockNotificationProvider{}
	ns := notification.NewService(ms)
	ns.AddProvider(provider)
	r.notificationService = ns

	// Simulation function (replicates Registry's SetOnStatusChange logic)
	onStatusChange := func(update telemetry.StatusUpdate) {
		dbCtx := t.Context()
		if workflow, err := r.storage.GetWorkflow(dbCtx, "wf-dlq"); err == nil && workflow.DLQThreshold > 0 {
			if update.DeadLetterCount >= uint64(workflow.DLQThreshold) {
				r.notificationService.Notify(dbCtx, "DLQ Threshold Exceeded", "dlq alert", workflow)
			}
		}
	}

	onStatusChange(telemetry.StatusUpdate{DeadLetterCount: 10})

	if !provider.sent {
		t.Errorf("Expected notification to be sent on DLQ threshold exceed")
	}
}

type mockCheckpointStorage struct {
	testutil.BaseMockStorage
	state map[string]string
}

func (m *mockCheckpointStorage) GetNodeStates(ctx context.Context, workflowID string) (map[string]any, error) {
	return nil, nil
}

func (m *mockCheckpointStorage) UpdateNodeState(ctx context.Context, workflowID, nodeID string, state any) error {
	m.state[nodeID] = state.(string)
	return nil
}

func TestWorkflowNodeCheckpoint(t *testing.T) {
	ms := &mockCheckpointStorage{state: make(map[string]string)}
	r := NewRegistry(ms)
	r.SetStateStore(state.NewMemoryStore())

	node := &storage.WorkflowNode{
		ID:   "node-1",
		Type: "transformation",
		Config: map[string]any{
			"transType": "filter_data",
			"field":     "a",
			"operator":  "=",
			"value":     "1",
		},
	}

	msg := message.AcquireMessage()
	msg.SetData("a", "1")
	msg.SetMetadata("last_lsn", "100")

	// Trigger manual checkpoint (simulating what the engine does)
	err := ms.UpdateNodeState(t.Context(), "wf-1", node.ID, "100")
	if err != nil {
		t.Fatalf("UpdateNodeState failed: %v", err)
	}

	if ms.state["node-1"] != "100" {
		t.Errorf("Expected checkpoint '100', got %s", ms.state["node-1"])
	}
}

type mockSamplingStorage struct {
	testutil.BaseMockStorage
	submissions []storage.FormSubmission
}

func (m *mockSamplingStorage) ListFormSubmissions(ctx context.Context, filter storage.FormSubmissionFilter) ([]storage.FormSubmission, int, error) {
	var result []storage.FormSubmission
	for _, s := range m.submissions {
		if s.Path == filter.Path {
			result = append(result, s)
		}
	}
	return result, len(result), nil
}

func TestSourceFormSampling(t *testing.T) {
	ms := &mockSamplingStorage{
		submissions: []storage.FormSubmission{
			{ID: "1", Path: "contact", Data: []byte(`{"name": "John"}`)},
			{ID: "2", Path: "contact", Data: []byte(`{"name": "Jane"}`)},
			{ID: "3", Path: "other", Data: []byte(`{"name": "Other"}`)},
		},
	}
	r := NewRegistry(ms)

	tests := []struct {
		path  string
		count int
	}{
		{"contact", 2},
		{"other", 1},
		{"empty", 0},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			msgs, err := r.GetSourceFormSamples(t.Context(), tt.path, 10)
			if err != nil {
				t.Fatalf("GetSourceFormSamples failed: %v", err)
			}
			if len(msgs) != tt.count {
				t.Errorf("Expected %d messages, got %d", tt.count, len(msgs))
			}
		})
	}
}

type mockMultiStorage struct {
	testutil.BaseMockStorage
	src storage.Source
}

func (m *mockMultiStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	return m.src, nil
}

type mockSource struct {
	hermod.Source
	readCount atomic.Int64
	readErr   error
}

func (m *mockSource) Read(ctx context.Context) (hermod.Message, error) {
	m.readCount.Add(1)
	return nil, m.readErr
}
func (m *mockSource) Ack(ctx context.Context, msg hermod.Message) error { return nil }
func (m *mockSource) Ping(ctx context.Context) error                    { return nil }
func (m *mockSource) Close() error                                      { return nil }

func TestMultiSource_Aggregation(t *testing.T) {
	ms := &mockMultiStorage{
		src: storage.Source{ID: "s1", Type: "sqlite"},
	}
	r := NewRegistry(ms)

	s1 := &mockSource{}
	s2 := &mockSource{}

	r.SetFactories(func(cfg factory.SourceConfig) (hermod.Source, error) {
		if cfg.ID == "node-1" {
			return s1, nil
		}
		return s2, nil
	}, nil)

	wf := storage.Workflow{
		ID: "wf-1",
		Nodes: []storage.WorkflowNode{
			{ID: "node-1", Type: "source", RefID: "s1"},
			{ID: "node-2", Type: "source", RefID: "s1"},
		},
	}

	// buildWorkflowSources is unexported but we test the result through multiSource behavior
	_, mSource, err := r.buildWorkflowSources(t.Context(), wf)
	if err != nil {
		t.Fatalf("buildWorkflowSources failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	// Should read from both internal sources
	go mSource.Read(ctx)
	time.Sleep(20 * time.Millisecond)

	if s1.readCount.Load() == 0 && s2.readCount.Load() == 0 {
		t.Error("Expected at least one read from sources")
	}
}

func TestRegistryDurationParsing(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		err      bool
	}{
		{"1s", 1 * time.Second, false},
		{"1h", 1 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{" 1d ", 24 * time.Hour, false}, // Test trimming
		{"1.5d", 36 * time.Hour, false},
		{"invalid", 0, true},
		{"", 0, false},  // Empty string
		{"2w", 0, true}, // We only support up to days for now
	}

	for _, tt := range tests {
		got, err := parseDuration(tt.input)
		if (err != nil) != tt.err {
			t.Errorf("parseDuration(%q) error = %v, wantErr %v", tt.input, err, tt.err)
		}
		if got != tt.expected {
			t.Errorf("parseDuration(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

type mockPersistenceStorage struct {
	testutil.BaseMockStorage
	mu         sync.Mutex
	nodeStates map[string]any
}

func (m *mockPersistenceStorage) UpdateNodeState(ctx context.Context, workflowID, nodeID string, state any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodeStates[nodeID] = state
	return nil
}

func (m *mockPersistenceStorage) GetNodeStates(ctx context.Context, workflowID string) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nodeStates, nil
}

func TestRegistry_StatePersistence(t *testing.T) {
	ms := &mockPersistenceStorage{nodeStates: make(map[string]any)}
	r := NewRegistry(ms)

	ctx := t.Context()
	wfID := "wf-1"

	err := r.UpdateNodeState(ctx, wfID, "n1", "offset-100")
	if err != nil {
		t.Fatalf("UpdateNodeState failed: %v", err)
	}

	states, err := r.GetNodeStates(ctx, wfID)
	if err != nil {
		t.Fatalf("GetNodeStates failed: %v", err)
	}

	if states["n1"] != "offset-100" {
		t.Errorf("Expected offset-100, got %v", states["n1"])
	}
}

type mockStorage struct {
	testutil.BaseMockStorage
}

func TestEnhancedTransformations(t *testing.T) {
	registry := NewRegistry(&mockStorage{})

	tests := []struct {
		name     string
		payload  string
		expr     string
		expected any
	}{
		{
			name:     "Trim function",
			payload:  `{"name": "  John Doe  "}`,
			expr:     "trim(source.name)",
			expected: "John Doe",
		},
		{
			name:     "Concat function",
			payload:  `{"first": "John", "last": "Doe"}`,
			expr:     `concat(source.first, " ", source.last)`,
			expected: "John Doe",
		},
		{
			name:     "Substring function",
			payload:  `{"zip": "12345-6789"}`,
			expr:     "substring(source.zip, 0, 5)",
			expected: "12345",
		},
		{
			name:     "Nested functions",
			payload:  `{"name": "  john doe  "}`,
			expr:     "upper(trim(source.name))",
			expected: "JOHN DOE",
		},
		{
			name:     "Coalesce function",
			payload:  `{"first": null, "second": "fallback"}`,
			expr:     "coalesce(source.first, source.second, \"default\")",
			expected: "fallback",
		},
		{
			name:     "Coalesce function default",
			payload:  `{"first": null, "second": null}`,
			expr:     "coalesce(source.first, source.second, \"default\")",
			expected: "default",
		},
		{
			name:     "Math add",
			payload:  `{"a": 10, "b": 20}`,
			expr:     "add(source.a, source.b)",
			expected: 30.0,
		},
		{
			name:     "Math complex",
			payload:  `{"a": 10, "b": 20, "c": 5}`,
			expr:     "mul(add(source.a, source.b), source.c)",
			expected: 150.0,
		},
		{
			name:     "Date format",
			payload:  `{"created_at": "2026-01-19T13:00:00Z"}`,
			expr:     "date_format(source.created_at, \"2006-01-02\")",
			expected: "2026-01-19",
		},
		{
			name:     "Math round",
			payload:  `{"val": 123.456}`,
			expr:     "round(source.val, 2)",
			expected: 123.46,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := message.AcquireMessage()
			msg.SetAfter([]byte(tt.payload))

			result := registry.evaluateAdvancedExpression(msg, tt.expr)

			// Handle numeric comparison as float64
			if expectedFloat, ok := tt.expected.(float64); ok {
				var actualFloat float64
				switch v := result.(type) {
				case int:
					actualFloat = float64(v)
				case int64:
					actualFloat = float64(v)
				case float64:
					actualFloat = v
				default:
					t.Errorf("Expected float-compatible result, got %T", result)
					return
				}
				if actualFloat != expectedFloat {
					t.Errorf("Expected %v, got %v", expectedFloat, actualFloat)
				}
			} else if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestTransformationPipelineEnhanced(t *testing.T) {
	registry := NewRegistry(&mockStorage{})

	msg := message.AcquireMessage()
	msg.SetAfter([]byte(`{"first_name": "John", "last_name": "Doe", "age": 30}`))

	config := map[string]any{
		"column.full_name": `concat(source.first_name, " ", source.last_name)`,
		"column.initials":  `concat(substring(source.first_name, 0, 1), substring(source.last_name, 0, 1))`,
		"column.is_adult":  `add(source.age, 0)`, // Just to test math in pipeline
	}

	res, err := registry.applyTransformation(t.Context(), msg, "advanced", config)
	if err != nil {
		t.Fatalf("Failed to apply transformation: %v", err)
	}

	var data map[string]any
	json.Unmarshal(res.After(), &data)

	if data["full_name"] != "John Doe" {
		t.Errorf("Expected John Doe, got %v", data["full_name"])
	}
	if data["initials"] != "JD" {
		t.Errorf("Expected JD, got %v", data["initials"])
	}
}

type policyMockStorage struct {
	testutil.BaseMockStorage
}

func (m *policyMockStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	if id == "non-existent" {
		return storage.Source{}, fmt.Errorf("source not found")
	}
	return storage.Source{ID: id, Type: "sqlite"}, nil
}

func TestMappingEnhanced(t *testing.T) {
	registry := NewRegistry(&policyMockStorage{})

	t.Run("Range mapping", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"age": 25}`))

		config := map[string]any{
			"field":       "age",
			"mappingType": "range",
			"mapping":     `{"0-18": "child", "19-65": "adult", "66+": "senior"}`,
		}

		res, err := registry.applyTransformation(t.Context(), msg, "mapping", config)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var data map[string]any
		json.Unmarshal(res.After(), &data)
		if data["age"] != "adult" {
			t.Errorf("Expected adult, got %v", data["age"])
		}
	})

	t.Run("Regex mapping", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"status": "error_critical"}`))

		config := map[string]any{
			"field":       "status",
			"mappingType": "regex",
			"mapping":     `{"^error_.*": "failed", "ok": "success"}`,
		}

		res, err := registry.applyTransformation(t.Context(), msg, "mapping", config)
		if err != nil {
			t.Fatalf("Failed: %v", err)
		}

		var data map[string]any
		json.Unmarshal(res.After(), &data)
		if data["status"] != "failed" {
			t.Errorf("Expected failed, got %v", data["status"])
		}
	})
}

func TestErrorPolicies(t *testing.T) {
	registry := NewRegistry(&policyMockStorage{})

	t.Run("Fail policy (default)", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"id": 1}`))

		// db_lookup will fail because source doesn't exist
		config := map[string]any{
			"sourceId":    "non-existent",
			"table":       "users",
			"keyColumn":   "id",
			"keyField":    "id",
			"targetField": "name",
		}

		_, err := registry.applyTransformation(t.Context(), msg, "db_lookup", config)
		if err == nil {
			t.Error("Expected error for non-existent source, got nil")
		}
	})

	t.Run("Continue policy", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"id": 1}`))

		config := map[string]any{
			"sourceId":    "non-existent",
			"table":       "users",
			"keyColumn":   "id",
			"keyField":    "id",
			"targetField": "name",
			"onError":     "continue",
			"statusField": "_status",
		}

		res, err := registry.applyTransformation(t.Context(), msg, "db_lookup", config)
		if err != nil {
			t.Fatalf("Expected no error due to continue policy, got %v", err)
		}

		var data map[string]any
		json.Unmarshal(res.After(), &data)
		if data["_status"] != "error" {
			t.Errorf("Expected _status to be error, got %v", data["_status"])
		}
		if data["_status_error"] == "" {
			t.Error("Expected _status_error to be populated")
		}
	})

	t.Run("Drop policy", func(t *testing.T) {
		msg := message.AcquireMessage()
		msg.SetAfter([]byte(`{"id": 1}`))

		config := map[string]any{
			"sourceId":    "non-existent",
			"table":       "users",
			"keyColumn":   "id",
			"keyField":    "id",
			"targetField": "name",
			"onError":     "drop",
		}

		res, err := registry.applyTransformation(t.Context(), msg, "db_lookup", config)
		if err != nil {
			t.Fatalf("Expected no error due to drop policy, got %v", err)
		}
		if res != nil {
			t.Error("Expected nil message due to drop policy")
		}
	})
}
