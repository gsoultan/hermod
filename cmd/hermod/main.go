package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
	"github.com/user/hermod"
	"github.com/user/hermod/internal/api"
	"github.com/user/hermod/internal/config"
	"github.com/user/hermod/internal/runtimetune"
	"github.com/user/hermod/internal/service"
	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/internal/version"

	_ "github.com/user/hermod/internal/engine/registry/nodes"
	_ "github.com/user/hermod/pkg/comm/transformer/advanced"
	_ "github.com/user/hermod/pkg/comm/transformer/ai"
	_ "github.com/user/hermod/pkg/comm/transformer/core"
	_ "github.com/user/hermod/pkg/comm/transformer/logic"
	_ "github.com/user/hermod/pkg/comm/transformer/lookup"
	_ "github.com/user/hermod/pkg/comm/transformer/security"
	"github.com/user/hermod/pkg/engine/telemetry"
	"github.com/user/hermod/pkg/security/crypto"
	_ "modernc.org/sqlite"
)

func main() {
	runtimetune.Apply()
	_ = godotenv.Load()
	_ = config.EnsureConfigDir()

	o := parseOptions()
	if o.versionFlag {
		fmt.Printf("hermod %s\n", version.Version)
		return
	}

	if o.buildUI || (o.mode != "worker" && o.serviceAction == "" && !api.IsUIEmbedded() && !config.IsUIBuilt() && config.CanBuildUI() && os.Getenv("HERMOD_ENV") != "production") {
		buildUIAndExit(o.mode)
	}

	svcCfg := service.Config{
		Name:        "hermod",
		DisplayName: "Hermod Messaging System",
		Description: "Hermod is a high-performance messaging and workflow automation platform.",
		Arguments:   filterServiceArgs(os.Args),
	}

	if err := service.Manage(svcCfg, o.serviceAction, func(ctx context.Context) { runApp(ctx, o) }); err != nil {
		log.Fatal(err)
	}
}

func buildUIAndExit(mode string) {
	if err := config.BuildUI(); err != nil {
		log.Fatalf("Failed to build UI: %v", err)
	}
	if mode == "build-only" {
		fmt.Println("UI build complete. Exiting due to build-only mode.")
		os.Exit(0)
	}
}

func filterServiceArgs(args []string) []string {
	var filtered []string
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-service=") || arg == "-service" || arg == "--service" {
			if arg == "-service" || arg == "--service" {
				i++
			}
			continue
		}
		filtered = append(filtered, arg)
	}
	return filtered
}

func runApp(svcCtx context.Context, o *Options) {
	logger := telemetry.NewDefaultLogger()
	initMasterKey(o)

	if o.mode != "api" && o.mode != "worker" && o.mode != "standalone" {
		log.Fatalf("Invalid mode: %s", o.mode)
	}

	dbType, dbConn, logType, logConn := getStorageConfig(o)
	firstRun := !config.IsDBConfigured()

	var store, logStore storage.Storage
	if !isWorkerModeWithPlatform(o) && !firstRun {
		store = initPrimaryStorage(svcCtx, dbType, dbConn, logger)
		logStore = initLogStorage(svcCtx, logType, logConn, logger)
	}

	reg, cfg := setupRegistry(store, logStore, logger, o)
	ctx, cancel := setupSignalHandler(svcCtx, logger, reg, store, logStore)
	defer cancel()

	configured, userSetup := computeSetupStatus(ctx, store, config.IsDBConfigured())
	if isWorkerModeWithPlatform(o) {
		// A remote worker relies on the platform for its configuration, so it
		// has no local DB/config.yaml. Treat it as configured to allow startup.
		configured, userSetup = true, true
	}
	logSetupStatus(logger, configured, userSetup, o.port)

	wrk := setupWorker(ctx, cancel, o, reg, store, configured, userSetup)
	runServer(ctx, o, reg, store, logStore, cfg, wrk, logger, configured, userSetup)

	logger.Info("Hermod shutdown complete")
}

func initMasterKey(o *Options) {
	if o.masterKey != "" {
		crypto.SetMasterKey(o.masterKey)
	} else if envKey := os.Getenv("HERMOD_MASTER_KEY"); envKey != "" {
		crypto.SetMasterKey(envKey)
	}
}

func isWorkerModeWithPlatform(o *Options) bool {
	return o.mode == "worker" && o.platformURL != ""
}

func getStorageConfig(o *Options) (string, string, string, string) {
	dbType, dbConn, logType, logConn := o.dbType, o.dbConn, o.logDBType, o.logDBConn
	if config.IsDBConfigured() {
		if cfg, err := config.LoadDBConfig(); err == nil {
			dbType, dbConn = cfg.Type, cfg.Conn
			if cfg.LogType != "" && logType == "" {
				logType = cfg.LogType
			}
			if cfg.LogConn != "" && logConn == "" {
				logConn = cfg.LogConn
			}
			if cfg.CryptoMasterKey != "" && o.masterKey == "" && os.Getenv("HERMOD_MASTER_KEY") == "" {
				crypto.SetMasterKey(cfg.CryptoMasterKey)
			}
		}
	}
	return dbType, dbConn, logType, logConn
}

func initPrimaryStorage(ctx context.Context, dbType, dbConn string, logger hermod.Logger) storage.Storage {
	logger.Info("Opening primary database ...", "type", dbType)
	store, err := initStorage(dbType, dbConn)
	if err != nil {
		logger.Error("Failed to initialize primary storage", "error", err)
		if store != nil {
			go retryInit(ctx, store, "primary", logger)
		}
	}
	return store
}

func initLogStorage(ctx context.Context, dbType, dbConn string, logger hermod.Logger) storage.Storage {
	if dbType == "" || dbConn == "" {
		return nil
	}
	logger.Info("Opening logging database ...", "type", dbType)
	store, err := initStorage(dbType, dbConn)
	if err != nil {
		logger.Error("Failed to initialize logging storage", "error", err)
		if store != nil {
			go retryInit(ctx, store, "logging", logger)
		}
	}
	return store
}

func setupSignalHandler(svcCtx context.Context, logger hermod.Logger, reg interface {
	StopAll()
	Close()
}, store, logStore storage.Storage) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(svcCtx)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		select {
		case sig := <-sigChan:
			logger.Info("Received signal, shutting down gracefully...", "signal", sig)
		case <-svcCtx.Done():
			logger.Info("Service manager requested shutdown, shutting down gracefully...")
		}
		cancel()
		reg.StopAll()
		reg.Close()
		closeStorage(store)
		closeStorage(logStore)
	}()
	return ctx, cancel
}

func closeStorage(s storage.Storage) {
	if s == nil {
		return
	}
	if closer, ok := s.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}

func logSetupStatus(logger hermod.Logger, configured, userSetup bool, port int) {
	if !configured || !userSetup {
		logger.Info("First-time setup detected. Starting API only.", "setup_url", fmt.Sprintf("http://localhost:%d/setup", port))
	}
}
