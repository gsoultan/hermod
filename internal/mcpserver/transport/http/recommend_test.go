package http

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gsoultan/hermod/internal/storage"
)

// An exposed recommendation model is a read-only recommend_<model> tool, and
// a call of it goes to the recommendation path, not to Predict: with no ML
// worker here the model cannot load, and the tool says so rather than
// complaining about rows.
func TestAnExposedRecommenderIsARecommendTool(t *testing.T) {
	srv, st := modelFixture(t)
	st.models["tenant-a/recs"] = storage.MLModel{VHost: "tenant-a", Name: "recs", Backend: storage.MLBackendWorker,
		RemoteVersion: "1", Task: storage.MLTaskRecommendation, MCPExposed: true}
	cs := connect(t, srv, mint(t, "Viewer", "tenant-a"))

	tools := listTools(t, cs)
	tool, ok := tools["recommend_recs"]
	if !ok {
		t.Fatalf("recommend_recs is not offered; tools: %v", tools)
	}
	if _, ok := tools["predict_recs"]; ok {
		t.Error("a recommender is offered as a predict tool too")
	}
	if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || !strings.Contains(tool.Description, "recommendation") {
		t.Errorf("tool = %+v", tool)
	}

	res := call(t, cs, "recommend_recs", map[string]any{"user": "1000"})
	text := ""
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcp.TextContent); ok {
			text = tc.Text
		}
	}
	if !res.IsError || !strings.Contains(text, "worker") {
		t.Errorf("result = %v %q, want the missing worker reported", res.IsError, text)
	}
	if res := call(t, cs, "recommend_recs", map[string]any{"k": 3}); !res.IsError {
		t.Error("a call without a user or an item was accepted")
	}
}
