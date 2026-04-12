package sendgrid

import (
	"testing"

	"github.com/saedx1/ultrabase/internal/domain"
)

func TestInterfaceCompliance(t *testing.T) {
	var _ domain.EmailSender = (*Sender)(nil)
}

func TestNew(t *testing.T) {
	s := New("SG.test_api_key")
	if s == nil {
		t.Fatal("expected non-nil sender")
	}
	if s.apiKey != "SG.test_api_key" {
		t.Errorf("apiKey = %q, want SG.test_api_key", s.apiKey)
	}
	if s.client == nil {
		t.Error("expected non-nil HTTP client")
	}
}
