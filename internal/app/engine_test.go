package app

import (
	"testing"

	"github.com/saedx1/ultrabase/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

func TestOrderSeedTables_UsersFirst(t *testing.T) {
	cfg := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields: map[string]domain.Field{
					"id":      {Type: "bigserial", PrimaryKey: true},
					"user_id": {ForeignKey: &domain.ForeignKey{References: "users.id"}},
				},
			},
			"teams": {
				Fields: map[string]domain.Field{
					"id": {Type: "bigserial", PrimaryKey: true},
				},
			},
		},
		Seeds: map[string][]map[string]any{
			"users": {{"email": "admin@test.com", "password": "secret"}},
			"todos": {{"id": 1, "title": "Test"}},
			"teams": {{"id": 1, "name": "Acme"}},
		},
	}

	order := orderSeedTables(cfg)
	if len(order) == 0 {
		t.Fatal("expected seed tables")
	}
	if order[0] != "users" {
		t.Errorf("first seed table should be 'users', got %q", order[0])
	}
}

func TestOrderSeedTables_NoUsers(t *testing.T) {
	cfg := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: map[string]domain.Field{"id": {Type: "bigserial", PrimaryKey: true}}},
		},
		Seeds: map[string][]map[string]any{
			"todos": {{"id": 1}},
		},
	}

	order := orderSeedTables(cfg)
	if len(order) != 1 || order[0] != "todos" {
		t.Errorf("expected [todos], got %v", order)
	}
}

func TestSeedPasswordHashing(t *testing.T) {
	// Simulate what applySeeds does for users
	row := map[string]any{
		"email":    "admin@test.com",
		"password": "secret123",
	}

	if pwd, ok := row["password"]; ok {
		if pwdStr, ok := pwd.(string); ok {
			hash, err := bcrypt.GenerateFromPassword([]byte(pwdStr), bcrypt.DefaultCost)
			if err != nil {
				t.Fatalf("hash error: %v", err)
			}
			row["password_hash"] = string(hash)
			delete(row, "password")
		}
	}

	// Verify password_hash is set and password is gone
	if _, ok := row["password"]; ok {
		t.Error("password should have been removed")
	}

	hashStr, ok := row["password_hash"].(string)
	if !ok || hashStr == "" {
		t.Fatal("password_hash should be set")
	}

	// Verify the hash matches the original password
	err := bcrypt.CompareHashAndPassword([]byte(hashStr), []byte("secret123"))
	if err != nil {
		t.Errorf("bcrypt verification failed: %v", err)
	}

	// Wrong password should fail
	err = bcrypt.CompareHashAndPassword([]byte(hashStr), []byte("wrong"))
	if err == nil {
		t.Error("wrong password should fail verification")
	}
}
