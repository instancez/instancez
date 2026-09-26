package app

import (
	"testing"
	"time"

	"github.com/instancez/instancez/internal/domain"
)

func TestEngineMigrateLockTimeout(t *testing.T) {
	e := NewEngine(&domain.Config{}, domain.OwnerDB{}, domain.RequestDB{}, domain.DefaultRoles())
	if e.migrator.lockTimeout != DefaultMigrateLockTimeout {
		t.Fatalf("default lock timeout = %v, want %v", e.migrator.lockTimeout, DefaultMigrateLockTimeout)
	}

	e = NewEngine(&domain.Config{}, domain.OwnerDB{}, domain.RequestDB{}, domain.DefaultRoles(),
		WithMigrateLockTimeout(750*time.Millisecond))
	if e.migrator.lockTimeout != 750*time.Millisecond {
		t.Fatalf("lock timeout = %v, want 750ms", e.migrator.lockTimeout)
	}
}
