package filestorage

import (
	"context"
	"io"
)

// Storage defines the interface for file storage operations.
type Storage interface {
	// Save stores the content from the reader and returns a path/URI to the stored file.
	Save(ctx context.Context, name string, r io.Reader) (string, error)
	// GetURL returns a URL or path to access the file.
	GetURL(ctx context.Context, name string) (string, error)
	// Delete removes the file from storage.
	Delete(ctx context.Context, name string) error
	// Type returns the storage type (local, s3).
	Type() string
}
