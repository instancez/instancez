package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/instancez/instancez/internal/adapter/resend"
	"github.com/instancez/instancez/internal/adapter/s3"
	"github.com/instancez/instancez/internal/domain"
)

// storageFactory builds an ObjectStore from a storage provider config block.
type storageFactory func(ctx context.Context, p *domain.StorageProvider) (domain.ObjectStore, error)

// emailFactory builds an EmailSender from an email provider config block.
type emailFactory func(p *domain.EmailProvider) (domain.EmailSender, error)

var (
	storageRegistry = map[string]storageFactory{}
	emailRegistry   = map[string]emailFactory{}
)

func registerStorage(name string, f storageFactory) { storageRegistry[name] = f }
func registerEmail(name string, f emailFactory)     { emailRegistry[name] = f }

// registerBuiltins registers the providers shipped in the box. It is idempotent
// (map overwrite) so tests and the init paths can call it freely. A new built-in
// provider adds one line here; an external build can call registerStorage /
// registerEmail before initProviders to plug in its own backend.
func registerBuiltins() {
	registerStorage("s3", func(ctx context.Context, p *domain.StorageProvider) (domain.ObjectStore, error) {
		return newS3Store(ctx, p)
	})
	registerStorage("local", func(_ context.Context, p *domain.StorageProvider) (domain.ObjectStore, error) {
		path := p.Path
		if path == "" {
			path = "./uploads"
		}
		return NewLocalStore(path, os.Getenv("INSTANCEZ_STORAGE_KEY_PREFIX"))
	})
	registerEmail("resend", func(p *domain.EmailProvider) (domain.EmailSender, error) {
		if p.APIKey == "" {
			return nil, fmt.Errorf("INSTANCEZ_RESEND_API_KEY not set (required for resend provider)")
		}
		return resend.New(p.APIKey), nil
	})
}

// sortedKeys returns the registry's provider names as a sorted, comma-joined
// list, used to build the "supported: ..." half of an unknown-provider error.
func sortedKeys[T any](m map[string]T) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// initEmailProvider creates the email sender based on provider config.
func initEmailProvider(cfg *domain.Config) (domain.EmailSender, error) {
	if cfg.Providers.Email == nil {
		return nil, nil
	}
	if cfg.Providers.Email.Type == "" {
		return nil, nil
	}
	registerBuiltins()
	f, ok := emailRegistry[cfg.Providers.Email.Type]
	if !ok {
		return nil, fmt.Errorf("unsupported email provider: %s (supported: %s)", cfg.Providers.Email.Type, sortedKeys(emailRegistry))
	}
	return f(cfg.Providers.Email)
}

// initStorageProvider creates the object store based on provider config.
func initStorageProvider(ctx context.Context, cfg *domain.Config) (domain.ObjectStore, error) {
	if cfg.Providers.Storage == nil {
		return nil, nil
	}

	if cfg.Providers.Storage.Type == "" {
		return nil, nil
	}
	registerBuiltins()
	f, ok := storageRegistry[cfg.Providers.Storage.Type]
	if !ok {
		return nil, fmt.Errorf("unsupported storage provider: %s (supported: %s)", cfg.Providers.Storage.Type, sortedKeys(storageRegistry))
	}
	return f(ctx, cfg.Providers.Storage)
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
