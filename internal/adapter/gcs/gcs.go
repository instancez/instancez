// Package gcs implements domain.ObjectStore using Google Cloud Storage.
package gcs

import (
	"context"
	"fmt"
	"time"

	gcsstorage "cloud.google.com/go/storage"
	"github.com/saedx1/ultrabase/internal/domain"
)

// Store implements domain.ObjectStore using GCS.
type Store struct {
	client *gcsstorage.Client
	bucket string
}

// New creates a new GCS store. Uses Application Default Credentials.
func New(ctx context.Context, bucket string) (*Store, error) {
	client, err := gcsstorage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create GCS client: %w", err)
	}

	return &Store{
		client: client,
		bucket: bucket,
	}, nil
}

func (s *Store) SignUpload(ctx context.Context, key string, contentType string, expiry time.Duration) (string, error) {
	url, err := s.client.Bucket(s.bucket).SignedURL(key, &gcsstorage.SignedURLOptions{
		Method:      "PUT",
		ContentType: contentType,
		Expires:     time.Now().Add(expiry),
	})
	if err != nil {
		return "", fmt.Errorf("sign upload URL: %w", err)
	}
	return url, nil
}

func (s *Store) SignDownload(ctx context.Context, key string, expiry time.Duration) (string, error) {
	url, err := s.client.Bucket(s.bucket).SignedURL(key, &gcsstorage.SignedURLOptions{
		Method:  "GET",
		Expires: time.Now().Add(expiry),
	})
	if err != nil {
		return "", fmt.Errorf("sign download URL: %w", err)
	}
	return url, nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	err := s.client.Bucket(s.bucket).Object(key).Delete(ctx)
	if err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

func (s *Store) EnsureBucket(ctx context.Context, bucket string) error {
	_, err := s.client.Bucket(bucket).Attrs(ctx)
	if err == nil {
		return nil // bucket exists
	}

	if err := s.client.Bucket(bucket).Create(ctx, "", nil); err != nil {
		return fmt.Errorf("create bucket %q: %w", bucket, err)
	}
	return nil
}

// Close releases the GCS client resources.
func (s *Store) Close() error {
	return s.client.Close()
}

var _ domain.ObjectStore = (*Store)(nil)
