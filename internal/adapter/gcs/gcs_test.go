package gcs

import (
	"testing"

	"github.com/saedx1/ultrabase/internal/domain"
)

func TestInterfaceCompliance(t *testing.T) {
	// Verify Store implements domain.ObjectStore at compile time
	var _ domain.ObjectStore = (*Store)(nil)
}
