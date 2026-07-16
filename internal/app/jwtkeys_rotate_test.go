package app

import (
	"context"
	"strings"
	"testing"
)

func TestRotateActive_RetiresThenInserts(t *testing.T) {
	db := &fakeDB{}
	m := NewJWTKeyManager(db)

	key, err := m.RotateActive(context.Background())
	if err != nil {
		t.Fatalf("RotateActive: %v", err)
	}
	if key == nil || key.KID == "" || key.PrivateKey == nil {
		t.Fatal("rotate returned no usable key")
	}

	// Two writes: a retire UPDATE, then an INSERT, in that order.
	if len(db.execs) != 2 {
		t.Fatalf("want 2 exec calls, got %d: %v", len(db.execs), db.execs)
	}
	if !strings.Contains(strings.ToUpper(db.execs[0]), "UPDATE") || !strings.Contains(db.execs[0], "retired_at") {
		t.Fatalf("first exec is not a retire UPDATE: %q", db.execs[0])
	}
	if !strings.Contains(strings.ToUpper(db.execs[1]), "INSERT") {
		t.Fatalf("second exec is not an INSERT: %q", db.execs[1])
	}

	// The new key is now active without re-reading the DB.
	got, err := m.Active(context.Background())
	if err != nil {
		t.Fatalf("Active after rotate: %v", err)
	}
	if got.KID != key.KID {
		t.Fatalf("active kid = %q, want %q", got.KID, key.KID)
	}
}

func TestRotateActive_NoDB(t *testing.T) {
	m := &JWTKeyManager{byKID: map[string]*JWTKey{}}
	if _, err := m.RotateActive(context.Background()); err == nil {
		t.Fatal("expected error when manager has no db")
	}
}

// TestRotateActive_RollsBackOnInsertFailure proves the retire and insert are
// atomic: when the INSERT fails, the retire UPDATE must not commit, so the
// table is never left with every key retired and none active. The in-memory
// active pointer must also stay unchanged.
func TestRotateActive_RollsBackOnInsertFailure(t *testing.T) {
	db := &fakeDB{failOnStatementContaining: "INSERT"}
	m := NewJWTKeyManager(db)

	if _, err := m.RotateActive(context.Background()); err == nil {
		t.Fatal("expected error when the INSERT fails")
	}

	if db.committedStatements != 0 {
		t.Fatalf("want 0 committed statements after rollback, got %d", db.committedStatements)
	}
	if m.active != nil {
		t.Fatalf("in-memory active key must stay nil after a failed rotation, got %q", m.active.KID)
	}
}
