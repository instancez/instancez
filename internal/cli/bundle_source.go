package cli

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	s3adapter "github.com/instancez/instancez/internal/adapter/s3"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
)

// BundleSource implements config.Source backed by a pre-built bundle archive.
// The bundle contains instancez.yaml at its root, so a single S3 object is the
// complete source of truth — config, schema, and function code arrive together,
// eliminating the ordering race between config and bundle files.
type BundleSource struct {
	pointer       string // original --bundle value; may include #version
	baseURI       string // pointer without #version (for HEAD requests and new fetches)
	extractParent string

	isS3     bool
	s3Bucket string
	s3Key    string

	mu      sync.Mutex
	store   *s3adapter.Store
	lastDir string // most recently extracted bundle directory
}

// NewBundleSource creates a BundleSource for pointer.
// pointer is in the same form accepted by app.FetchAndExtract:
//   - s3://bucket/key.tar.gz         (S3 bundle, polls ETag for changes)
//   - s3://bucket/key.tar.gz#etag    (S3 bundle with pinned version, still polls for new ETags)
//   - /path/to/bundle.tar.gz         (local bundle, polls mtime for changes)
func NewBundleSource(pointer, extractParent string) *BundleSource {
	baseURI, _ := splitBundlePointer(pointer)
	bs := &BundleSource{
		pointer:       pointer,
		baseURI:       baseURI,
		extractParent: extractParent,
	}
	if strings.HasPrefix(baseURI, "s3://") {
		bs.isS3 = true
		if u, err := url.Parse(baseURI); err == nil {
			bs.s3Bucket = u.Host
			bs.s3Key = strings.TrimPrefix(u.Path, "/")
		}
	}
	return bs
}

// splitBundlePointer splits a bundle pointer into its URI and version parts.
// The version is everything after the last '#'; if there is no '#', version is "".
func splitBundlePointer(pointer string) (uri, version string) {
	if i := strings.LastIndex(pointer, "#"); i >= 0 {
		return pointer[:i], pointer[i+1:]
	}
	return pointer, ""
}

// ExtractedDir returns the most recently extracted bundle directory.
// Returns "" before the first successful Load or Read.
func (bs *BundleSource) ExtractedDir() string {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	return bs.lastDir
}

// Describe implements config.Source.
func (bs *BundleSource) Describe() string {
	return "bundle:" + bs.pointer
}

// Load implements config.Source.
func (bs *BundleSource) Load(ctx context.Context) (*domain.Config, error) {
	data, _, err := bs.Read(ctx)
	if err != nil {
		return nil, err
	}
	return config.ParseBytes(data, bs.Describe())
}

// Read implements config.Source: extracts the bundle and returns instancez.yaml bytes.
func (bs *BundleSource) Read(ctx context.Context) ([]byte, string, error) {
	return bs.fetchAndRead(ctx, bs.pointer)
}

// Write implements config.Source: bundles are read-only.
func (bs *BundleSource) Write(_ context.Context, _ []byte, _ string) (string, error) {
	return "", fmt.Errorf("bundle source %s is read-only: rebuild and upload via `inz bundle`", bs.pointer)
}

// Watch implements config.Source: polls the bundle ETag (S3) or mtime (local)
// every interval and emits a WatchEvent when a new version is available.
func (bs *BundleSource) Watch(ctx context.Context, interval time.Duration) (<-chan config.WatchEvent, error) {
	if interval <= 0 {
		interval = 60 * time.Second
	}

	// Seed with the current version so we don't reload immediately on first tick.
	lastVer, _ := bs.headVersion(ctx)

	out := make(chan config.WatchEvent, 1)
	go func() {
		defer close(out)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ver, err := bs.headVersion(ctx)
				if err != nil {
					select {
					case out <- config.WatchEvent{Err: fmt.Errorf("bundle poll %s: %w", bs.baseURI, err)}:
					case <-ctx.Done():
						return
					}
					continue
				}
				if ver == lastVer {
					continue
				}
				// Build a pointer with the new version so FetchAndExtract uses it as the
				// directory name (stable across restarts with the same bundle).
				newPointer := bs.baseURI
				if ver != "" {
					newPointer += "#" + ver
				}
				data, newVer, err := bs.fetchAndRead(ctx, newPointer)
				if err != nil {
					select {
					case out <- config.WatchEvent{Err: err}:
					case <-ctx.Done():
						return
					}
					continue
				}
				lastVer = newVer
				select {
				case out <- config.WatchEvent{Data: data, Version: newVer}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// headVersion returns a cheap version token for the current bundle state:
// ETag for S3 bundles, mtime+size for local file bundles.
func (bs *BundleSource) headVersion(ctx context.Context) (string, error) {
	if bs.isS3 {
		store, err := bs.getStore(ctx)
		if err != nil {
			return "", err
		}
		info, err := store.Head(ctx, bs.s3Key)
		if err != nil {
			return "", fmt.Errorf("head %s: %w", bs.baseURI, err)
		}
		return info.ETag, nil
	}
	fi, err := os.Stat(bs.baseURI)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", bs.baseURI, err)
	}
	return fmt.Sprintf("%d-%d", fi.ModTime().UnixNano(), fi.Size()), nil
}

// fetchAndRead calls FetchAndExtract for pointer, records the extracted dir, and
// reads instancez.yaml from the extracted tree.
func (bs *BundleSource) fetchAndRead(ctx context.Context, pointer string) ([]byte, string, error) {
	dir, version, err := app.FetchAndExtract(ctx, pointer, bs.extractParent)
	if err != nil {
		return nil, "", fmt.Errorf("bundle source: fetch %s: %w", pointer, err)
	}
	bs.mu.Lock()
	bs.lastDir = dir
	bs.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(dir, "instancez.yaml"))
	if err != nil {
		return nil, "", fmt.Errorf("bundle source: instancez.yaml not found in %s: %w", pointer, err)
	}
	return data, version, nil
}

// getStore lazily initializes the S3 store using the same credential env vars
// as app.FetchAndExtract (S3_REGION, S3_ENDPOINT, S3_ACCESS_KEY_ID, etc.).
func (bs *BundleSource) getStore(ctx context.Context) (*s3adapter.Store, error) {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if bs.store != nil {
		return bs.store, nil
	}
	store, err := s3adapter.New(ctx, s3adapter.Config{
		Bucket:          bs.s3Bucket,
		Region:          os.Getenv("S3_REGION"),
		Endpoint:        os.Getenv("S3_ENDPOINT"),
		AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
	})
	if err != nil {
		return nil, fmt.Errorf("bundle source: init s3: %w", err)
	}
	bs.store = store
	return store, nil
}

// Verify interface compliance.
var _ config.Source = (*BundleSource)(nil)
