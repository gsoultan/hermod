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
