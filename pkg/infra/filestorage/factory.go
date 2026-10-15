// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
