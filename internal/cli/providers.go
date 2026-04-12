package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/saedx1/ultrabase/internal/adapter/gcs"
	"github.com/saedx1/ultrabase/internal/adapter/resend"
	"github.com/saedx1/ultrabase/internal/adapter/s3"
	"github.com/saedx1/ultrabase/internal/adapter/sendgrid"
	"github.com/saedx1/ultrabase/internal/domain"
)

// initEmailProvider creates the email sender based on provider config.
func initEmailProvider(cfg *domain.Config) (domain.EmailSender, error) {
	if cfg.Providers.Email == nil {
		return nil, nil
	}

	switch cfg.Providers.Email.Type {
	case "resend":
		apiKey := os.Getenv("RESEND_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("RESEND_API_KEY not set")
		}
		return resend.New(apiKey), nil

	case "sendgrid":
		apiKey := os.Getenv("SENDGRID_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("SENDGRID_API_KEY not set")
		}
		return sendgrid.New(apiKey), nil

	case "":
		return nil, nil

	default:
		return nil, fmt.Errorf("unsupported email provider: %s (supported: resend, sendgrid)", cfg.Providers.Email.Type)
	}
}

// initStorageProvider creates the object store based on provider config.
func initStorageProvider(ctx context.Context, cfg *domain.Config) (domain.ObjectStore, error) {
	if cfg.Providers.Storage == nil {
		return nil, nil
	}

	switch cfg.Providers.Storage.Type {
	case "s3":
		return newS3Store(ctx, false)

	case "minio":
		return newS3Store(ctx, true)

	case "gcs":
		bucket := os.Getenv("GCS_BUCKET")
		if bucket == "" {
			return nil, fmt.Errorf("GCS_BUCKET not set")
		}
		return gcs.New(ctx, bucket)

	case "local":
		path := os.Getenv("ULTRABASE_LOCAL_STORAGE_PATH")
		if path == "" {
			path = "./uploads"
		}
		return NewLocalStore(path)

	case "":
		return nil, nil

	default:
		return nil, fmt.Errorf("unsupported storage provider: %s (supported: s3, gcs, minio, local)", cfg.Providers.Storage.Type)
	}
}

// initProviders creates both email and storage providers from config.
func initProviders(ctx context.Context, cfg *domain.Config) (domain.EmailSender, domain.ObjectStore, error) {
	email, err := initEmailProvider(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("email provider: %w", err)
	}

	storage, err := initStorageProvider(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("storage provider: %w", err)
	}

	return email, storage, nil
}

// checkStorageHealth validates the storage provider can reach the bucket.
func checkStorageHealth(ctx context.Context, store domain.ObjectStore, cfg *domain.Config, logger *slog.Logger) error {
	for bucketName := range cfg.Storage {
		if err := store.EnsureBucket(ctx, bucketName); err != nil {
			return fmt.Errorf("storage health check failed for bucket %q: %w", bucketName, err)
		}
		logger.Info("storage bucket verified", "bucket", bucketName)
	}
	return nil
}

func newS3Store(ctx context.Context, isMinio bool) (*s3.Store, error) {
	bucket := os.Getenv("S3_BUCKET")
	if bucket == "" {
		return nil, fmt.Errorf("S3_BUCKET not set")
	}

	s3Cfg := s3.Config{
		Bucket:         bucket,
		Region:         os.Getenv("S3_REGION"),
		Endpoint:       os.Getenv("S3_ENDPOINT"),
		AccessKeyID:    os.Getenv("S3_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("S3_SECRET_ACCESS_KEY"),
		ForcePathStyle: isMinio,
	}

	if isMinio && s3Cfg.Endpoint == "" {
		return nil, fmt.Errorf("S3_ENDPOINT must be set for MinIO provider")
	}

	return s3.New(ctx, s3Cfg)
}
