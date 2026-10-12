package main

import (
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/config"
	"github.com/gsoultan/hermod/pkg/ml/builtin"
	mlworker "github.com/gsoultan/hermod/pkg/ml/worker"
)

// setupMLEngine puts Hermod's built-in ML engine in force for whenever no
// hermod-ml worker is configured, unless HERMOD_ML_BUILTIN=false turns it
// off. Its datasets and versions live under HERMOD_ML_BUILTIN_DIR, by default
// the ml directory of Hermod's config directory.
func setupMLEngine(logger hermod.Logger) {
	cfg, on := builtin.ConfigFromEnv(config.GetConfigDir())
	if !on {
		return
	}
	e, err := builtin.New(cfg)
	if err != nil {
		logger.Warn("The built-in ML engine is off", "error", err)
		return
	}
	mlworker.Default.SetBuiltin(e.Client())
}
