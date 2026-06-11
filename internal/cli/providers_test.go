package cli

import (
	"testing"

	"github.com/instancez/instancez/internal/domain"
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
	// APIKey is empty in struct — should error regardless of env var
	_, err := initEmailProvider(cfg)
	if err == nil {
		t.Error("expected error when APIKey is empty in struct")
	}
}

func TestInitEmailProvider_ResendWithKey(t *testing.T) {
	cfg := &domain.Config{
		Providers: domain.Providers{
			Email: &domain.EmailProvider{Type: "resend", APIKey: "re_test_key"},
		},
	}
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
			Email: &domain.EmailProvider{Type: "sendgrid", APIKey: "SG.test_key"},
		},
	}
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
	cfg := &domain.Config{
		Providers: domain.Providers{
			Storage: &domain.StorageProvider{Type: "local", Path: dir},
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

func TestInitEmailProvider_Resend_UsesStructField(t *testing.T) {
	cfg := &domain.Config{
		Providers: domain.Providers{
			Email: &domain.EmailProvider{
				Type:   "resend",
				APIKey: "re_struct_key",
			},
		},
	}
	sender, err := initEmailProvider(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sender == nil {
		t.Fatal("expected non-nil sender")
	}
}

func TestInitEmailProvider_Resend_MissingStructKey(t *testing.T) {
	cfg := &domain.Config{
		Providers: domain.Providers{
			Email: &domain.EmailProvider{Type: "resend", APIKey: ""},
		},
	}
	// Env var should be ignored — struct field is what matters now
	t.Setenv("RESEND_API_KEY", "re_should_be_ignored")
	_, err := initEmailProvider(cfg)
	if err == nil {
		t.Fatal("expected error when APIKey is empty in struct")
	}
}

func TestInitStorageProvider_Local_UsesStructPath(t *testing.T) {
	dir := t.TempDir()
	cfg := &domain.Config{
		Providers: domain.Providers{
			Storage: &domain.StorageProvider{
				Type: "local",
				Path: dir,
			},
		},
	}
	store, err := initStorageProvider(t.Context(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}
