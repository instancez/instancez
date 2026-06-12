package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/instancez/instancez/internal/adapter/resend"
	"github.com/instancez/instancez/internal/adapter/s3"
	"github.com/instancez/instancez/internal/domain"
)

// initEmailProvider creates the email sender based on provider config.
func initEmailProvider(cfg *domain.Config) (domain.EmailSender, error) {
	if cfg.Providers.Email == nil {
		return nil, nil
	}
	switch cfg.Providers.Email.Type {
	case "resend":
		if cfg.Providers.Email.APIKey == "" {
			return nil, fmt.Errorf("INSTANCEZ_RESEND_API_KEY not set (required for resend provider)")
		}
		return resend.New(cfg.Providers.Email.APIKey), nil
	case "":
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported email provider: %s (supported: resend)", cfg.Providers.Email.Type)
	}
}

// initStorageProvider creates the object store based on provider config.
func initStorageProvider(ctx context.Context, cfg *domain.Config) (domain.ObjectStore, error) {
	if cfg.Providers.Storage == nil {
		return nil, nil
	}

	switch cfg.Providers.Storage.Type {
	case "s3":
		return newS3Store(ctx, cfg.Providers.Storage)
	case "local":
		path := cfg.Providers.Storage.Path
		if path == "" {
			path = "./uploads"
		}
		return NewLocalStore(path, os.Getenv("INSTANCEZ_STORAGE_KEY_PREFIX"))
	case "":
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported storage provider: %s (supported: s3, local)", cfg.Providers.Storage.Type)
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

func newS3Store(ctx context.Context, p *domain.StorageProvider) (*s3.Store, error) {
	if p.Bucket == "" {
		return nil, fmt.Errorf("INSTANCEZ_S3_BUCKET not set (required for s3 provider)")
	}
	s3Cfg := s3.Config{
		Bucket:          p.Bucket,
		Region:          p.Region,
		Endpoint:        p.Endpoint,
		AccessKeyID:     p.AccessKeyID,
		SecretAccessKey: p.SecretAccessKey,
		KeyPrefix:       os.Getenv("INSTANCEZ_STORAGE_KEY_PREFIX"),
	}
	return s3.New(ctx, s3Cfg)
}

