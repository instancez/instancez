package domain

import (
	"context"
	"io"
	"time"
)

// ObjectInfo holds metadata about a stored object.
type ObjectInfo struct {
	Key         string
	Size        int64
	ContentType string
	ETag        string // content version; populated by S3, empty for local storage
}

// ObjectStore is the port for object storage operations (S3, local disk, etc.).
type ObjectStore interface {
	SignUpload(ctx context.Context, key string, contentType string, expiry time.Duration) (string, error)
	SignDownload(ctx context.Context, key string, expiry time.Duration) (string, error)
	Delete(ctx context.Context, key string) error
	EnsureBucket(ctx context.Context, bucket string) error

	Upload(ctx context.Context, key string, r io.Reader, contentType string, size int64) error
	Download(ctx context.Context, key string) (io.ReadCloser, string, error) // returns body, contentType, error
	Copy(ctx context.Context, srcKey, dstKey string) error
	Head(ctx context.Context, key string) (ObjectInfo, error)
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)
}
