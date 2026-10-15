// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
