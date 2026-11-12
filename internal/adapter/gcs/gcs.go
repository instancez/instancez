// Package gcs implements domain.ObjectStore using Google Cloud Storage.
package gcs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/instancez/instancez/internal/domain"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// Store implements domain.ObjectStore on a single GCS bucket using ADC.
type Store struct {
	client    *storage.Client
	bucket    *storage.BucketHandle
	keyPrefix string
	// signerEmail forces the service account used for signing; empty means auto-detect.
	signerEmail string
	// signBytes overrides the IAM signBlob call (tests only).
	signBytes func([]byte) ([]byte, error)
}

// Config holds GCS connection configuration. Credentials come from ADC only.
type Config struct {
	Bucket      string
	KeyPrefix   string // optional prefix prepended to all object keys
	SignerEmail string // optional; service account that signs URLs via IAM signBlob
}

// New creates a store using Application Default Credentials.
func New(ctx context.Context, cfg Config) (*Store, error) {
	return newStore(ctx, cfg)
}

func newStore(ctx context.Context, cfg Config, opts ...option.ClientOption) (*Store, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("gcs: bucket is required")
	}
	client, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create GCS client: %w", err)
	}
	return &Store{
		client:      client,
		bucket:      client.Bucket(cfg.Bucket),
		keyPrefix:   cfg.KeyPrefix,
		signerEmail: cfg.SignerEmail,
	}, nil
}

func (s *Store) fullKey(key string) string {
	if s.keyPrefix == "" {
		return key
	}
	return s.keyPrefix + "/" + key
}

func (s *Store) stripKey(key string) string {
	if s.keyPrefix == "" {
		return key
	}
	return strings.TrimPrefix(key, s.keyPrefix+"/")
}

// objectName rejects keys GCS cannot store; S3 would fail them server-side too.
func (s *Store) objectName(key string) (string, error) {
	if key == "" {
		return "", errors.New("empty object key")
	}
	if strings.ContainsRune(key, 0) {
		return "", errors.New("object key contains NUL")
	}
	return s.fullKey(key), nil
}

func (s *Store) sign(ctx context.Context, key, method string, expiry time.Duration, o storage.SignedURLOptions) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	name, err := s.objectName(key)
	if err != nil {
		return "", err
	}
	o.Scheme = storage.SigningSchemeV4
	o.Method = method
	// The 999ms keeps X-Goog-Expires from truncating one second short.
	o.Expires = time.Now().Add(expiry + 999*time.Millisecond)
	o.GoogleAccessID = s.signerEmail
	o.SignBytes = s.signBytes
	type result struct {
		url string
		err error
	}
	done := make(chan result, 1)
	// The library's signBlob call ignores ctx; abandon it on cancel, it ends on its own retry deadline.
	go func() {
		u, err := s.bucket.SignedURL(name, &o)
		done <- result{u, err}
	}()
	select {
	case r := <-done:
		return r.url, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (s *Store) SignUpload(ctx context.Context, key string, contentType string, expiry time.Duration) (string, error) {
	u, err := s.sign(ctx, key, "PUT", expiry, storage.SignedURLOptions{ContentType: contentType})
	if err != nil {
		return "", fmt.Errorf("presign upload: %w", err)
	}
	return u, nil
}

func (s *Store) SignDownload(ctx context.Context, key string, expiry time.Duration, opts domain.DownloadOptions) (string, error) {
	q := url.Values{}
	if opts.ContentType != "" {
		q.Set("response-content-type", opts.ContentType)
	}
	if opts.ContentDisposition != "" {
		q.Set("response-content-disposition", opts.ContentDisposition)
	}
	if opts.CacheControl != "" {
		q.Set("response-cache-control", opts.CacheControl)
	}
	u, err := s.sign(ctx, key, "GET", expiry, storage.SignedURLOptions{QueryParameters: q})
	if err != nil {
		return "", fmt.Errorf("presign download: %w", err)
	}
	return u, nil
}

// Delete is idempotent like S3: a missing object is not an error.
func (s *Store) Delete(ctx context.Context, key string) error {
	name, err := s.objectName(key)
	if err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	if err := s.bucket.Object(name).Delete(ctx); err != nil && !errors.Is(err, storage.ErrObjectNotExist) {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

// EnsureBucket is a no-op: all objects live in the single configured bucket
// (provisioned out of band); logical buckets are key prefixes.
func (s *Store) EnsureBucket(_ context.Context, _ string) error { return nil }

func (s *Store) Upload(ctx context.Context, key string, r io.Reader, contentType string, size int64) error {
	name, err := s.objectName(key)
	if err != nil {
		return fmt.Errorf("upload object: %w", err)
	}
	// Cancelling before Close aborts the write so a failed upload leaves no object.
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := s.bucket.Object(name).NewWriter(wctx)
	w.ContentType = contentType
	n, err := io.Copy(w, r)
	if err == nil && size > 0 && n != size {
		err = fmt.Errorf("body is %d bytes, declared %d", n, size)
	}
	if err != nil {
		cancel()
		_ = w.Close()
		return fmt.Errorf("upload object: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("upload object: %w", err)
	}
	return nil
}

func (s *Store) Download(ctx context.Context, key string) (io.ReadCloser, string, error) {
	name, err := s.objectName(key)
	if err != nil {
		return nil, "", fmt.Errorf("download object: %w", err)
	}
	rd, err := s.bucket.Object(name).NewReader(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("download object: %w", err)
	}
	return rd, rd.Attrs.ContentType, nil
}

func (s *Store) Copy(ctx context.Context, srcKey, dstKey string) error {
	src, err := s.objectName(srcKey)
	if err != nil {
		return fmt.Errorf("copy object: %w", err)
	}
	dst, err := s.objectName(dstKey)
	if err != nil {
		return fmt.Errorf("copy object: %w", err)
	}
	if _, err := s.bucket.Object(dst).CopierFrom(s.bucket.Object(src)).Run(ctx); err != nil {
		return fmt.Errorf("copy object: %w", err)
	}
	return nil
}

func (s *Store) Head(ctx context.Context, key string) (domain.ObjectInfo, error) {
	name, err := s.objectName(key)
	if err != nil {
		return domain.ObjectInfo{}, fmt.Errorf("head object: %w", err)
	}
	a, err := s.bucket.Object(name).Attrs(ctx)
	if err != nil {
		return domain.ObjectInfo{}, fmt.Errorf("head object: %w", err)
	}
	// Return the caller's logical key, not the prefixed storage key.
	return domain.ObjectInfo{Key: key, Size: a.Size, ContentType: a.ContentType, ETag: a.Etag}, nil
}

func (s *Store) List(ctx context.Context, prefix string) ([]domain.ObjectInfo, error) {
	it := s.bucket.Objects(ctx, &storage.Query{Prefix: s.fullKey(prefix)})
	var items []domain.ObjectInfo
	for {
		a, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return items, nil
		}
		if err != nil {
			return nil, fmt.Errorf("list objects: %w", err)
		}
		items = append(items, domain.ObjectInfo{Key: s.stripKey(a.Name), Size: a.Size})
	}
}

// Verify interface compliance.
var _ domain.ObjectStore = (*Store)(nil)
