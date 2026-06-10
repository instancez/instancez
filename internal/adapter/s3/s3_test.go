package s3

import (
	"context"
	"testing"

	"github.com/saedx1/instancez/internal/domain"
)

func TestEnsureBucket_IsNoOp(t *testing.T) {
	s := &Store{} // nil client; a no-op must not touch it
	if err := s.EnsureBucket(context.Background(), "avatars"); err != nil {
		t.Fatalf("expected no-op nil, got %v", err)
	}
}

func TestKeyPrefix_PrependAndStrip(t *testing.T) {
	s := &Store{keyPrefix: "app123"}
	if got := s.fullKey("avatars/tok"); got != "app123/avatars/tok" {
		t.Fatalf("fullKey = %q", got)
	}
	if got := s.stripKey("app123/avatars/tok"); got != "avatars/tok" {
		t.Fatalf("stripKey = %q", got)
	}
	z := &Store{}
	if got := z.fullKey("avatars/tok"); got != "avatars/tok" {
		t.Fatalf("no-prefix fullKey = %q", got)
	}
	if got := z.stripKey("avatars/tok"); got != "avatars/tok" {
		t.Fatalf("no-prefix stripKey = %q", got)
	}
}

func TestInterfaceCompliance(t *testing.T) {
	// Verify Store implements domain.ObjectStore at compile time
	var _ domain.ObjectStore = (*Store)(nil)
}

func TestConfig(t *testing.T) {
	cfg := Config{
		Bucket:          "test-bucket",
		Region:          "us-east-1",
		Endpoint:        "http://localhost:9000",
		AccessKeyID:     "testkey",
		SecretAccessKey: "testsecret",
	}

	if cfg.Bucket != "test-bucket" {
		t.Errorf("bucket = %q, want test-bucket", cfg.Bucket)
	}
	if cfg.Region != "us-east-1" {
		t.Errorf("region = %q, want us-east-1", cfg.Region)
	}
	if cfg.Endpoint != "http://localhost:9000" {
		t.Errorf("endpoint = %q, want http://localhost:9000", cfg.Endpoint)
	}
}

