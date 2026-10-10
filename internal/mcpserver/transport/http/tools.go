package http

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/gsoultan/hermod/internal/mcpserver"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/version"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const instructions = "Hermod runs data and automation workflows. list_workflows shows the workflows " +
	"you may use; run_workflow sends one an input record and, for workflows that reply, " +
	"returns what the workflow produced. Only workflows their owner tagged \"mcp\" are offered. " +
	"Each predict_<model> tool runs a machine-learning model its vhost exposed on one record and returns the prediction."

type listInput struct{}

type listOutput struct {
	Workflows []mcpserver.WorkflowSummary `json:"workflows"`
}

type statusInput struct {
	WorkflowID string `json:"workflow_id" jsonschema:"the id of the workflow, as list_workflows returns it"`
}

type runInput struct {
	WorkflowID string         `json:"workflow_id" jsonschema:"the id of the workflow, as list_workflows returns it"`
	Input      map[string]any `json:"input,omitempty" jsonschema:"the record to send to the workflow, as a JSON object"`
}

// newServer builds the MCP server that answers one request, bound to the
// user that request authenticated as.
func (m *MCPHandler) newServer(r *http.Request, user *storage.User) *mcp.Server {
	svc := &mcpserver.Service{Store: m.Storage, Wake: m.WakeUpWorkflow, Models: models{svc: m.mlService()}}
	caller := mcpserver.Caller{
		Name:   user.Username,
		CanRun: user.Role == storage.RoleAdministrator || user.Role == storage.RoleEditor,
		MayAccess: func(vhost string) bool {
			return user.Role == storage.RoleAdministrator || m.HasVHostAccess(vhost, user.VHosts)
		},
	}

	srv := mcp.NewServer(&mcp.Implementation{Name: "hermod", Title: "Hermod", Version: version.Version},
		&mcp.ServerOptions{Instructions: instructions})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_workflows",
		Title:       "List workflows",
		Description: "Lists the Hermod workflows exposed to MCP that you may use, with whether each can be run and whether a run returns the workflow's result.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ listInput) (*mcp.CallToolResult, listOutput, error) {
		wfs, err := svc.List(ctx, caller)
		return nil, listOutput{Workflows: wfs}, err
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_workflow_status",
		Title:       "Get workflow status",
		Description: "Returns whether a workflow is active, its status, and its processed, error and lag counters.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in statusInput) (*mcp.CallToolResult, mcpserver.WorkflowStatus, error) {
		st, err := svc.Status(ctx, caller, in.WorkflowID)
		return nil, st, err
	})

	destructive := true
	mcp.AddTool(srv, &mcp.Tool{
		Name:  "run_workflow",
		Title: "Run workflow",
		Description: "Sends an input record to a workflow through its webhook. For a workflow that replies, " +
			"waits for it and returns its status and the record it produced; otherwise confirms the input was queued.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &destructive},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in runInput) (*mcp.CallToolResult, mcpserver.RunResult, error) {
		res, err := svc.Run(ctx, caller, in.WorkflowID, in.Input)
		if err == nil {
			m.RecordAuditLog(r, "INFO", "MCP client ran workflow "+in.WorkflowID, "MCP_RUN_WORKFLOW",
				in.WorkflowID, "", "", map[string]string{"message_id": res.ID, "status": res.Status})
		}
		return nil, res, err
	})

	m.addModelTools(r, srv, svc, caller, user)
	return srv
}

func jsonText(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}
