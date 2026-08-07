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

func startAPI(ctx context.Context, o *Options, reg *registry.Registry, store, logStore storage.Storage, cfg *config.Config, wrk *worker.Worker, logger hermod.Logger, configured, userSetup bool) error {
	aiSvc := ai.NewSelfHealingService(logger)
	server := api.NewServer(reg, store, cfg, o.configPath, aiSvc, logStore)
	if wrk != nil {
		server.SetWorker(wrk)
	}

	stopAutoscaler := startAutoscaler(o, store, configured, userSetup)
	defer stopAutoscaler()

	httpServer := &http.Server{Addr: fmt.Sprintf(":%d", o.port), Handler: server.Routes()}
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
