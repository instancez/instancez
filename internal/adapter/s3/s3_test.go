package s3

import (
	"testing"

	"github.com/saedx1/ultrabase/internal/domain"
)

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
