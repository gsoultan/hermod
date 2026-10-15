// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package main

import (
	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry"
	"github.com/gsoultan/hermod/internal/ml"
	"github.com/gsoultan/hermod/pkg/ml/builtin"
	mlworker "github.com/gsoultan/hermod/pkg/ml/worker"
)

// setupMLEngine puts Hermod's built-in ML engine in force beside any
// hermod-ml worker, unless HERMOD_ML_BUILTIN=false turns it off. It keeps
// its datasets and model versions in Hermod's metadata store (the registry's,
// which setup and a database switch replace), so every replica serves every
// model; HERMOD_ML_BUILTIN_DIR is used only with a store that cannot keep
// them.
func setupMLEngine(logger hermod.Logger, reg *registry.Registry) {
	cfg, on := builtin.ConfigFromEnv()
	if !on {
		return
	}
	var fallback builtin.Blobs
	if cfg.Dir != "" {
		d, err := builtin.DirBlobs(cfg.Dir)
		if err != nil {
			logger.Warn("HERMOD_ML_BUILTIN_DIR cannot be used", "error", err)
		}
		fallback = d
	}
	cfg.Blobs = ml.BuiltinBlobs(func() any { return reg.GetStorage() }, fallback)
	e, err := builtin.New(cfg)
	if err != nil {
		logger.Warn("The built-in ML engine is off", "error", err)
		return
	}
	mlworker.Default.SetEngine(e.Route)
}
