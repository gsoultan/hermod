package http

import (
	"context"
	"errors"
	"log"
	"net/http"
	"slices"

	"github.com/gsoultan/hermod/internal/mcpserver"
	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// models is the model registry as the model tools see it: the same service
// the REST and gRPC predict paths use, so quotas and metrics apply alike.
// user is who calls, for the prediction log.
type models struct {
	svc  *ml.Service
	user string
}

func (m models) ListModels(ctx context.Context, vhost string) ([]storage.MLModel, error) {
	ms, err := m.svc.Models()
	if errors.Is(err, storage.ErrMLModelsUnsupported) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ms.ListMLModels(ctx, vhost)
}

// Predict is the service's: an inference.Row is a map[string]any.
func (m models) Predict(ctx context.Context, vhost, name string, rows []map[string]any) ([]map[string]any, error) {
	return m.svc.Predict(ml.WithCaller(ctx, storage.MLCallerMCP, m.user), vhost, name, rows)
}

// mlService is the registry's ML service when the engine runs, so a model's
// token secret is answered as a workflow's is; otherwise one over the API's
// storage, as the model REST handlers do.
func (m *MCPHandler) mlService() *ml.Service {
	if m.Registry != nil {
		return m.Registry.MLService()
	}
	return ml.NewService(func() any { return m.Storage }, nil, nil)
}

// callerVHosts is every vhost the user may use: all of them for an
// Administrator or a "*" grant, otherwise the user's own and the default.
func (m *MCPHandler) callerVHosts(ctx context.Context, user *storage.User) ([]string, error) {
	if user.Role != storage.RoleAdministrator && !slices.Contains(user.VHosts, "*") {
		return append(slices.Clone(user.VHosts), "default"), nil
	}
	vhosts, _, err := m.Storage.ListVHosts(ctx, storage.CommonFilter{Limit: -1})
	if err != nil {
		return nil, err
	}
	out := []string{"default"}
	for _, v := range vhosts {
		out = append(out, v.Name)
	}
	return out, nil
}

// addModelTools adds a predict_<model> tool for every model the user's vhosts
// expose. A failure to list them leaves the workflow tools working and is
// logged, rather than failing the whole request.
func (m *MCPHandler) addModelTools(r *http.Request, srv *mcp.Server, svc *mcpserver.Service, caller mcpserver.Caller, user *storage.User) {
	vhosts, err := m.callerVHosts(r.Context(), user)
	if err != nil {
		log.Printf("mcp: listing the caller's vhosts for model tools: %v", err)
		return
	}
	tools, err := svc.ModelTools(r.Context(), caller, vhosts)
	if err != nil {
		log.Printf("mcp: listing exposed models: %v", err)
		return
	}
	for _, t := range tools {
		srv.AddTool(&mcp.Tool{
			Name:        t.Name,
			Title:       "Predict with " + t.Model,
			Description: t.Description,
			InputSchema: t.InputSchema,
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
		}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			res, err := svc.PredictTool(ctx, caller, t, req.Params.Arguments)
			if err != nil {
				// A refused or failed prediction is a tool result the model
				// reads and can act on, not a protocol error.
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil //nolint:nilerr // see above
			}
			out := &mcp.CallToolResult{StructuredContent: res}
			if b, err := jsonText(res); err == nil {
				out.Content = []mcp.Content{&mcp.TextContent{Text: b}}
			}
			return out, nil
		})
	}
}
