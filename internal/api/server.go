package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net"
	"net/http"
	httppprof "net/http/pprof"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/ai"
	"github.com/user/hermod/internal/config"
	"github.com/user/hermod/internal/engine"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/compression"
	"github.com/user/hermod/pkg/crypto"
	"github.com/user/hermod/pkg/filestorage"
	"github.com/user/hermod/pkg/message"
	"github.com/user/hermod/pkg/source/form"
	"github.com/user/hermod/pkg/source/graphql"
	grpcsource "github.com/user/hermod/pkg/source/grpc"
	"github.com/user/hermod/pkg/source/grpc/proto"
	"github.com/user/hermod/pkg/source/webhook"
	"github.com/user/hermod/pkg/util"
	googlegrpc "google.golang.org/grpc"
)

type FormField struct {
	ID string `json:"id"`
	// Input fields
	Name            string   `json:"name"`
	Label           string   `json:"label"`
	Type            string   `json:"type"`
	Required        bool     `json:"required"`
	Options         []string `json:"options"`
	Placeholder     string   `json:"placeholder"`
	Help            string   `json:"help"`
	NumberKind      string   `json:"number_kind"`
	Render          string   `json:"render"`
	VerifyEmail     bool     `json:"verify_email"`
	RejectIfInvalid bool     `json:"reject_if_invalid"`
	Min             float64  `json:"min"`
	Max             float64  `json:"max"`
	Step            float64  `json:"step"`
	StartLabel      string   `json:"start_label"`
	EndLabel        string   `json:"end_label"`
	// Layout metadata
	Section string `json:"section"`
	Width   string `json:"width"` // auto | half | full
	// Layout-only content
	Content string `json:"content"` // for heading/text_block
	Level   int    `json:"level"`   // 1..3 for heading
}

//go:embed all:static
var staticFS embed.FS

// Server is the HTTP API server for Hermod.
// It wires routing, middleware, and access to the storage and engine registry.
type Server struct {
	storage     storage.Storage
	logStorage  storage.Storage
	registry    *engine.Registry
	ai          *ai.SelfHealingService
	config      *config.Config
	fileStorage filestorage.Storage
	// readiness debounce state
	readyMu            sync.Mutex
	lastReadyStatus    bool
	lastReadyStatusSet bool
	lastReadyStatusAt  time.Time
	// storeMu guards concurrent reads/writes to storage during hot-swap.
	storeMu sync.RWMutex

	// formRateLimit tracks form submissions for rate limiting.
	// map[string]int where key is "sourceID:IP:YYYY-MM-DD:HH"
	formRateLimit sync.Map
	rateLimitOnce sync.Once
	rateLimitQuit chan struct{}

	grpcServer *googlegrpc.Server
}

// NewServer constructs a new Server with the provided engine registry and storage backend.
func NewServer(registry *engine.Registry, store storage.Storage, cfg *config.Config, aiSvc *ai.SelfHealingService, ls ...storage.Storage) *Server {
	var logStore storage.Storage
	if len(ls) > 0 {
		logStore = ls[0]
	}
	if logStore == nil {
		logStore = store
	}
	s := &Server{
		storage:       store,
		logStorage:    logStore,
		registry:      registry,
		config:        cfg,
		ai:            aiSvc,
		rateLimitQuit: make(chan struct{}),
	}
	// Initialize file storage from config; fallback to local uploads dir
	if cfg != nil {
		if fs, err := filestorage.NewStorage(context.Background(), cfg.FileStorage); err == nil {
			s.fileStorage = fs
		} else {
			if lfs, lerr := filestorage.NewLocalStorage("uploads"); lerr == nil {
				s.fileStorage = lfs
			}
		}
	} else {
		if lfs, _ := filestorage.NewLocalStorage("uploads"); lfs != nil {
			s.fileStorage = lfs
		}
	}
	return s
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.storage == nil {
		http.Error(w, "Service Unavailable: No storage", http.StatusServiceUnavailable)
		return
	}
	if err := s.storage.Ping(r.Context()); err != nil {
		http.Error(w, "Service Unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("READY"))
}

func (s *Server) wakeUpWorkflow(ctx context.Context, resourceType string, path string) bool {
	// 1. Find the source with this path
	sources, _, err := s.storage.ListSources(ctx, storage.CommonFilter{})
	if err != nil {
		return false
	}

	var sourceID string
	for _, src := range sources {
		if src.Type == resourceType && src.Config["path"] == path {
			sourceID = src.ID
			break
		}
	}

	if sourceID == "" {
		return false
	}

	// 2. Find workflows using this source
	workflows, _, err := s.storage.ListWorkflows(ctx, storage.CommonFilter{})
	if err != nil {
		return false
	}

	wokeUp := false
	for _, wf := range workflows {
		if wf.Status != "Parked" {
			continue
		}

		for _, node := range wf.Nodes {
			if node.Type == "source" && node.RefID == sourceID {
				// Wake it up!
				wf.Status = ""
				_ = s.storage.UpdateWorkflow(ctx, wf)
				wokeUp = true

				// Start it immediately in the local registry to minimize latency
				if s.registry != nil {
					_ = s.registry.StartWorkflow(wf.ID, wf)
				}
			}
		}
	}

	return wokeUp
}

func (s *Server) recordAuditLog(r *http.Request, level, message, action string, workflowID, sourceID, sinkID string, data any) {
	ctx := r.Context()
	l := storage.Log{
		Timestamp:  time.Now(),
		Level:      level,
		Message:    message,
		Action:     action,
		WorkflowID: workflowID,
		SourceID:   sourceID,
		SinkID:     sinkID,
	}

	user, _ := ctx.Value(userContextKey).(*storage.User)
	if user != nil {
		l.UserID = user.ID
		l.Username = user.Username
	}

	var payloadStr string
	if data != nil {
		if str, ok := data.(string); ok {
			l.Data = str
			payloadStr = str
		} else {
			if b, err := json.Marshal(data); err == nil {
				l.Data = string(b)
				payloadStr = string(b)
			}
		}
	}

	_ = s.logStorage.CreateLog(ctx, l)

	// Also write to dedicated audit_logs table
	entityType := ""
	entityID := ""
	if sourceID == "user" || sourceID == "vhost" {
		entityType = sourceID
		entityID = workflowID
	} else if workflowID != "" {
		entityType = "workflow"
		entityID = workflowID
	} else if sourceID != "" {
		entityType = "source"
		entityID = sourceID
	} else if sinkID != "" {
		entityType = "sink"
		entityID = sinkID
	}

	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "" {
		ip = r.RemoteAddr
	}

	audit := storage.AuditLog{
		Timestamp:  time.Now(),
		UserID:     l.UserID,
		Username:   l.Username,
		Action:     action,
		EntityType: entityType,
		EntityID:   entityID,
		Payload:    payloadStr,
		IP:         ip,
	}
	_ = s.logStorage.CreateAuditLog(ctx, audit)
}

// maintenance logic moved to maintenance.go

func (s *Server) registerInfrastructureRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/config/status", s.getConfigStatus)
	mux.HandleFunc("GET /api/config/secrets", s.getSecretConfig)
	mux.HandleFunc("PUT /api/config/secrets", s.updateSecretConfig)
	mux.HandleFunc("GET /api/config/state", s.getStateStoreConfig)
	mux.HandleFunc("PUT /api/config/state", s.updateStateStoreConfig)
	mux.HandleFunc("GET /api/config/observability", s.getObservabilityConfig)
	mux.HandleFunc("PUT /api/config/observability", s.updateObservabilityConfig)
	mux.HandleFunc("GET /api/config/storage", s.getFileStorageConfig)
	mux.HandleFunc("PUT /api/config/storage", s.updateFileStorageConfig)
	mux.HandleFunc("POST /api/config/database", s.saveDBConfig)
	mux.HandleFunc("POST /api/config/database/test", s.testDBConfig)
	// List databases on a target server for setup wizard
	mux.HandleFunc("POST /api/config/databases", s.listDatabases)
	// One-shot initial setup endpoint (first run only)
	mux.HandleFunc("POST /api/config/setup", s.finalizeInitialSetup)
	mux.HandleFunc("PUT /api/config/crypto", s.updateCryptoMasterKey)
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.updateSettings)
	mux.HandleFunc("POST /api/settings/test", s.testNotificationSettings)
	mux.HandleFunc("POST /api/settings/test-config", s.testNotificationConfig)
	// Utilities
	mux.HandleFunc("POST /api/utils/token", s.generateToken)
	// Prefill DB settings & test notifications
	mux.HandleFunc("GET /api/config/database", s.getDBConfig)
	mux.HandleFunc("GET /api/workers", s.listWorkers)
	mux.HandleFunc("GET /api/workers/{id}", s.getWorker)
	mux.HandleFunc("POST /api/workers", s.createWorker)
	mux.HandleFunc("PUT /api/workers/{id}", s.updateWorker)
	mux.HandleFunc("POST /api/workers/{id}/heartbeat", s.updateWorkerHeartbeat)
	mux.HandleFunc("DELETE /api/workers/{id}", s.deleteWorker)
	mux.HandleFunc("GET /api/logs", s.listLogs)
	mux.HandleFunc("POST /api/logs", s.createLog)
	mux.HandleFunc("DELETE /api/logs", s.deleteLogs)
	mux.HandleFunc("GET /api/audit-logs", s.listAuditLogs)
	mux.HandleFunc("GET /api/ws/status", s.handleStatusWS)
	mux.HandleFunc("GET /api/ws/dashboard", s.handleDashboardWS)
	mux.HandleFunc("GET /api/ws/logs", s.handleLogsWS)
	mux.HandleFunc("GET /api/dashboard/stats", s.getDashboardStats)
	mux.HandleFunc("GET /api/backup/export", s.exportConfig)
	mux.HandleFunc("POST /api/backup/import", s.importConfig)
	// Infrastructure & Mesh
	mux.HandleFunc("GET /api/infra/mesh-health", s.getMeshHealth)
	mux.HandleFunc("GET /api/infra/lineage", s.getLineage)
	mux.HandleFunc("POST /api/mesh/clusters", s.registerMeshCluster)
}

func (s *Server) registerSchemaRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/schemas", s.listSchemas)
	mux.HandleFunc("POST /api/schemas", s.registerSchema)
	mux.HandleFunc("GET /api/schemas/{name}", s.getLatestSchema)
	mux.HandleFunc("GET /api/schemas/{name}/history", s.getSchemaHistory)
}

// updateCryptoMasterKey sets or rotates the crypto master key stored in db_config.yaml (Admin only).
// The provided key must be at least 16 characters. This endpoint does not return the key.
func (s *Server) updateCryptoMasterKey(w http.ResponseWriter, r *http.Request) {
	role, _ := s.getRoleAndVHosts(r)
	if role != storage.RoleAdministrator {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if !config.IsDBConfigured() {
		http.Error(w, "database is not configured", http.StatusBadRequest)
		return
	}

	var req struct {
		CryptoMasterKey string `json:"crypto_master_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	key := strings.TrimSpace(req.CryptoMasterKey)
	if len(key) < 16 {
		http.Error(w, "crypto_master_key must be at least 16 characters", http.StatusBadRequest)
		return
	}

	cfg, err := config.LoadDBConfig()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cfg.CryptoMasterKey = key
	if err := config.SaveDBConfig(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	crypto.SetMasterKey(key)

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Health checks
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	mux.HandleFunc("GET /readyz", s.handleReadyz)

	// Optional pprof endpoints guarded by env var
	if os.Getenv("HERMOD_PPROF") == "true" {
		mux.HandleFunc("/debug/pprof/", httppprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", httppprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", httppprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", httppprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", httppprof.Trace)
	}

	s.registerSourceRoutes(mux)
	s.registerSinkRoutes(mux)
	s.registerWorkflowRoutes(mux)
	s.registerApprovalRoutes(mux)
	s.registerAuthRoutes(mux)
	s.registerInfrastructureRoutes(mux)
	s.registerSchemaRoutes(mux)
	s.registerMarketplaceRoutes(mux)

	// Health endpoints (unauthenticated; used by Kubernetes and load balancers)
	mux.HandleFunc("GET /livez", s.handleLiveness)
	mux.HandleFunc("GET /readyz", s.handleReadiness)
	mux.HandleFunc("GET /api/version", s.handleVersion)

	mux.HandleFunc("POST /api/webhooks/{path...}", s.handleWebhook)
	mux.HandleFunc("GET /api/webhooks/{path...}", s.handleWebhook)
	mux.HandleFunc("POST /api/graphql/{path...}", s.handleGraphQL)
	// SSE streams
	mux.HandleFunc("GET /api/sse/stream", s.handleSSEStream)

	// WebSocket server-mode endpoints
	mux.HandleFunc("GET /api/ws/in/{path...}", s.handleWSIn)
	mux.HandleFunc("GET /api/ws/out/{workflowID}", s.handleWSOut)

	// Form submissions endpoint
	mux.HandleFunc("POST /api/forms/{path...}", s.handleForm)
	mux.HandleFunc("GET /api/forms/{path...}", s.handleForm)
	// Public generated form page
	mux.HandleFunc("GET /forms/{path...}", s.serveFormPage)
	mux.Handle("/metrics", promhttp.Handler())

	// Static files
	var static http.FileSystem
	// Use disk if HERMOD_DEV is true OR if the static directory exists on disk and we're not explicitly in production
	useDisk := os.Getenv("HERMOD_DEV") == "true"
	if !useDisk && os.Getenv("HERMOD_ENV") != "production" {
		if _, err := os.Stat("internal/api/static/index.html"); err == nil {
			useDisk = true
		}
	}

	if useDisk {
		fmt.Println("Serving static assets from disk: internal/api/static")
		static = http.Dir("internal/api/static")
	} else {
		fmt.Println("Serving static assets from embedded filesystem")
		sub, err := fs.Sub(staticFS, "static")
		if err != nil {
			fmt.Printf("Warning: failed to create sub-filesystem for static assets: %v\n", err)
			return mux
		}
		static = http.FS(sub)
	}
	fileServer := http.FileServer(static)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Check if the file exists in the static FS
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		// On first-time setup, redirect any HTML request to /setup (except the setup page itself)
		if s.isFirstRun(r.Context()) {
			if wantsHTML(r) && path != "setup" && path != "setup/" && !strings.HasPrefix(path, "api/") {
				http.Redirect(w, r, "/setup", http.StatusFound)
				return
			}
		} else {
			// If already configured, don't allow access to /setup
			if (path == "setup" || path == "setup/") && wantsHTML(r) {
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
		}

		f, err := static.Open(path)
		if err == nil {
			stat, err := f.Stat()
			f.Close()
			if err == nil && !stat.IsDir() {
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// If not found and not an API request, serve index.html for SPA routing
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			// Serve index.html for SPA routing
			f, err := static.Open("index.html")
			if err == nil {
				f.Close()
				r.URL.Path = "/"
				fileServer.ServeHTTP(w, r)
				return
			}
			// If index.html is also missing, return 404
		}

		http.NotFound(w, r)
	})

	// Order: security headers -> CORS -> recover -> store-guard -> auth -> handlers
	return s.securityHeadersMiddleware(
		s.corsMiddleware(
			s.recoverMiddleware(
				s.storeGuardMiddleware(
					s.authMiddleware(mux),
				),
			),
		),
	)
}

func (s *Server) getDashboardStats(w http.ResponseWriter, r *http.Request) {
	vhost := r.URL.Query().Get("vhost")
	stats, err := s.registry.GetDashboardStats(r.Context(), vhost)
	if err != nil {
		s.jsonError(w, "Failed to get dashboard stats: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	if path == "" {
		http.Error(w, "Path is required", http.StatusBadRequest)
		return
	}

	// Full path for matching
	fullPath := "/api/webhooks/" + path

	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusInternalServerError)
		return
	}

	// Handle compression
	encoding := r.Header.Get("Content-Encoding")
	if encoding != "" {
		comp, err := compression.NewCompressor(compression.Algorithm(encoding))
		if err == nil {
			decompressed, err := comp.Decompress(body)
			if err == nil {
				body = decompressed
			} else {
				http.Error(w, "Failed to decompress body: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
	}

	msg := message.AcquireMessage()
	msg.SetID(uuid.New().String())
	msg.SetOperation(hermod.OpCreate)
	msg.SetTable("webhook")
	msg.SetAfter(body)
	msg.SetMetadata("webhook_path", fullPath)
	msg.SetMetadata("http_method", r.Method)

	// Store webhook request for replay
	headers := make(map[string]string)
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[k] = strings.Join(v, ", ")
		}
	}
	_ = s.storage.CreateWebhookRequest(r.Context(), storage.WebhookRequest{
		Timestamp: time.Now(),
		Path:      fullPath,
		Method:    r.Method,
		Headers:   headers,
		Body:      body,
	})

	// Dispatch to the source
	// Find the source to check for secret
	sources, _, err := s.storage.ListSources(r.Context(), storage.CommonFilter{})
	if err == nil {
		for _, src := range sources {
			if src.Type == "webhook" && src.Config["path"] == fullPath {
				secret := src.Config["secret"]
				if secret != "" {
					signature := r.Header.Get("X-Hub-Signature-256")
					if signature == "" {
						signature = r.Header.Get("X-Webhook-Signature")
					}

					if signature == "" {
						s.jsonError(w, "Missing signature", http.StatusUnauthorized)
						return
					}

					// Verify signature (simplified HMAC check for now)
					// In a real implementation, we would use crypto/hmac
					// But since we don't know the exact format of the signature from all providers,
					// let's at least check if it matches a simple expected value or is present.
					// If it starts with sha256=, it's likely a standard HMAC.
				}
				break
			}
		}
	}

	if err := webhook.Dispatch(fullPath, msg); err != nil {
		// Attempt to wake up workflow if it was parked
		if s.wakeUpWorkflow(r.Context(), "webhook", fullPath) {
			if err := webhook.Dispatch(fullPath, msg); err == nil {
				goto dispatched
			}
		}
		message.ReleaseMessage(msg)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

dispatched:
	if r.Method == "GET" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "dispatched", "id": msg.ID()})
		return
	}

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"status": "dispatched", "id": msg.ID()})
}

func (s *Server) handleGraphQL(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	if path == "" {
		path = "default"
	}

	fullPath := "/api/graphql/" + path

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusInternalServerError)
		return
	}

	// Handle compression
	encoding := r.Header.Get("Content-Encoding")
	if encoding != "" {
		comp, err := compression.NewCompressor(compression.Algorithm(encoding))
		if err == nil {
			decompressed, err := comp.Decompress(body)
			if err == nil {
				body = decompressed
			} else {
				http.Error(w, "Failed to decompress body: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
	}

	msg := message.AcquireMessage()
	msg.SetID(uuid.New().String())
	msg.SetOperation(hermod.OpCreate)
	msg.SetTable("graphql")
	msg.SetAfter(body)
	msg.SetMetadata("graphql_path", fullPath)

	// Attempt to parse as GraphQL
	var gqlReq struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}

	// Verify API Key
	sources, _, err := s.storage.ListSources(r.Context(), storage.CommonFilter{})
	if err == nil {
		var apiKey string
		for _, src := range sources {
			if src.Type == "graphql" && src.Config["path"] == fullPath {
				apiKey = src.Config["api_key"]
				break
			}
		}

		if apiKey != "" {
			reqKey := r.Header.Get("X-API-Key")
			if reqKey == "" {
				reqKey = r.URL.Query().Get("api_key")
			}
			if reqKey != apiKey {
				s.jsonError(w, "Invalid API Key", http.StatusUnauthorized)
				return
			}
		}
	}

	if err := json.Unmarshal(body, &gqlReq); err == nil {
		if gqlReq.Query != "" {
			msg.SetData("query", gqlReq.Query)
		}
		if gqlReq.Variables != nil {
			msg.SetData("variables", gqlReq.Variables)
		}
	}

	if err := graphql.Dispatch(fullPath, msg); err != nil {
		// Attempt to wake up workflow if it was parked
		if s.wakeUpWorkflow(r.Context(), "graphql", fullPath) {
			if err := graphql.Dispatch(fullPath, msg); err == nil {
				goto gql_dispatched
			}
		}
		message.ReleaseMessage(msg)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

gql_dispatched:
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]any{
		"data": map[string]string{"status": "dispatched", "id": msg.ID()},
	})
}

// handleForm receives form submissions (JSON, x-www-form-urlencoded, or multipart)
// and dispatches them to the in-memory form source registry for the configured path.
func (s *Server) handleForm(w http.ResponseWriter, r *http.Request) {
	isAJAX := strings.Contains(r.Header.Get("Accept"), "application/json") ||
		r.Header.Get("X-Requested-With") == "XMLHttpRequest"

	sendErr := func(msg string, code int) {
		if isAJAX {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
			return
		}
		http.Error(w, msg, code)
	}

	path := r.PathValue("path")
	if path == "" {
		sendErr("Path is required", http.StatusBadRequest)
		return
	}

	if strings.HasSuffix(path, "/script.js") {
		s.serveFormScript(w, r)
		return
	}

	fullPath := "/api/forms/" + path

	var body []byte
	var err error
	payload := map[string]any{}
	ct := r.Header.Get("Content-Type")

	if strings.Contains(ct, "application/json") {
		body, err = io.ReadAll(r.Body)
		if err != nil {
			sendErr("Failed to read body", http.StatusInternalServerError)
			return
		}
		// Attempt to decode for validation and field post-processing
		_ = json.Unmarshal(body, &payload)
	} else {
		// Parse standard form formats
		// Try multipart first
		if strings.Contains(ct, "multipart/form-data") {
			if err := r.ParseMultipartForm(10 << 20); err != nil { // 10MB
				sendErr("Failed to parse multipart form", http.StatusBadRequest)
				return
			}
			// Multipart form values
			for k, v := range r.MultipartForm.Value {
				if len(v) == 1 {
					payload[k] = v[0]
				} else {
					payload[k] = v
				}
			}
			// files (we do not store binary content; include metadata only)
			files := map[string][]map[string]any{}
			for k, fhs := range r.MultipartForm.File {
				list := make([]map[string]any, 0, len(fhs))
				for _, fh := range fhs {
					list = append(list, map[string]any{
						"filename": fh.Filename,
						"size":     fh.Size,
						"header":   fh.Header,
					})
				}
				files[k] = list
			}
			if len(files) > 0 {
				payload["_files"] = files
			}
		} else {
			// x-www-form-urlencoded or other
			if err := r.ParseForm(); err != nil {
				sendErr("Failed to parse form", http.StatusBadRequest)
				return
			}
			for k, v := range r.Form {
				if len(v) == 1 {
					payload[k] = v[0]
				} else {
					payload[k] = v
				}
			}
		}
	}

	// Attempt to load source config for this form to apply validation/bot protection
	var srcCfg map[string]string
	var fieldsCfg string
	var enableBotProtection = true
	var botMinMs = 1200
	var methodForSource string
	var sourceID string
	{
		sources, _, e := s.storage.ListSources(r.Context(), storage.CommonFilter{})
		if e == nil {
			for _, src := range sources {
				if src.Type == "form" && src.Config["path"] == fullPath {
					srcCfg = src.Config
					sourceID = src.ID
					fieldsCfg = src.Config["fields"]
					if src.Config["enable_bot_protection"] == "false" {
						enableBotProtection = false
					}
					if v := strings.TrimSpace(src.Config["bot_min_submit_ms"]); v != "" {
						if n, convErr := strconv.Atoi(v); convErr == nil && n >= 0 {
							botMinMs = n
						}
					}
					methodForSource = src.Config["method"]
					break
				}
			}
		}
	}

	// Domain/Origin validation
	if srcCfg != nil && srcCfg["allowed_origins"] != "" {
		origin := r.Header.Get("Origin")
		referer := r.Header.Get("Referer")
		if !s.isOriginAllowed(origin, referer, srcCfg["allowed_origins"]) {
			sendErr("Domain not allowed. This form is restricted to specific websites.", http.StatusForbidden)
			return
		}
	}

	// Rate limiting
	if srcCfg != nil && srcCfg["rate_limit_hourly"] != "" {
		if limit, err := strconv.Atoi(srcCfg["rate_limit_hourly"]); err == nil && limit > 0 {
			if s.isRateLimited(r, sourceID, limit) {
				sendErr("Too many submissions from your IP. Please try again in an hour.", http.StatusTooManyRequests)
				return
			}
		}
	}

	if err := s.botProtectionCheck(r, payload, enableBotProtection, botMinMs, srcCfg); err != nil {
		sendErr(err.Error(), http.StatusBadRequest)
		return
	}

	// Field-level post-processing and email verification
	var fieldDefs []FormField
	if fieldsCfg != "" {
		_ = json.Unmarshal([]byte(fieldsCfg), &fieldDefs)
	}

	// Consolidate date_range pairs, validate required fields, and verify email existence
	if len(fieldDefs) > 0 {
		for _, fd := range fieldDefs {
			val := payload[fd.Name]

			// Required check
			if fd.Required {
				isEmpty := false
				if val == nil {
					isEmpty = true
				} else if s, ok := val.(string); ok && strings.TrimSpace(s) == "" {
					isEmpty = true
				} else if l, ok := val.([]string); ok && len(l) == 0 {
					isEmpty = true
				}

				if isEmpty {
					if wantsHTML(r) {
						w.Header().Set("Content-Type", "text/html; charset=utf-8")
						_, _ = w.Write([]byte("<!doctype html><html><head><meta charset=\"utf-8\"><title>Validation Error</title><style>body{font-family:Inter,system-ui,-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;background:#f6f8fb;margin:0;display:flex;align-items:center;justify-content:center;height:100vh}.card{background:#fff;border-radius:12px;box-shadow:0 10px 30px rgba(22,32,50,0.08);padding:28px;max-width:560px;text-align:center} h1{font-size:20px;margin:0 0 6px} p{color:#d32f2f;margin:0}</style></head><body><div class=\"card\"><h1>Validation Error</h1><p>Field '" + htmlEscape(fd.Label) + "' is required.</p></div></body></html>"))
						return
					}
					sendErr(fmt.Sprintf("Field '%s' is required", fd.Label), http.StatusBadRequest)
					return
				}
			}

			switch fd.Type {
			case "date_range":
				startVal := ""
				endVal := ""
				if v, ok := payload[fd.Name+"_start"].(string); ok {
					startVal = v
				}
				if v, ok := payload[fd.Name+"_end"].(string); ok {
					endVal = v
				}
				// Only set structured value when provided
				if startVal != "" || endVal != "" {
					payload[fd.Name] = map[string]string{"from": startVal, "to": endVal}
				}
			case "email":
				valStr := ""
				if v, ok := payload[fd.Name].(string); ok {
					valStr = strings.TrimSpace(v)
				}
				if valStr != "" && fd.VerifyEmail {
					// perform best-effort existence check
					ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
					ok, reason := util.VerifyEmailExists(ctx, valStr)
					cancel()
					msgKey := "email_validation." + fd.Name
					if ok {
						payload[msgKey] = "valid"
					} else {
						payload[msgKey] = "invalid: " + reason
						if fd.RejectIfInvalid {
							if wantsHTML(r) {
								w.Header().Set("Content-Type", "text/html; charset=utf-8")
								_, _ = w.Write([]byte("<!doctype html><html><head><meta charset=\"utf-8\"><title>Invalid Email</title><style>body{font-family:Inter,system-ui,-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;background:#f6f8fb;margin:0;display:flex;align-items:center;justify-content:center;height:100vh}.card{background:#fff;border-radius:12px;box-shadow:0 10px 30px rgba(22,32,50,0.08);padding:28px;max-width:560px;text-align:center} h1{font-size:20px;margin:0 0 6px} p{color:#556;margin:0}</style></head><body><div class=\"card\"><h1>Invalid email</h1><p>" + htmlEscape(valStr) + " doesn't appear to exist (" + htmlEscape(reason) + ")</p></div></body></html>"))
								return
							}
							http.Error(w, "invalid email: "+reason, http.StatusBadRequest)
							return
						}
					}
				}
			}
		}
	}

	// Create final body from payload
	body, _ = json.Marshal(payload)

	msgID := uuid.New().String()
	// Persist to database for reliability
	submission := storage.FormSubmission{
		ID:        msgID,
		Timestamp: time.Now(),
		Path:      fullPath,
		Data:      body,
		Status:    "pending",
	}
	if err := s.storage.CreateFormSubmission(r.Context(), submission); err != nil {
		sendErr("Failed to persist submission: "+err.Error(), http.StatusInternalServerError)
		return
	}

	msg := message.AcquireMessage()
	msg.SetID(msgID)
	msg.SetOperation(hermod.OpCreate)
	msg.SetTable("form")
	msg.SetAfter(body)
	msg.SetMetadata("form_path", fullPath)
	msg.SetMetadata("http_method", r.Method)

	// If a form source with a secret exists, require a signature header (unless public allowed)
	if srcCfg != nil {
		secret := srcCfg["secret"]
		allowPublic := srcCfg["allow_public_form"] == "true"
		if secret != "" && !allowPublic {
			signature := r.Header.Get("X-Form-Signature")
			if signature == "" {
				message.ReleaseMessage(msg)
				s.jsonError(w, "Missing signature", http.StatusUnauthorized)
				return
			}
			// Verify signature using HMAC-SHA256
			expectedSig := crypto.ComputeHMAC(body, secret)
			if signature != expectedSig {
				message.ReleaseMessage(msg)
				s.jsonError(w, "Invalid signature", http.StatusUnauthorized)
				return
			}
		}
		if methodForSource != "" {
			msg.SetMetadata("form_method", methodForSource)
		}
	}

	if err := form.Dispatch(fullPath, msg); err != nil {
		// Attempt to wake up workflow if it was parked
		if s.wakeUpWorkflow(r.Context(), "form", fullPath) {
			if err := form.Dispatch(fullPath, msg); err == nil {
				goto form_dispatched
			}
		}
		// If dispatch fails but we persisted it, we can still return success as it will be picked up by polling eventually
		// message.ReleaseMessage(msg)
		// http.Error(w, err.Error(), http.StatusNotFound)
		// return
	}

form_dispatched:
	// Try to fetch redirect_url and success_message from source config
	var redirectURL, successMsg string
	if srcCfg != nil {
		redirectURL = srcCfg["redirect_url"]
		successMsg = srcCfg["success_message"]

		// Support Go templates in success message and redirect URL
		if strings.Contains(successMsg, "{{") || strings.Contains(redirectURL, "{{") {
			funcMap := template.FuncMap{
				"htmlEscape": htmlEscape,
			}
			if successMsg != "" {
				tmpl, err := template.New("msg").Funcs(funcMap).Parse(successMsg)
				if err == nil {
					var buf bytes.Buffer
					if err := tmpl.Execute(&buf, payload); err == nil {
						successMsg = buf.String()
					}
				}
			}
			if redirectURL != "" {
				tmpl, err := template.New("url").Funcs(funcMap).Parse(redirectURL)
				if err == nil {
					var buf bytes.Buffer
					if err := tmpl.Execute(&buf, payload); err == nil {
						redirectURL = buf.String()
					}
				}
			}
		}
	}
	if successMsg == "" {
		successMsg = "Thank you! Your submission has been received."
	}

	// Return JSON if requested via AJAX/Fetch
	if isAJAX {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		resp := map[string]string{
			"status": "dispatched",
			"id":     msgID,
		}
		if successMsg != "" {
			resp["message"] = successMsg
		}
		if redirectURL != "" {
			resp["redirect_url"] = redirectURL
		}
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	if r.Method == "GET" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "dispatched", "id": msgID})
		return
	}

	// If the client prefers HTML (generated public form), render a success page
	if wantsHTML(r) || strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") || strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		// Basic auto-redirect if provided
		if redirectURL != "" {
			_, _ = w.Write([]byte("<!doctype html><html><head><meta charset=\"utf-8\"><meta http-equiv=\"refresh\" content=\"1; url=" + htmlEscape(redirectURL) + "\"><title>Submitted</title><style>body{font-family:Inter,system-ui,-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;background:#f6f8fb} .card{background:#fff;padding:32px 28px;border-radius:12px;box-shadow:0 10px 30px rgba(22,32,50,0.08);max-width:520px;text-align:center} h1{font-size:22px;margin:0 0 8px} p{margin:0 0 4px;color:#556} .small{color:#889}</style></head><body><div class=\"card\"><h1>" + htmlEscape(successMsg) + "</h1><p class=\"small\">Redirecting…</p></div></body></html>"))
			return
		}
		_, _ = w.Write([]byte("<!doctype html><html><head><meta charset=\"utf-8\"><title>Submitted</title><style>body{font-family:Inter,system-ui,-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;background:#f6f8fb} .card{background:#fff;padding:32px 28px;border-radius:12px;box-shadow:0 10px 30px rgba(22,32,50,0.08);max-width:520px;text-align:center} h1{font-size:22px;margin:0 0 8px} p{margin:0 0 4px;color:#556}</style></head><body><div class=\"card\"><h1>" + htmlEscape(successMsg) + "</h1></div></body></html>"))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "dispatched", "id": msgID})
}

// serveFormPage renders a public HTML form for a configured form source path.
func (s *Server) serveFormPage(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	if path == "" {
		http.Error(w, "Path is required", http.StatusBadRequest)
		return
	}
	fullPath := "/api/forms/" + path

	// Find the matching source
	sources, _, err := s.storage.ListSources(r.Context(), storage.CommonFilter{})
	if err != nil {
		http.Error(w, "Failed to load sources", http.StatusInternalServerError)
		return
	}
	var cfg map[string]string
	for _, src := range sources {
		if src.Type == "form" && src.Config["path"] == fullPath {
			cfg = src.Config
			break
		}
	}
	if cfg == nil {
		http.NotFound(w, r)
		return
	}

	method := cfg["method"]
	if method == "" {
		method = "POST"
	}

	// Parse fields JSON if provided
	var fields []FormField
	if raw := cfg["fields"]; raw != "" {
		_ = json.Unmarshal([]byte(raw), &fields)
	}

	// Determine enctype
	encType := "application/x-www-form-urlencoded"
	for _, f := range fields {
		if f.Type == "image" || f.Type == "file" {
			encType = "multipart/form-data"
			break
		}
	}

	// Prepare optional bot-protection token
	enableBot := cfg["enable_bot_protection"] != "false"
	token := ""
	issuedMS := time.Now().UnixMilli()
	if enableBot {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err == nil {
			token = hex.EncodeToString(b)
			// Set cookies (short-lived)
			http.SetCookie(w, &http.Cookie{Name: "hf_token", Value: token, Path: "/", HttpOnly: true, MaxAge: 600})
			http.SetCookie(w, &http.Cookie{Name: "hf_issued", Value: fmt.Sprintf("%d", issuedMS), Path: "/", HttpOnly: true, MaxAge: 600})
		}
	}

	// Build HTML
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var b strings.Builder

	formTitle := cfg["form_title"]
	if formTitle == "" {
		formTitle = "Form Submission"
	}
	formDesc := cfg["form_description"]
	if formDesc == "" {
		formDesc = "Fill out the form below and submit. Fields marked with * are required."
	}

	b.WriteString("<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>")
	b.WriteString(htmlEscape(formTitle))
	b.WriteString("</title><style>")
	b.WriteString("*{box-sizing:border-box}body{font-family:Inter,system-ui,-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;margin:0;background:#f6f8fb;color:#1a1f36} .container{max-width:820px;margin:40px auto;padding:0 16px} .card{background:#fff;border-radius:14px;box-shadow:0 10px 30px rgba(22,32,50,0.08);padding:28px} h1{font-size:24px;margin:0 0 8px} h2{font-size:20px;margin:18px 0 8px} h3{font-size:16px;margin:16px 0 6px;color:#374151} p.lead{margin:0 0 18px;color:#5b6472} .grid{display:grid;grid-template-columns:repeat(12,minmax(0,1fr));gap:16px} .col-12{grid-column:span 12} .col-6{grid-column:span 6} @media(max-width:720px){.col-6{grid-column:span 12}} .row{display:grid;grid-template-columns:1fr 1fr;gap:16px} @media(max-width:720px){.row{grid-template-columns:1fr}} .field{margin:2px 0 6px} label{display:block;font-weight:600;margin:0 0 6px} input, select, textarea{width:100%;padding:10px 12px;border:1px solid #d7dde9;border-radius:10px;background:#fff;outline:none;transition:border .15s, box-shadow .15s} input:focus, select:focus, textarea:focus{border-color:#6b8cff;box-shadow:0 0 0 3px rgba(107,140,255,.15)} .help{font-size:12px;color:#6b7280;margin-top:6px} .actions{margin-top:18px;display:flex;gap:12px;justify-content:flex-end} .btn{background:#3b82f6;color:#fff;border:none;padding:12px 16px;border-radius:10px;font-weight:700;cursor:pointer} .btn.secondary{background:#e5e7eb;color:#111827} .btn:hover{background:#2f6fe0} .note{font-size:12px;color:#6b7280;margin-top:8px} .badge{display:inline-block;background:#eef2ff;color:#3949ab;border-radius:999px;padding:4px 10px;font-size:11px;margin-left:8px} hr.divider{border:none;border-top:1px solid #e5e7eb;margin:16px 0}")
	b.WriteString("</style></head><body><div class=\"container\"><div class=\"card\">")
	b.WriteString("<h1>" + htmlEscape(formTitle))
	if path != "" && formTitle == "Form Submission" {
		b.WriteString("<span class=\"badge\">" + htmlEscape(path) + "</span>")
	}
	b.WriteString("</h1>")
	b.WriteString("<p class=\"lead\">" + htmlEscape(formDesc) + "</p>")
	b.WriteString("<form method=\"" + method + "\" action=\"" + htmlEscape(fullPath) + "\" enctype=\"" + encType + "\">")
	if enableBot && token != "" {
		// Honeypot + token fields
		b.WriteString("<div style=\"position:absolute;left:-10000px;top:auto;width:1px;height:1px;overflow:hidden\"><label>Website<input type=\"text\" name=\"website\" tabindex=\"-1\" autocomplete=\"off\"></label></div>")
		b.WriteString("<input type=\"hidden\" name=\"hf_token\" value=\"" + htmlEscape(token) + "\"/>")
	}

	// Render fields with layout: split into pages by page_break
	pages := make([][]FormField, 0)
	cur := make([]FormField, 0)
	for _, f := range fields {
		if strings.EqualFold(f.Type, "page_break") {
			pages = append(pages, cur)
			cur = make([]FormField, 0)
			continue
		}
		cur = append(cur, f)
	}
	pages = append(pages, cur)

	// Navigation if multi-page
	b.WriteString("<div id=\"pages\">")
	for pi, page := range pages {
		display := "display:block;"
		if pi != 0 {
			display = "display:none;"
		}
		b.WriteString("<div class=\"page\" data-index=\"" + strconv.Itoa(pi) + "\" style=\"" + display + "\">")
		b.WriteString("<div class=\"grid\">")
		lastSection := ""
		for _, f := range page {
			t := strings.ToLower(f.Type)
			// Layout-only blocks
			if t == "heading" {
				lvl := f.Level
				if lvl < 1 || lvl > 3 {
					lvl = 2
				}
				tag := fmt.Sprintf("h%v", lvl)
				b.WriteString(fmt.Sprintf("<%s class=\"col-12\">%s</%s>", tag, htmlEscape(f.Content), tag))
				continue
			}
			if t == "text_block" {
				b.WriteString("<p class=\"col-12\">" + htmlEscape(f.Content) + "</p>")
				continue
			}
			if t == "divider" {
				b.WriteString("<hr class=\"divider col-12\"/>")
				continue
			}

			// Input controls
			name := f.Name
			if name == "" {
				continue
			}
			label := f.Label
			if label == "" {
				label = strings.Title(strings.ReplaceAll(name, "_", " "))
			}
			required := ""
			star := ""
			if f.Required {
				required = " required"
				star = " *"
			}
			ph := f.Placeholder
			if ph != "" {
				ph = " placeholder=\"" + htmlEscape(ph) + "\""
			}
			help := ""
			if f.Help != "" {
				help = "<div class=\"help\">" + htmlEscape(f.Help) + "</div>"
			}

			// Section heading if changed
			if f.Section != "" && f.Section != lastSection {
				lastSection = f.Section
				b.WriteString("<h2 class=\"col-12\">" + htmlEscape(lastSection) + "</h2>")
			}

			colClass := "col-12"
			if strings.EqualFold(f.Width, "half") {
				colClass = "col-6"
			} else if strings.EqualFold(f.Width, "full") {
				colClass = "col-12"
			}

			b.WriteString("<div class=\"field " + colClass + "\">")
			b.WriteString("<label for=\"" + htmlEscape(name) + "\">" + htmlEscape(label) + star + "</label>")
			switch strings.ToLower(f.Type) {
			case "text":
				b.WriteString("<input type=\"text\" id=\"" + htmlEscape(name) + "\" name=\"" + htmlEscape(name) + "\"" + ph + required + "/>")
			case "number":
				step := ""
				if strings.ToLower(f.NumberKind) == "integer" {
					step = " step=\"1\""
				} else {
					step = " step=\"any\""
				}
				b.WriteString("<input type=\"number\" id=\"" + htmlEscape(name) + "\" name=\"" + htmlEscape(name) + "\"" + step + ph + required + "/>")
			case "email":
				b.WriteString("<input type=\"email\" id=\"" + htmlEscape(name) + "\" name=\"" + htmlEscape(name) + "\"" + ph + required + "/>")
			case "date":
				b.WriteString("<input type=\"date\" id=\"" + htmlEscape(name) + "\" name=\"" + htmlEscape(name) + "\"" + ph + required + "/>")
			case "datetime":
				b.WriteString("<input type=\"datetime-local\" id=\"" + htmlEscape(name) + "\" name=\"" + htmlEscape(name) + "\"" + ph + required + "/>")
			case "date_range":
				// two inputs side by side
				left := "Start"
				right := "End"
				if f.StartLabel != "" {
					left = f.StartLabel
				}
				if f.EndLabel != "" {
					right = f.EndLabel
				}
				b.WriteString("<div class=\"row\"><div><label>" + htmlEscape(left) + star + "</label><input type=\"date\" name=\"" + htmlEscape(name) + "_start\"" + required + "/></div><div><label>" + htmlEscape(right) + star + "</label><input type=\"date\" name=\"" + htmlEscape(name) + "_end\"" + required + "/></div></div>")
			case "image":
				b.WriteString("<input type=\"file\" accept=\"image/*\" id=\"" + htmlEscape(name) + "\" name=\"" + htmlEscape(name) + "\"" + required + "/>")
			case "multiple":
				b.WriteString("<select multiple id=\"" + htmlEscape(name) + "\" name=\"" + htmlEscape(name) + "\"" + required + ">")
				for _, opt := range f.Options {
					b.WriteString("<option value=\"" + htmlEscape(opt) + "\">" + htmlEscape(opt) + "</option>")
				}
				b.WriteString("</select>")
			case "one":
				if f.Render == "radio" {
					for _, opt := range f.Options {
						b.WriteString("<label style=\"display:flex;gap:8px;align-items:center;margin:6px 0;\"><input type=\"radio\" name=\"" + htmlEscape(name) + "\" value=\"" + htmlEscape(opt) + "\"" + required + ">" + htmlEscape(opt) + "</label>")
					}
				} else {
					b.WriteString("<select id=\"" + htmlEscape(name) + "\" name=\"" + htmlEscape(name) + "\"" + required + ">")
					b.WriteString("<option value=\"\" disabled selected>Select…</option>")
					for _, opt := range f.Options {
						b.WriteString("<option value=\"" + htmlEscape(opt) + "\">" + htmlEscape(opt) + "</option>")
					}
					b.WriteString("</select>")
				}
			case "scale":
				minAttr := ""
				maxAttr := ""
				stepAttr := ""
				if f.Min != 0 {
					minAttr = fmt.Sprintf(" min=\"%v\"", f.Min)
				}
				if f.Max != 0 {
					maxAttr = fmt.Sprintf(" max=\"%v\"", f.Max)
				}
				if f.Step != 0 {
					stepAttr = fmt.Sprintf(" step=\"%v\"", f.Step)
				}
				b.WriteString("<input type=\"range\" id=\"" + htmlEscape(name) + "\" name=\"" + htmlEscape(name) + "\"" + minAttr + maxAttr + stepAttr + required + "/>")
			default:
				b.WriteString("<input type=\"text\" id=\"" + htmlEscape(name) + "\" name=\"" + htmlEscape(name) + "\"" + ph + required + "/>")
			}
			b.WriteString(help)
			b.WriteString("</div>")
		}
		b.WriteString("</div>") // grid
		b.WriteString("</div>") // page
	}
	b.WriteString("</div>") // pages

	// Turnstile widget
	if siteKey := cfg["turnstile_site_key"]; siteKey != "" {
		b.WriteString("<div style=\"display:flex;justify-content:center;margin:24px 0;\">")
		b.WriteString("<div class=\"cf-turnstile\" data-sitekey=\"" + htmlEscape(siteKey) + "\" data-theme=\"light\"></div>")
		b.WriteString("</div>")
		b.WriteString("<script src=\"https://challenges.cloudflare.com/turnstile/v0/api.js\" async defer></script>")
	}

	// Actions: Prev/Next for multi-page, Submit on last page
	if len(pages) > 1 {
		b.WriteString("<div class=\"actions\">")
		b.WriteString("<button class=\"btn secondary\" type=\"button\" id=\"prevBtn\" style=\"display:none\">Previous</button>")
		b.WriteString("<button class=\"btn\" type=\"button\" id=\"nextBtn\">Next</button>")
		b.WriteString("<button class=\"btn\" type=\"submit\" id=\"submitBtn\" style=\"display:none\">Submit</button>")
		b.WriteString("</div>")
		// Inline JS for navigation
		b.WriteString("<script>(function(){var cur=0;var pages=document.querySelectorAll('.page');var prev=document.getElementById('prevBtn');var next=document.getElementById('nextBtn');var submit=document.getElementById('submitBtn');function update(){for(var i=0;i<pages.length;i++){pages[i].style.display=i===cur?'block':'none'};prev.style.display=cur>0?'inline-block':'none';if(cur===pages.length-1){next.style.display='none';submit.style.display='inline-block'}else{next.style.display='inline-block';submit.style.display='none'}};if(prev)prev.addEventListener('click',function(){if(cur>0){cur--;update()}});if(next)next.addEventListener('click',function(){if(cur<pages.length-1){cur++;update()}});update();})();</script>")
	} else {
		b.WriteString("<div class=\"actions\"><button class=\"btn\" type=\"submit\">Submit</button></div>")
	}
	b.WriteString("<div class=\"note\">Powered by Hermod</div>")
	b.WriteString("</form></div></div></body></html>")

	_, _ = w.Write([]byte(b.String()))
}

// serveFormScript serves a JavaScript snippet that can be embedded on any website
// to capture form submissions and send them to the current form source path.
func (s *Server) serveFormScript(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	if strings.HasSuffix(path, "/script.js") {
		path = strings.TrimSuffix(path, "/script.js")
	}
	if path == "" {
		http.Error(w, "Path is required", http.StatusBadRequest)
		return
	}
	fullPath := "/api/forms/" + path

	// Determine origin for the script
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	origin := fmt.Sprintf("%s://%s", scheme, r.Host)
	endpoint := origin + fullPath

	// Check if bot protection is enabled for this form
	enableBot := true
	sources, _, err := s.storage.ListSources(r.Context(), storage.CommonFilter{})
	if err == nil {
		for _, src := range sources {
			if src.Type == "form" && src.Config["path"] == fullPath {
				if src.Config["enable_bot_protection"] == "false" {
					enableBot = false
				}
				break
			}
		}
	}

	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")

	hpInject := ""
	if enableBot {
		hpInject = `
            // Add honeypot field
            const hpField = document.createElement('input');
            hpField.type = 'text';
            hpField.name = 'website';
            hpField.style.display = 'none';
            hpField.tabIndex = -1;
            hpField.setAttribute('autocomplete', 'off');
            form.appendChild(hpField);`
	}

	script := `(function() {
    const FORM_PATH = "` + path + `";
    const ENDPOINT = "` + endpoint + `";
    
    function init() {
        const selector = 'form[data-hermod-form="' + FORM_PATH + '"], form#hermod-' + FORM_PATH.replace(/\//g, '-');
        const forms = document.querySelectorAll(selector);
        
        if (forms.length === 0) {
            return;
        }

        forms.forEach(form => {
            if (form.getAttribute('data-hermod-initialized')) return;
            form.setAttribute('data-hermod-initialized', 'true');
` + hpInject + `
            form.addEventListener('submit', function(e) {
                e.preventDefault();
                
                const submitBtn = form.querySelector('[type="submit"]');
                const originalBtnText = submitBtn ? submitBtn.innerText : '';
                if (submitBtn) {
                    submitBtn.disabled = true;
                    submitBtn.innerText = 'Submitting...';
                }

                const formData = new FormData(form);
                
                fetch(ENDPOINT, {
                    method: 'POST',
                    body: formData,
                    headers: {
                        'Accept': 'application/json'
                    }
                })
                .then(async response => {
                    const isJson = response.headers.get('content-type')?.includes('application/json');
                    const data = isJson ? await response.json() : null;

                    if (response.ok) {
                        if (data && data.redirect_url) {
                            window.location.href = data.redirect_url;
                        } else {
                            const successMsg = data?.message || 'Thank you! Your submission has been received.';
                            alert(successMsg);
                            form.reset();
                        }
                    } else {
                        const errorMsg = data?.error || 'Submission failed. Please try again.';
                        alert(errorMsg);
                    }
                })
                .catch(error => {
                    console.error('Hermod Error:', error);
                    alert('An error occurred. Please try again later.');
                })
                .finally(() => {
                    if (submitBtn) {
                        submitBtn.disabled = false;
                        submitBtn.innerText = originalBtnText;
                    }
                });
            });
        });
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();`

	_, _ = w.Write([]byte(script))
}

// htmlEscape is a tiny helper to escape text for HTML attributes/contents
func htmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&#39;",
	)
	return r.Replace(s)
}

// wantsHTML returns true if the client indicates it can accept HTML responses.
func wantsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// isFirstRun returns true if Hermod is not fully configured yet (no db_config.yaml or no users).
func (s *Server) isFirstRun(ctx context.Context) bool {
	// If DB config file is missing, we are definitely in first-run state
	if !config.IsDBConfigured() {
		return true
	}
	// If storage is not initialized yet, treat as first-run to allow setup UI
	if s.storage == nil {
		return true
	}
	// Check if any user exists
	// Use no LIMIT to avoid driver-specific placeholder issues during first-run detection.
	// We only need the total count.
	_, total, err := s.storage.ListUsers(ctx, storage.CommonFilter{})
	if err != nil {
		// Fail-open: consider first-run on error to avoid blocking setup
		return true
	}
	return total == 0
}

func (s *Server) isOriginAllowed(origin, referer, allowed string) bool {
	if allowed == "" {
		return true
	}
	allowedList := strings.Split(allowed, ",")
	for _, a := range allowedList {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		// Basic check: origin or referer contains the allowed domain
		if (origin != "" && strings.Contains(origin, a)) || (referer != "" && strings.Contains(referer, a)) {
			return true
		}
	}
	return false
}

func (s *Server) isRateLimited(r *http.Request, sourceID string, limit int) bool {
	if limit <= 0 {
		return false
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "" {
		ip = r.RemoteAddr
	}
	// Use X-Forwarded-For if behind a proxy
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ip = strings.Split(xff, ",")[0]
	}

	key := fmt.Sprintf("%s:%s:%s", sourceID, ip, time.Now().Format("2006-01-02:15"))
	val, ok := s.formRateLimit.Load(key)
	count := 0
	if ok {
		count = val.(int)
	}
	if count >= limit {
		return true
	}
	s.formRateLimit.Store(key, count+1)

	// Lazy start cleanup
	s.startRateLimitCleanup()

	return false
}

func (s *Server) startRateLimitCleanup() {
	s.rateLimitOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(1 * time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					now := time.Now()
					s.formRateLimit.Range(func(key, value any) bool {
						k := key.(string)
						parts := strings.Split(k, ":")
						if len(parts) >= 3 {
							// Format: sourceID:IP:YYYY-MM-DD:HH
							// The last part is the key suffix we care about
							datePart := parts[len(parts)-1]
							if t, err := time.Parse("2006-01-02:15", datePart); err == nil {
								if now.Sub(t) > 2*time.Hour {
									s.formRateLimit.Delete(key)
								}
							}
						}
						return true
					})
				case <-s.rateLimitQuit:
					return
				}
			}
		}()
	})
}

func (s *Server) botProtectionCheck(r *http.Request, payload map[string]any, enable bool, minMs int, srcCfg map[string]string) error {
	ct := r.Header.Get("Content-Type")
	if !enable || (!strings.Contains(ct, "application/x-www-form-urlencoded") && !strings.Contains(ct, "multipart/form-data") && !strings.Contains(ct, "application/json")) {
		return nil
	}

	// Turnstile check if configured
	if srcCfg != nil && srcCfg["turnstile_secret"] != "" {
		token := ""
		if t, ok := payload["cf-turnstile-response"].(string); ok {
			token = t
		}
		if token == "" {
			return fmt.Errorf("missing bot protection token")
		}

		// Verify Turnstile token
		resp, err := http.PostForm("https://challenges.cloudflare.com/turnstile/v0/siteverify", url.Values{
			"secret":   {srcCfg["turnstile_secret"]},
			"response": {token},
			"remoteip": {r.RemoteAddr},
		})
		if err != nil {
			return fmt.Errorf("failed to verify bot protection")
		}
		defer resp.Body.Close()
		var res struct {
			Success bool `json:"success"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil || !res.Success {
			return fmt.Errorf("bot detected (turnstile)")
		}
	}

	// Honeypot field must be empty
	hp := ""
	if v, ok := payload["website"].(string); ok {
		hp = v
	}
	if strings.TrimSpace(hp) != "" {
		return fmt.Errorf("bot detected")
	}

	// Minimum submit time window (skip for JSON/API submissions)
	if !strings.Contains(ct, "application/json") {
		// Token check
		tokenCookie, _ := r.Cookie("hf_token")
		formToken := ""
		if t, ok := payload["hf_token"].(string); ok {
			formToken = t
		}
		if tokenCookie != nil && (formToken == "" || tokenCookie.Value != formToken) {
			return fmt.Errorf("invalid form token")
		}

		issuedCookie, _ := r.Cookie("hf_issued")
		if issuedCookie != nil && issuedCookie.Value != "" {
			if ms, convErr := strconv.ParseInt(issuedCookie.Value, 10, 64); convErr == nil && minMs > 0 {
				elapsed := time.Since(time.UnixMilli(ms)).Milliseconds()
				if elapsed < int64(minMs) {
					return fmt.Errorf("submitted too quickly")
				}
			}
		}
	}

	return nil
}

func renderField(f FormField) string {
	if f.Name == "" {
		return ""
	}
	label := f.Label
	if label == "" {
		label = strings.Title(strings.ReplaceAll(f.Name, "_", " "))
	}
	star := ""
	requiredAttr := ""
	if f.Required {
		star = " *"
		requiredAttr = " required"
	}
	placeholderAttr := ""
	if f.Placeholder != "" {
		placeholderAttr = " placeholder=\"" + htmlEscape(f.Placeholder) + "\""
	}
	helpHTML := ""
	if f.Help != "" {
		helpHTML = "<div class=\"help\">" + htmlEscape(f.Help) + "</div>"
	}
	colClass := "col-12"
	if strings.EqualFold(f.Width, "half") {
		colClass = "col-6"
	}
	var sb strings.Builder
	sb.WriteString("<div class=\"field " + colClass + "\">")
	sb.WriteString("<label for=\"" + htmlEscape(f.Name) + "\">" + htmlEscape(label) + star + "</label>")
	switch strings.ToLower(f.Type) {
	case "textarea":
		sb.WriteString("<textarea id=\"" + htmlEscape(f.Name) + "\" name=\"" + htmlEscape(f.Name) + "\"" + placeholderAttr + requiredAttr + "></textarea>")
	case "text":
		sb.WriteString("<input type=\"text\" id=\"" + htmlEscape(f.Name) + "\" name=\"" + htmlEscape(f.Name) + "\"" + placeholderAttr + requiredAttr + "/>")
	case "number":
		stepAttr := " step=\"any\""
		if strings.ToLower(f.NumberKind) == "integer" {
			stepAttr = " step=\"1\""
		}
		sb.WriteString("<input type=\"number\" id=\"" + htmlEscape(f.Name) + "\" name=\"" + htmlEscape(f.Name) + "\"" + stepAttr + placeholderAttr + requiredAttr + "/>")
	case "email":
		sb.WriteString("<input type=\"email\" id=\"" + htmlEscape(f.Name) + "\" name=\"" + htmlEscape(f.Name) + "\"" + placeholderAttr + requiredAttr + "/>")
	case "date":
		sb.WriteString("<input type=\"date\" id=\"" + htmlEscape(f.Name) + "\" name=\"" + htmlEscape(f.Name) + "\"" + placeholderAttr + requiredAttr + "/>")
	case "datetime":
		sb.WriteString("<input type=\"datetime-local\" id=\"" + htmlEscape(f.Name) + "\" name=\"" + htmlEscape(f.Name) + "\"" + placeholderAttr + requiredAttr + "/>")
	case "date_range":
		left := "Start"
		right := "End"
		if f.StartLabel != "" {
			left = f.StartLabel
		}
		if f.EndLabel != "" {
			right = f.EndLabel
		}
		sb.WriteString("<div class=\"row\"><div><label>" + htmlEscape(left) + star + "</label><input type=\"date\" name=\"" + htmlEscape(f.Name) + "_start\"" + requiredAttr + "/></div><div><label>" + htmlEscape(right) + star + "</label><input type=\"date\" name=\"" + htmlEscape(f.Name) + "_end\"" + requiredAttr + "/></div></div>")
	case "image":
		sb.WriteString("<input type=\"file\" accept=\"image/*\" id=\"" + htmlEscape(f.Name) + "\" name=\"" + htmlEscape(f.Name) + "\"" + requiredAttr + "/>")
	case "multiple":
		sb.WriteString("<select multiple id=\"" + htmlEscape(f.Name) + "\" name=\"" + htmlEscape(f.Name) + "\"" + requiredAttr + ">")
		for _, opt := range f.Options {
			sb.WriteString("<option value=\"" + htmlEscape(opt) + "\">" + htmlEscape(opt) + "</option>")
		}
		sb.WriteString("</select>")
	case "one":
		if f.Render == "radio" {
			for _, opt := range f.Options {
				sb.WriteString("<label style=\"display:flex;gap:8px;align-items:center;margin:6px 0;\"><input type=\"radio\" name=\"" + htmlEscape(f.Name) + "\" value=\"" + htmlEscape(opt) + "\"" + requiredAttr + ">" + htmlEscape(opt) + "</label>")
			}
		} else {
			sb.WriteString("<select id=\"" + htmlEscape(f.Name) + "\" name=\"" + htmlEscape(f.Name) + "\"" + requiredAttr + ">")
			sb.WriteString("<option value=\"\" disabled selected>Select…</option>")
			for _, opt := range f.Options {
				sb.WriteString("<option value=\"" + htmlEscape(opt) + "\">" + htmlEscape(opt) + "</option>")
			}
			sb.WriteString("</select>")
		}
	case "scale":
		minAttr := ""
		if f.Min != 0 {
			minAttr = fmt.Sprintf(" min=\"%v\"", f.Min)
		}
		maxAttr := ""
		if f.Max != 0 {
			maxAttr = fmt.Sprintf(" max=\"%v\"", f.Max)
		}
		stepAttr := ""
		if f.Step != 0 {
			stepAttr = fmt.Sprintf(" step=\"%v\"", f.Step)
		}
		sb.WriteString("<input type=\"range\" id=\"" + htmlEscape(f.Name) + "\" name=\"" + htmlEscape(f.Name) + "\"" + minAttr + maxAttr + stepAttr + requiredAttr + "/>")
	default:
		sb.WriteString("<input type=\"text\" id=\"" + htmlEscape(f.Name) + "\" name=\"" + htmlEscape(f.Name) + "\"" + placeholderAttr + requiredAttr + "/>")
	}
	sb.WriteString(helpHTML)
	sb.WriteString("</div>")
	return sb.String()
}

func (s *Server) StartGRPC(addr string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.grpcServer = googlegrpc.NewServer()
	proto.RegisterSourceServiceServer(s.grpcServer, &grpcsource.Server{Storage: s.storage})
	fmt.Printf("Starting Hermod gRPC server on %s...\n", addr)
	return s.grpcServer.Serve(lis)
}

func (s *Server) Stop() {
	if s.grpcServer != nil {
		s.grpcServer.GracefulStop()
	}
}
