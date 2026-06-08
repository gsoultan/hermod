package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/ai"
	"github.com/user/hermod/internal/api"
	"github.com/user/hermod/internal/autoscaler"
	"github.com/user/hermod/internal/config"
	"github.com/user/hermod/internal/engine/registry"
	"github.com/user/hermod/internal/engine/worker"
	"github.com/user/hermod/internal/storage"
)

func runServer(ctx context.Context, o *Options, reg *registry.Registry, store, logStore storage.Storage, cfg *config.Config, wrk *worker.Worker, logger hermod.Logger, configured, userSetup bool) {
	if o.mode == "api" || o.mode == "standalone" {
		startAPI(ctx, o, reg, store, logStore, cfg, wrk, logger, configured, userSetup)
	} else {
		runWorkerOnly(ctx, logger, configured, userSetup)
	}
}

func startAPI(ctx context.Context, o *Options, reg *registry.Registry, store, logStore storage.Storage, cfg *config.Config, wrk *worker.Worker, logger hermod.Logger, configured, userSetup bool) {
	aiSvc := ai.NewSelfHealingService(logger)
	server := api.NewServer(reg, store, cfg, o.configPath, aiSvc, logStore)
	if wrk != nil {
		server.SetWorker(wrk)
	}

	stopAutoscaler := startAutoscaler(o, store, configured, userSetup)
	defer stopAutoscaler()

	httpServer := &http.Server{Addr: fmt.Sprintf(":%d", o.port), Handler: server.Routes()}
	startServersAsync(server, httpServer, o.grpcPort)

	fmt.Printf("Starting Hermod API server on :%d...\n", o.port)
	<-ctx.Done()
	logger.Info("Shutting down API server...")
	server.Stop()
	_ = httpServer.Shutdown(context.Background())
}

func startServersAsync(server *api.Server, httpServer *http.Server, grpcPort int) {
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("API server failed: %v", err)
		}
	}()

	go func() {
		if err := server.StartGRPC(fmt.Sprintf(":%d", grpcPort)); err != nil {
			log.Printf("gRPC server failed: %v", err)
		}
	}()
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
