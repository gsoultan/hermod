// Package http serves the natural-language workflow builder.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/workflow/builder"
	workflowhttp "github.com/gsoultan/hermod/internal/workflow/transport/http"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer/genai"
	"github.com/gsoultan/hermod/pkg/llm"
)

const (
	// maxRequestBytes bounds the request: a description and a connection.
	maxRequestBytes = 64 << 10
	// buildTimeout bounds the model call, retries included.
	buildTimeout = 2 * time.Minute
	// maxRefs bounds how many sources and sinks the prompt lists.
	maxRefs = 200
)

// BuilderHandler serves POST /api/ai/build-workflow.
type BuilderHandler struct {
	*handlers.Handler
}

// NewBuilderHandler builds the handler.
func NewBuilderHandler(h *handlers.Handler) *BuilderHandler {
	return &BuilderHandler{Handler: h}
}

// RegisterBuilderRoutes mounts the endpoint. Drafting needs the Editor role:
// it spends the vhost's model credit and its result is meant to be saved.
func (h *BuilderHandler) RegisterBuilderRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/ai/build-workflow", h.EditorOnly(h.BuildWorkflow))
}

// Connection names the model to draft with, as an AI node names it.
type Connection struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// APIKey must be a {{secret("NAME")}} reference, resolved for the vhost.
	APIKey  string `json:"apiKey"`
	BaseURL string `json:"baseUrl"`
}

// BuildRequest is the body of POST /api/ai/build-workflow.
type BuildRequest struct {
	Description string     `json:"description"`
	VHost       string     `json:"vhost"`
	Connection  Connection `json:"connection"`
}

// Usage is what the model call consumed.
type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// BuildResponse is the draft. Saved is always false: the draft is opened in
// the editor and saved, if at all, by the person through the normal path.
type BuildResponse struct {
	Workflow storage.Workflow `json:"workflow"`
	Issues   []builder.Issue  `json:"issues"`
	Saved    bool             `json:"saved"`
	Provider string           `json:"provider"`
	Model    string           `json:"model"`
	Usage    Usage            `json:"usage"`
}

// BuildWorkflow drafts a workflow from a description. Nothing is saved and
// nothing is started.
func (h *BuilderHandler) BuildWorkflow(w http.ResponseWriter, r *http.Request) {
	req, ok := h.admit(w, r)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), buildTimeout)
	defer cancel()

	sources, sinks, err := h.refs(ctx, req.VHost)
	if err != nil {
		h.JsonError(w, "Failed to read the vhost's sources and sinks: "+err.Error(), http.StatusInternalServerError)
		return
	}

	provider, model, err := providerFor(req)
	if err != nil {
		h.JsonError(w, "AI connection: "+err.Error(), http.StatusBadRequest)
		return
	}

	draft, err := builder.Build(ctx, provider, builder.Request{
		Description: req.Description, VHost: req.VHost, Model: model, Sources: sources, Sinks: sinks,
	})
	switch {
	case errors.Is(err, builder.ErrEmptyDescription), errors.Is(err, builder.ErrDescriptionTooLong):
		h.JsonError(w, err.Error(), http.StatusBadRequest)
		return
	case err != nil:
		h.JsonError(w, "Drafting failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	issues := h.validate(ctx, draft)
	h.RecordAuditLog(r, "INFO", "AI drafted a workflow", "AI_BUILD_WORKFLOW", "", "", "", map[string]any{
		"vhost": req.VHost, "provider": draft.Provider, "model": draft.Model,
		"nodes": len(draft.Workflow.Nodes), "issues": len(issues),
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(BuildResponse{
		Workflow: draft.Workflow, Issues: issues, Saved: false,
		Provider: draft.Provider, Model: draft.Model,
		Usage: Usage{InputTokens: draft.Usage.InputTokens, OutputTokens: draft.Usage.OutputTokens},
	})
}

// admit reads the request and refuses, having answered, one the caller may
// not make: a vhost outside theirs, or a connection without a secret key.
func (h *BuilderHandler) admit(w http.ResponseWriter, r *http.Request) (BuildRequest, bool) {
	var req BuildRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes)).Decode(&req); err != nil {
		h.JsonError(w, "Invalid request: "+err.Error(), http.StatusBadRequest)
		return req, false
	}
	role, vhosts := h.GetRoleAndVHosts(r)
	if role != storage.RoleAdministrator && !h.HasVHostAccess(req.VHost, vhosts) {
		h.JsonError(w, "Forbidden: you do not have access to this vhost", http.StatusForbidden)
		return req, false
	}
	if msg := checkConnection(req.Connection); msg != "" {
		h.JsonError(w, msg, http.StatusBadRequest)
		return req, false
	}
	return req, true
}

// providerFor reads the connection exactly as an AI node's is read, for a
// message of the request's vhost: {{secret("NAME")}} answers from that
// vhost's secrets and from nobody else's.
func providerFor(req BuildRequest) (llm.Provider, string, error) {
	scope := message.AcquireMessage()
	defer message.ReleaseMessage(scope)
	scope.SetVHost(req.VHost)
	return genai.ProviderFor(map[string]any{
		"provider": req.Connection.Provider, "model": req.Connection.Model,
		"apiKey": req.Connection.APIKey, "baseUrl": req.Connection.BaseURL,
	}, scope)
}

// validate runs the draft through the workflow validator every save goes
// through, after the builder's own findings.
func (h *BuilderHandler) validate(ctx context.Context, draft builder.Draft) []builder.Issue {
	issues := append([]builder.Issue{}, draft.Issues...)
	for _, v := range workflowhttp.NewWorkflowHandler(h.Handler).ValidateWorkflow(ctx, draft.Workflow) {
		issues = append(issues, builder.Issue{Severity: v.Severity, Message: v.Message, Recommendation: v.Recommendation, NodeID: v.NodeID})
	}
	return issues
}

// checkConnection returns why a connection is refused, or "".
func checkConnection(c Connection) string {
	if strings.TrimSpace(c.Provider) == "" {
		return "connection.provider is required"
	}
	if key := strings.TrimSpace(c.APIKey); key != "" && !strings.Contains(key, "{{") {
		return `connection.apiKey must be a vhost secret reference such as {{secret("OPENAI_API_KEY")}}, not the key itself`
	}
	return ""
}

// refs lists the sources and sinks in vhost, and only in vhost: storage
// reads an empty vhost filter as "every vhost".
func (h *BuilderHandler) refs(ctx context.Context, vhost string) (sources, sinks []builder.Ref, err error) {
	srcs, _, err := h.Storage.ListSources(ctx, storage.CommonFilter{VHost: vhost, Limit: maxRefs})
	if err != nil {
		return nil, nil, err
	}
	for _, s := range srcs {
		if s.VHost == vhost {
			sources = append(sources, builder.Ref{ID: s.ID, Name: s.Name, Type: s.Type})
		}
	}
	snks, _, err := h.Storage.ListSinks(ctx, storage.CommonFilter{VHost: vhost, Limit: maxRefs})
	if err != nil {
		return nil, nil, err
	}
	for _, s := range snks {
		if s.VHost == vhost {
			sinks = append(sinks, builder.Ref{ID: s.ID, Name: s.Name, Type: s.Type})
		}
	}
	return sources, sinks, nil
}
