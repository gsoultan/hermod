package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/ai"
	"github.com/user/hermod/internal/api"
	"github.com/user/hermod/internal/autoscaler"
	"github.com/user/hermod/internal/config"
	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/engine/worker"
	"github.com/user/hermod/internal/storage"
)

func runServer(ctx context.Context, o *Options, reg *registry.Registry, store, logStore storage.Storage, cfg *config.Config, wrk *worker.Worker, logger hermod.Logger, configured, userSetup bool) error {
	if o.mode == "api" || o.mode == "standalone" {
		return startAPI(ctx, o, reg, store, logStore, cfg, wrk, logger, configured, userSetup)
	}
	runWorkerOnly(ctx, logger, configured, userSetup)
	return nil
}

// HTTP server limits. Named rather than inline so the reasoning sits with the
// numbers and a deployment that needs different ones can find them.
const (
	// Long enough for a slow mobile connection to finish sending headers,
	// short enough that a Slowloris client cannot hold a slot for minutes.
	readHeaderTimeout = 20 * time.Second

	// Keep-alive reuse is normal and worth keeping; this only bounds how long
	// an idle connection may sit before it is reclaimed.
	idleTimeout = 120 * time.Second

	// Go's own default, stated explicitly so it is a decision rather than an
	// inheritance.
	maxHeaderBytes = http.DefaultMaxHeaderBytes

	// Go 1.27. Also its default, made explicit for the same reason: a client
	// repeating one header thousands of times is a cheap way to make the
	// server do expensive work.
	maxHeaderValueCount = http.DefaultMaxHeaderValueCount
)

func startAPI(ctx context.Context, o *Options, reg *registry.Registry, store, logStore storage.Storage, cfg *config.Config, wrk *worker.Worker, logger hermod.Logger, configured, userSetup bool) error {
	aiSvc := ai.NewSelfHealingService(logger)
	server := api.NewServer(reg, store, cfg, o.configPath, aiSvc, logStore)
	if wrk != nil {
		server.SetWorker(wrk)
	}

	stopAutoscaler := startAutoscaler(o, store, configured, userSetup)
	defer stopAutoscaler()

	// Go's http.Server has no timeouts by default, and this one had none set.
	// A connection that opens and then dribbles its request headers one byte at
	// a time is held open indefinitely — Slowloris — and enough of them exhaust
	// the server without ever completing a request. The Dockerfile EXPOSEs this
	// port directly, so there is no reverse proxy in the default deployment to
	// absorb it.
	//
	// What is deliberately NOT set here matters as much as what is. WriteTimeout
	// and ReadTimeout apply to the whole exchange, and this server also carries
	// the UI's WebSockets (/api/ws/live and friends) and Server-Sent Events,
	// which are long-lived by design: either one would sever a working stream
	// mid-flight on a timer. ReadHeaderTimeout bounds only the part that must
	// be fast — the headers — and IdleTimeout only reclaims keep-alive
	// connections between requests, so neither touches a stream in progress.
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", o.port),
		Handler: server.Routes(),

		// Headers must arrive promptly; a request body may legitimately take
		// as long as it takes (large imports, uploads).
		ReadHeaderTimeout: readHeaderTimeout,

		// Reclaims idle keep-alive connections. Longer than the header
		// timeout, because a browser reusing a connection is normal.
		IdleTimeout: idleTimeout,

		// A cap on total header size (Go's default is 1MB) and, since Go 1.27,
		// on the number of values a single header may repeat — both are ways a
		// client can make the server allocate far more than the request is
		// worth.
		MaxHeaderBytes:      maxHeaderBytes,
		MaxHeaderValueCount: maxHeaderValueCount,
	}
	fatal := startServersAsync(
		httpServer.ListenAndServe,
		func() error { return server.StartGRPC(fmt.Sprintf(":%d", o.grpcPort)) },
	)

	fmt.Printf("Starting Hermod API server on :%d...\n", o.port)

	var startErr error
	select {
	case <-ctx.Done():
		logger.Info("Shutting down API server...")
	case startErr = <-fatal:
		// Without this the process kept running with no listener at all: alive,
		// serving nothing, and undetectable by anything that looks at ports.
		logger.Error("Listener failed, shutting down", "error", startErr)
	}

	server.Stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	// Shutdown waits for every connection to go idle, and a Server-Sent Events
	// response never does. Bound the wait, then drop what is left.
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn("Graceful shutdown timed out, closing connections", "error", err)
		_ = httpServer.Close()
	}
	return startErr
}

// shutdownGrace bounds how long in-flight requests get to finish. Long-lived
// streams (SSE, log tails) never finish on their own, so an unbounded wait here
// is an unbounded hang.
const shutdownGrace = 10 * time.Second

// startServersAsync runs both listeners and reports the first fatal error. A
// listener that cannot bind is a misconfiguration the operator has to fix, so
// it must bring the process down rather than be logged and forgotten.
func startServersAsync(serveHTTP, serveGRPC func() error) <-chan error {
	fatal := make(chan error, 2)
	serve := func(name string, fn func() error) {
		// A deliberate shutdown surfaces as ErrServerClosed from net/http and as
		// a nil error from gRPC's GracefulStop. Neither is a failure.
		if err := fn(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal <- fmt.Errorf("%s server failed: %w", name, err)
		}
	}
	go serve("API", serveHTTP)
	go serve("gRPC", serveGRPC)
	return fatal
}

func startAutoscaler(o *Options, store storage.Storage, configured, userSetup bool) func() {
	if (o.mode == "api" || o.mode == "standalone") && configured && userSetup && !o.disableAutoscaler && store != nil {
		manager := &autoscaler.KubernetesWorkerManager{
			Namespace: "hermod", Deployment: "hermod-worker", Storage: store,
		}
		as := autoscaler.NewAutoscaler(store, manager)
		as.Start()
		fmt.Println("Autoscaler service started")
		return as.Stop
	}
	return func() {}
}

func runWorkerOnly(ctx context.Context, logger hermod.Logger, configured, userSetup bool) {
	if configured && userSetup {
		logger.Info("Starting Hermod worker in dedicated mode")
		<-ctx.Done()
	} else {
		logger.Error("Hermod is not configured yet. Please run API mode to complete setup. Exiting.")
		log.Fatal("Not configured")
	}
}
