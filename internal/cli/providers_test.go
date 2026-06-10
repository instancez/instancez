package cli

import (
	"testing"

	"github.com/saedx1/instancez/internal/domain"
)

func TestInitEmailProvider_Nil(t *testing.T) {
	cfg := &domain.Config{}
	sender, err := initEmailProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if sender != nil {
		t.Error("expected nil sender when no provider configured")
	}
}

func TestInitEmailProvider_EmptyType(t *testing.T) {
	cfg := &domain.Config{
		Providers: domain.Providers{
			Email: &domain.EmailProvider{Type: ""},
		},
	}
	sender, err := initEmailProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if sender != nil {
		t.Error("expected nil sender for empty type")
	}
}

func TestInitEmailProvider_UnsupportedType(t *testing.T) {
	cfg := &domain.Config{
		Providers: domain.Providers{
			Email: &domain.EmailProvider{Type: "mailchimp"},
		},
	}
	_, err := initEmailProvider(cfg)
	if err == nil {
		t.Error("expected error for unsupported type")
	}
}

func TestInitEmailProvider_ResendNoKey(t *testing.T) {
	cfg := &domain.Config{
		Providers: domain.Providers{
			Email: &domain.EmailProvider{Type: "resend"},
		},
	}
	// RESEND_API_KEY not set
	t.Setenv("RESEND_API_KEY", "")
	_, err := initEmailProvider(cfg)
	if err == nil {
		t.Error("expected error when RESEND_API_KEY not set")
	}
}

func TestInitEmailProvider_ResendWithKey(t *testing.T) {
	cfg := &domain.Config{
		Providers: domain.Providers{
			Email: &domain.EmailProvider{Type: "resend"},
		},
	}
	t.Setenv("RESEND_API_KEY", "re_test_key")
	sender, err := initEmailProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if sender == nil {
		t.Error("expected non-nil sender")
	}
}

func TestInitEmailProvider_SendGridWithKey(t *testing.T) {
	cfg := &domain.Config{
		Providers: domain.Providers{
			Email: &domain.EmailProvider{Type: "sendgrid"},
		},
	}
	t.Setenv("SENDGRID_API_KEY", "SG.test_key")
	sender, err := initEmailProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if sender == nil {
		t.Error("expected non-nil sender")
	}
}

func TestInitStorageProvider_Nil(t *testing.T) {
	cfg := &domain.Config{}
	store, err := initStorageProvider(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if store != nil {
		t.Error("expected nil store when no provider configured")
	}
}

func TestInitStorageProvider_UnsupportedType(t *testing.T) {
	cfg := &domain.Config{
		Providers: domain.Providers{
			Storage: &domain.StorageProvider{Type: "azure"},
		},
	}
	_, err := initStorageProvider(t.Context(), cfg)
	if err == nil {
		t.Error("expected error for unsupported type")
	}
}

func TestInitStorageProvider_Local(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ULTRABASE_LOCAL_STORAGE_PATH", dir)

	cfg := &domain.Config{
		Providers: domain.Providers{
			Storage: &domain.StorageProvider{Type: "local"},
		},
	}
	store, err := initStorageProvider(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if store == nil {
		t.Error("expected non-nil store")
	}
}

