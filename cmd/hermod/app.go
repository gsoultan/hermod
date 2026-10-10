package main

import (
	"context"
	"fmt"
	"log"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/config"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/observability"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/transformer/genai"
	"github.com/gsoultan/hermod/pkg/comm/transformer/lookup"
	engineConfig "github.com/gsoultan/hermod/pkg/engine/config"
	"github.com/gsoultan/hermod/pkg/engine/telemetry/aimetrics"
	"github.com/gsoultan/hermod/pkg/infra/filestorage"
	"github.com/gsoultan/hermod/pkg/infra/state"
	"github.com/gsoultan/hermod/pkg/security/secrets"
)

func setupRegistry(store, logStore storage.Storage, logger hermod.Logger, o *Options) (*registry.Registry, *config.Config) {
	reg := registry.NewRegistry(store, logStore)
	reg.SetLogger(logger)

	// Every model call an AI node makes is counted in the hermod_ai_*
	// metrics. The observer sees provider, model, usage and outcome only.
	genai.SetObserver(aimetrics.Observe)

	cfg, err := config.LoadConfig(o.configPath)
	if err != nil {
		cfg = &config.Config{}
		log.Printf("Warning: Using default config because config.yaml could not be loaded: %v", err)
	}

	// reference_lookup reads files only from where uploads are stored, plus
	// the directories in HERMOD_REFERENCE_DIRS. With s3 storage there is no
	// local upload directory, and only those directories remain.
	uploadDir, _ := filestorage.LocalDir(cfg.FileStorage)
	lookup.ConfigureReferenceRoots(uploadDir)

	if cfg != nil {
		initRegistryComponents(reg, cfg)
		applyEngineConfig(reg, cfg)
	}

	return reg, cfg
}

func initRegistryComponents(reg *registry.Registry, cfg *config.Config) {
	if cfg.Observability.OTLP.Endpoint != "" {
		if cfg.Observability.OTLP.ServiceName == "" {
			cfg.Observability.OTLP.ServiceName = "hermod"
		}
		if shutdown, err := observability.InitOTLP(context.Background(), cfg.Observability.OTLP); err == nil {
			// Note: shutdown should be handled by caller if possible, but here we just log
			_ = shutdown
			fmt.Printf("OTLP observability initialized: %s\n", cfg.Observability.OTLP.Endpoint)
		}
	}

	if cfg.Secrets.Type != "" {
		if mgr, err := secrets.NewManager(context.Background(), cfg.Secrets); err == nil {
			reg.SetSecretManager(mgr)
		}
	}

	if cfg.StateStore.Type != "" {
		ss, err := state.NewStateStore(state.Config{
			Type: cfg.StateStore.Type, Path: cfg.StateStore.Path, Address: cfg.StateStore.Address,
			Password: cfg.StateStore.Password, DB: cfg.StateStore.DB, Prefix: cfg.StateStore.Prefix,
		})
		if err == nil {
			reg.SetStateStore(ss)
		}
	}
}

func applyEngineConfig(reg *registry.Registry, cfg *config.Config) {
	engCfg := engineConfig.DefaultConfig()
	if cfg.Engine.MaxRetries > 0 {
		engCfg.MaxRetries = cfg.Engine.MaxRetries
	}
	if cfg.Engine.RetryInterval > 0 {
		engCfg.RetryInterval = cfg.Engine.RetryInterval
	}
	if cfg.Engine.ReconnectInterval > 0 {
		engCfg.ReconnectInterval = cfg.Engine.ReconnectInterval
	}
	if cfg.Engine.MaxInflight > 0 {
		engCfg.MaxInflight = cfg.Engine.MaxInflight
	}
	if cfg.Engine.DrainTimeout > 0 {
		engCfg.DrainTimeout = cfg.Engine.DrainTimeout
	}
	reg.SetConfig(engCfg)
}
