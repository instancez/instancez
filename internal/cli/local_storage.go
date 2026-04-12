package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/saedx1/ultrabase/internal/domain"
)

// LocalStore implements domain.ObjectStore using the local filesystem.
// Intended for development use only.
type LocalStore struct {
	basePath string
}

func NewLocalStore(basePath string) (*LocalStore, error) {
	if err := os.MkdirAll(basePath, 0o755); err != nil {
		return nil, fmt.Errorf("create storage dir: %w", err)
	}
	return &LocalStore{basePath: basePath}, nil
}

func (s *LocalStore) SignUpload(_ context.Context, key string, _ string, _ time.Duration) (string, error) {
	fullPath := filepath.Join(s.basePath, key)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", fmt.Errorf("create dir: %w", err)
	}
	// Return the file path as the "upload URL" — the client writes directly in dev mode
	return "file://" + fullPath, nil
}

func (s *LocalStore) SignDownload(_ context.Context, key string, _ time.Duration) (string, error) {
	fullPath := filepath.Join(s.basePath, key)
	return "file://" + fullPath, nil
}

func (s *LocalStore) Delete(_ context.Context, key string) error {
	fullPath := filepath.Join(s.basePath, key)
	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}

func (s *LocalStore) EnsureBucket(_ context.Context, bucket string) error {
	dir := filepath.Join(s.basePath, bucket)
	return os.MkdirAll(dir, 0o755)
}

var _ domain.ObjectStore = (*LocalStore)(nil)
