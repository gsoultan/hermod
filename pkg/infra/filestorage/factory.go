package filestorage

import (
	"context"
	"fmt"

	"github.com/user/hermod/internal/config"
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
		dir := cfg.LocalDir
		if dir == "" {
			dir = "uploads"
		}
		return NewLocalStorage(dir)
	default:
		return nil, fmt.Errorf("unknown storage type: %s", cfg.Type)
	}
}
