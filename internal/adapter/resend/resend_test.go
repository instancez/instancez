package resend

import (
	"testing"

	"github.com/saedx1/instancez/internal/domain"
)

func TestInterfaceCompliance(t *testing.T) {
	var _ domain.EmailSender = (*Sender)(nil)
}

func TestNew(t *testing.T) {
	s := New("re_test_key")
	if s == nil {
		t.Fatal("expected non-nil sender")
	}
	if s.apiKey != "re_test_key" {
		t.Errorf("apiKey = %q, want re_test_key", s.apiKey)
	}
	if s.client == nil {
		t.Error("expected non-nil HTTP client")
	}
}
