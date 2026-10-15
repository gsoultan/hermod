// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package filestorage

import (
	"context"
	"fmt"

	"github.com/gsoultan/hermod/internal/config"
)

func NewStorage(ctx context.Context, cfg config.FileStorageConfig) (Storage, error) {
	switch cfg.Type {
	case "s3":
		return NewS3Storage(
			ctx,
			cfg.S3.Endpoint,
			cfg.S3.Region,
			cfg.S3.Bucket,
			cfg.S3.AccessKeyID,
			cfg.S3.SecretAccessKey,
			cfg.S3.UseSSL,
		)
	case "local", "":
		dir, _ := LocalDir(cfg)
		return NewLocalStorage(dir)
	default:
		return nil, fmt.Errorf("unknown storage type: %s", cfg.Type)
	}
}

// LocalDir is the directory local storage writes uploads to, and false when
// cfg stores them elsewhere (s3). NewStorage creates its storage here, and
// reference_lookup is bounded to it, so both read the same setting the same
// way.
func LocalDir(cfg config.FileStorageConfig) (string, bool) {
	switch cfg.Type {
	case "local", "":
		if cfg.LocalDir != "" {
			return cfg.LocalDir, true
		}
		return "uploads", true
	}
	return "", false
}
