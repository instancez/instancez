package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/instancez/instancez/internal/domain"
)

// LocalStore implements domain.ObjectStore using the local filesystem.
// Intended for development use only.
type LocalStore struct {
	basePath  string
	keyPrefix string
}

func NewLocalStore(basePath, keyPrefix string) (*LocalStore, error) {
	if err := os.MkdirAll(basePath, 0o755); err != nil {
		return nil, fmt.Errorf("create storage dir: %w", err)
	}
	return &LocalStore{basePath: basePath, keyPrefix: keyPrefix}, nil
}

// fullPath resolves a key under basePath/keyPrefix and rejects keys that would escape it.
func (s *LocalStore) fullPath(key string) (string, error) {
	if !filepath.IsLocal(key) {
		return "", fmt.Errorf("invalid key %q", key)
	}
	return filepath.Join(s.basePath, s.keyPrefix, key), nil
}

func (s *LocalStore) SignUpload(_ context.Context, key string, _ string, _ time.Duration) (string, error) {
	fullPath, err := s.fullPath(key)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", fmt.Errorf("create dir: %w", err)
	}
	return "file://" + fullPath, nil
}

func (s *LocalStore) SignDownload(_ context.Context, key string, _ time.Duration, _ domain.DownloadOptions) (string, error) {
	fullPath, err := s.fullPath(key)
	if err != nil {
		return "", err
	}
	return "file://" + fullPath, nil
}

func (s *LocalStore) Delete(_ context.Context, key string) error {
	fullPath, err := s.fullPath(key)
	if err != nil {
		return err
	}
	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}

func (s *LocalStore) EnsureBucket(_ context.Context, bucket string) error {
	dir, err := s.fullPath(bucket)
	if err != nil {
		return err
	}
	return os.MkdirAll(dir, 0o755)
}

func (s *LocalStore) Upload(_ context.Context, key string, r io.Reader, _ string, _ int64) error {
	fullPath, err := s.fullPath(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	f, err := os.Create(fullPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(f, r); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}

func (s *LocalStore) Download(_ context.Context, key string) (io.ReadCloser, string, error) {
	fullPath, err := s.fullPath(key)
	if err != nil {
		return nil, "", err
	}
	f, err := os.Open(fullPath)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil, "", fmt.Errorf("open file: %w: %w", domain.ErrNotFound, err)
	}
	if err != nil {
		return nil, "", fmt.Errorf("open file: %w", err)
	}
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	ct := http.DetectContentType(buf[:n])
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, "", fmt.Errorf("seek file: %w", err)
	}
	return f, ct, nil
}

func (s *LocalStore) Copy(_ context.Context, srcKey, dstKey string) error {
	srcPath, err := s.fullPath(srcKey)
	if err != nil {
		return err
	}
	dstPath, err := s.fullPath(dstKey)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer func() { _ = src.Close() }()
	dst, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("create dest: %w", err)
	}
	defer func() { _ = dst.Close() }()
	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return nil
}

func (s *LocalStore) Head(_ context.Context, key string) (domain.ObjectInfo, error) {
	fullPath, err := s.fullPath(key)
	if err != nil {
		return domain.ObjectInfo{}, err
	}
	fi, err := os.Stat(fullPath)
	if err != nil {
		return domain.ObjectInfo{}, fmt.Errorf("stat: %w", err)
	}
	return domain.ObjectInfo{Key: key, Size: fi.Size()}, nil
}

func (s *LocalStore) List(_ context.Context, prefix string) ([]domain.ObjectInfo, error) {
	base := filepath.Join(s.basePath, s.keyPrefix)
	dir := base
	if prefix != "" {
		var err error
		if dir, err = s.fullPath(prefix); err != nil {
			return nil, err
		}
	}
	var items []domain.ObjectInfo
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		// Rel is computed from base so the keyPrefix never leaks to callers.
		rel, _ := filepath.Rel(base, path)
		rel = strings.ReplaceAll(rel, string(filepath.Separator), "/")
		items = append(items, domain.ObjectInfo{Key: rel, Size: info.Size()})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("walk: %w", err)
	}
	return items, nil
}

var _ domain.ObjectStore = (*LocalStore)(nil)
