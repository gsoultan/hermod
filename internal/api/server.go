package api

import (
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	httppprof "net/http/pprof"
	"os"
	"path/filepath"
	"strings"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/user/hermod/internal/ai"
	"github.com/user/hermod/internal/api/handlers"
	approvalhttp "github.com/user/hermod/internal/approval/transport/http"
	authhttp "github.com/user/hermod/internal/auth/transport/http"
	"github.com/user/hermod/internal/config"
	dashboardhttp "github.com/user/hermod/internal/dashboard/transport/http"
	"github.com/user/hermod/internal/engine/registry"
	fileshttp "github.com/user/hermod/internal/files/transport/http"
	formshttp "github.com/user/hermod/internal/forms/transport/http"
	infrahttp "github.com/user/hermod/internal/infra/transport/http"
	logshttp "github.com/user/hermod/internal/logs/transport/http"
	marketplacehttp "github.com/user/hermod/internal/marketplace/transport/http"
	schemahttp "github.com/user/hermod/internal/schema/transport/http"
	sinkhttp "github.com/user/hermod/internal/sink/transport/http"
	sourcehttp "github.com/user/hermod/internal/source/transport/http"
	ssehttp "github.com/user/hermod/internal/sse/transport/http"
	"github.com/user/hermod/internal/storage"
	webhookshttp "github.com/user/hermod/internal/webhooks/transport/http"
	workerhttp "github.com/user/hermod/internal/worker/transport/http"
	workflowhttp "github.com/user/hermod/internal/workflow/transport/http"
	wshttp "github.com/user/hermod/internal/ws/transport/http"
	grpcsource "github.com/user/hermod/pkg/comm/source/grpc"
	"github.com/user/hermod/pkg/comm/source/grpc/proto"
	"github.com/user/hermod/pkg/infra/filestorage"
	googlegrpc "google.golang.org/grpc"
)

//go:embed all:static
var staticFS embed.FS

// IsUIEmbedded checks if the UI assets are embedded in the binary.
func IsUIEmbedded() bool {
	_, err := staticFS.ReadFile("static/index.html")
	return err == nil
}

// Server is the HTTP API server for Hermod.
// It wires routing, middleware, and access to the storage and engine registry.
type Server struct {
	Handler    *handlers.Handler
	Storage    storage.Storage
	GrpcServer *googlegrpc.Server
}

// NewServer constructs a new Server with the provided engine registry and storage backend.
func NewServer(registry *registry.Registry, store storage.Storage, cfg *config.Config, configPath string, aiSvc *ai.SelfHealingService, ls ...storage.Storage) *Server {
	var logStore storage.Storage
	if len(ls) > 0 {
		logStore = ls[0]
	}
	if logStore == nil {
		logStore = store
	}
	s := &Server{
		Storage: store,
	}
	s.Handler = &handlers.Handler{
		Storage:       store,
		LogStorage:    logStore,
		Registry:      registry,
		AI:            aiSvc,
		Config:        cfg,
		ConfigPath:    configPath,
		RateLimitQuit: make(chan struct{}),
	}
	// Initialize file storage from config; fallback to local uploads dir
	if cfg != nil {
		if fstorage, err := filestorage.NewStorage(context.Background(), cfg.FileStorage); err == nil {
			s.Handler.FileStorage = fstorage
		} else {
			if lfs, lerr := filestorage.NewLocalStorage("uploads"); lerr == nil {
				s.Handler.FileStorage = lfs
			}
		}
	} else {
		if lfs, _ := filestorage.NewLocalStorage("uploads"); lfs != nil {
			s.Handler.FileStorage = lfs
		}
	}
	return s
}

// maintenance logic moved to maintenance.go

// updateCryptoMasterKey sets or rotates the crypto master key stored in db_config.yaml (Admin only).
// The provided key must be at least 16 characters. This endpoint does not return the key.

// SetWorker sets the worker updater for the handler.
func (s *Server) SetWorker(w handlers.WorkerUpdater) {
	s.Handler.Worker = w
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	infraH := infrahttp.NewInfraHandler(s.Handler)
	workflowH := workflowhttp.NewWorkflowHandler(s.Handler)
	sourceH := sourcehttp.NewSourceHandler(s.Handler)
	sinkH := sinkhttp.NewSinkHandler(s.Handler)
	approvalH := approvalhttp.NewApprovalHandler(s.Handler)
	authH := authhttp.NewAuthHandler(s.Handler)
	schemaH := schemahttp.NewSchemaHandler(s.Handler)
	marketplaceH := marketplacehttp.NewMarketplaceHandler(s.Handler)
	logsH := logshttp.NewLogHandler(s.Handler)
	dashboardH := dashboardhttp.NewDashboardHandler(s.Handler)
	sseH := ssehttp.NewSSEHandler(s.Handler)
	wsH := wshttp.NewWSHandler(s.Handler)
	formsH := formshttp.NewFormHandler(s.Handler)
	filesH := fileshttp.NewFileHandler(s.Handler)
	webhooksH := webhookshttp.NewWebhookHandler(s.Handler)
	workerH := workerhttp.NewWorkerHandler(s.Handler)

	// Health endpoints (unauthenticated; used by Kubernetes and load balancers)
	mux.HandleFunc("GET /healthz", infraH.HandleLiveness)
	mux.HandleFunc("GET /livez", infraH.HandleLiveness)
	mux.HandleFunc("GET /readyz", infraH.HandleReadiness)
	mux.HandleFunc("GET /api/version", infraH.HandleVersion)

	// Optional pprof endpoints guarded by env var
	if os.Getenv("HERMOD_PPROF") == "true" {
		mux.HandleFunc("/debug/pprof/", httppprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", httppprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", httppprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", httppprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", httppprof.Trace)
	}

	workflowH.RegisterWorkflowRoutes(mux)
	sourceH.RegisterSourceRoutes(mux)
	sinkH.RegisterSinkRoutes(mux)
	approvalH.RegisterApprovalRoutes(mux)
	authH.RegisterAuthRoutes(mux)
	infraH.RegisterInfrastructureRoutes(mux)
	schemaH.RegisterSchemaRoutes(mux)
	marketplaceH.RegisterMarketplaceRoutes(mux)
	logsH.RegisterLogRoutes(mux)
	dashboardH.RegisterDashboardRoutes(mux)
	sseH.RegisterSSERoutes(mux)
	wsH.RegisterWSRoutes(mux)
	formsH.RegisterFormRoutes(mux)
	filesH.RegisterFileRoutes(mux)
	webhooksH.RegisterWebhookRoutes(mux)
	workerH.RegisterWorkerRoutes(mux)

	mux.HandleFunc("POST /api/graphql/{path...}", webhooksH.HandleGraphQL)
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
		if s.Handler.IsFirstRun(r.Context()) {
			if s.Handler.WantsHTML(r) && path != "setup" && path != "setup/" && !strings.HasPrefix(path, "api/") {
				http.Redirect(w, r, "/setup", http.StatusFound)
				return
			}
		} else {
			// If already configured, don't allow access to /setup
			if (path == "setup" || path == "setup/") && s.Handler.WantsHTML(r) {
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
		}

		f, err := static.Open(path)
		if err == nil {
			stat, err := f.Stat()
			f.Close()
			if err == nil && !stat.IsDir() {
				// Check for Brotli compression support
				if strings.Contains(r.Header.Get("Accept-Encoding"), "br") {
					brPath := path + ".br"
					if brF, err := static.Open(brPath); err == nil {
						brStat, err := brF.Stat()
						if err == nil && !brStat.IsDir() {
							// Found Brotli version!
							defer brF.Close()
							if ctype := mime.TypeByExtension(filepath.Ext(path)); ctype != "" {
								w.Header().Set("Content-Type", ctype)
							}
							w.Header().Set("Content-Encoding", "br")
							w.Header().Set("Vary", "Accept-Encoding")
							http.ServeContent(w, r, path, brStat.ModTime(), brF.(io.ReadSeeker))
							return
						}
						brF.Close()
					}
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// If not found and not an API request, serve index.html for SPA routing
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			// Serve index.html for SPA routing
			f, err := static.Open("index.html")
			if err == nil {
				stat, err := f.Stat()
				if err == nil && !stat.IsDir() {
					// Check for Brotli compression support for SPA root
					if strings.Contains(r.Header.Get("Accept-Encoding"), "br") {
						brPath := "index.html.br"
						if brF, err := static.Open(brPath); err == nil {
							brStat, err := brF.Stat()
							if err == nil && !brStat.IsDir() {
								defer brF.Close()
								w.Header().Set("Content-Type", "text/html; charset=utf-8")
								w.Header().Set("Content-Encoding", "br")
								w.Header().Set("Vary", "Accept-Encoding")
								http.ServeContent(w, r, "index.html", brStat.ModTime(), brF.(io.ReadSeeker))
								return
							}
							brF.Close()
						}
					}
				}
				f.Close()
				r.URL.Path = "/"
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "404 Not Found: %s %s", r.Method, r.URL.Path)
	})

	// Order: security headers -> CORS -> recover -> store-guard -> auth -> handlers
	return s.Handler.SecurityHeadersMiddleware(
		s.Handler.CorsMiddleware(
			s.Handler.RecoverMiddleware(
				s.Handler.StoreGuardMiddleware(
					s.Handler.AuthMiddleware(mux),
				),
			),
		),
	)
}

func (s *Server) StartGRPC(addr string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.GrpcServer = googlegrpc.NewServer()
	proto.RegisterSourceServiceServer(s.GrpcServer, &grpcsource.Server{Storage: s.Storage})
	fmt.Printf("Starting Hermod gRPC server on %s...\n", addr)
	return s.GrpcServer.Serve(lis)
}

func (s *Server) Stop() {
	if s.GrpcServer != nil {
		s.GrpcServer.GracefulStop()
	}
}
