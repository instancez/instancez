package domain

import (
	"context"
	"time"
)

// ObjectStore is the port for object storage operations (S3, GCS, etc.).
type ObjectStore interface {
	SignUpload(ctx context.Context, key string, contentType string, expiry time.Duration) (string, error)
	SignDownload(ctx context.Context, key string, expiry time.Duration) (string, error)
	Delete(ctx context.Context, key string) error
	EnsureBucket(ctx context.Context, bucket string) error
}
