// Package http mounts the MCP server on Hermod's HTTP API.
//
// The endpoint is /api/mcp, Streamable HTTP in stateless mode. It sits behind
// the same AuthMiddleware as every other /api route, so an MCP client
// authenticates exactly as a script would: with a session token in an
// "Authorization: Bearer" header. Each request is answered by a server built
// for the user that request authenticated as, so a tool can only ever see
// what that user may see.
package http

import (
	"context"
	"net/http"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxRequestBytes bounds one JSON-RPC request. Tool input is a workflow's
// input record, so it is held to the same order of size as a webhook body.
const maxRequestBytes = 1 << 20

// MCPHandler serves /api/mcp.
type MCPHandler struct {
	*handlers.Handler
	streamable *mcp.StreamableHTTPHandler
}

type serverKey struct{}

// NewMCPHandler builds the handler.
func NewMCPHandler(h *handlers.Handler) *MCPHandler {
	m := &MCPHandler{Handler: h}
	m.streamable = mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		s, _ := r.Context().Value(serverKey{}).(*mcp.Server)
		return s
	}, &mcp.StreamableHTTPOptions{
		// No session state: every request carries its own credentials and
		// is answered by a server built for them.
		Stateless:    true,
		JSONResponse: true,
		// The SDK's DNS-rebinding guard refuses a loopback connection whose
		// Host is not loopback, which is every deployment behind a reverse
		// proxy on the same machine. The guard protects unauthenticated local
		// servers; this endpoint requires a Hermod session, and a rebound
		// origin does not carry Hermod's cookie or bearer token.
		DisableLocalhostProtection: true,
		MaxRequestBodyBytes:        maxRequestBytes,
	})
	return m
}

// RegisterMCPRoutes mounts the endpoint. Every role may connect; what a role
// may do is decided per tool.
func (m *MCPHandler) RegisterMCPRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/mcp", m.RbacMiddleware(storage.RoleViewer)(m))
	// GET (a server-sent event stream) and DELETE (ending a session) answer
	// 405 from the SDK in stateless mode, which is what a client expects to be
	// told; without these they would fall through to the SPA's 404.
	mux.Handle("GET /api/mcp", m.RbacMiddleware(storage.RoleViewer)(m))
	mux.Handle("DELETE /api/mcp", m.RbacMiddleware(storage.RoleViewer)(m))
}

// ServeHTTP answers one MCP request as the user it authenticated as.
func (m *MCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value(handlers.UserContextKey).(*storage.User)
	if !ok || user == nil {
		m.JsonError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	srv := m.newServer(r, user)
	m.streamable.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), serverKey{}, srv)))
}
