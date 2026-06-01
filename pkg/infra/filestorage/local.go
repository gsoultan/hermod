package filestorage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type LocalStorage struct {
	baseDir string
}

func NewLocalStorage(baseDir string) (*LocalStorage, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base directory: %w", err)
	}
	return &LocalStorage{baseDir: baseDir}, nil
}

func (s *LocalStorage) Save(ctx context.Context, name string, r io.Reader) (string, error) {
	path := filepath.Join(s.baseDir, name)

	// Ensure directory exists if name contains subdirectories
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("failed to create file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, r); err != nil {
		return "", fmt.Errorf("failed to save file: %w", err)
	}

	return filepath.Abs(path)
}

func (s *LocalStorage) GetURL(ctx context.Context, name string) (string, error) {
	return filepath.Abs(filepath.Join(s.baseDir, name))
}

func (s *LocalStorage) Delete(ctx context.Context, name string) error {
	path := filepath.Join(s.baseDir, name)
	return os.Remove(path)
}

func (s *LocalStorage) Type() string {
	return "local"
}
