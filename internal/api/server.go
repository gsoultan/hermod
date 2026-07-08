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
	"github.com/user/hermod/internal/config"
	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/storage"
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

	// Health endpoints (unauthenticated; used by Kubernetes and load balancers)
	mux.HandleFunc("GET /healthz", s.Handler.HandleLiveness)
	mux.HandleFunc("GET /livez", s.Handler.HandleLiveness)
	mux.HandleFunc("GET /readyz", s.Handler.HandleReadiness)
	mux.HandleFunc("GET /api/version", s.Handler.HandleVersion)

	// Optional pprof endpoints guarded by env var
	if os.Getenv("HERMOD_PPROF") == "true" {
		mux.HandleFunc("/debug/pprof/", httppprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", httppprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", httppprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", httppprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", httppprof.Trace)
	}

	s.Handler.RegisterWorkflowRoutes(mux)
	s.Handler.RegisterSourceRoutes(mux)
	s.Handler.RegisterSinkRoutes(mux)
	s.Handler.RegisterApprovalRoutes(mux)
	s.Handler.RegisterAuthRoutes(mux)
	s.Handler.RegisterInfrastructureRoutes(mux)
	s.Handler.RegisterSchemaRoutes(mux)
	s.Handler.RegisterMarketplaceRoutes(mux)

	mux.HandleFunc("POST /api/webhooks/{path...}", s.Handler.HandleWebhook)
	mux.HandleFunc("GET /api/webhooks/{path...}", s.Handler.HandleWebhook)
	mux.HandleFunc("POST /api/graphql/{path...}", s.Handler.HandleGraphQL)
	// Data orchestration streams (SSE)
	mux.HandleFunc("GET /streams/sse", s.Handler.HandleSSEStream)
	// Internal API notifications (SSE)
	mux.HandleFunc("GET /api/notifications/sse", s.Handler.HandleInternalSSE)

	// WebSocket server-mode endpoints
	mux.HandleFunc("GET /api/ws/in/{path...}", s.Handler.HandleWSIn)
	mux.HandleFunc("GET /api/ws/out/{workflowID}", s.Handler.HandleWSOut)

	// Form submissions endpoint
	mux.HandleFunc("POST /api/forms/{path...}", s.Handler.HandleForm)
	mux.HandleFunc("GET /api/forms/{path...}", s.Handler.HandleForm)
	// Public generated form page
	mux.HandleFunc("GET /forms/{path...}", s.Handler.ServeFormPage)
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
