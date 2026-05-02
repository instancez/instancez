package app

import (
	"testing"

	"github.com/saedx1/ultrabase/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

func TestOrderDataTables_UsersFirst(t *testing.T) {
	cfg := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "user_id", ForeignKey: &domain.ForeignKey{References: "users.id"}},
				},
			},
			"teams": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
				},
			},
		},
		Data: map[string]domain.TableData{
			"users": {CSVFiles: map[string]string{"demo": "./seeds/users.csv"}},
			"todos": {CSVFiles: map[string]string{"init": "./seeds/todos.csv"}},
			"teams": {CSVFiles: map[string]string{"init": "./seeds/teams.csv"}},
		},
	}

	order := orderDataTables(cfg)
	if len(order) == 0 {
		t.Fatal("expected data tables")
	}
	if order[0] != "users" {
		t.Errorf("first data table should be 'users', got %q", order[0])
	}
}

func TestOrderDataTables_NoUsers(t *testing.T) {
	cfg := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}},
		},
		Data: map[string]domain.TableData{
			"todos": {CSVFiles: map[string]string{"init": "./seeds/todos.csv"}},
		},
	}

	order := orderDataTables(cfg)
	if len(order) != 1 || order[0] != "todos" {
		t.Errorf("expected [todos], got %v", order)
	}
}

func TestDataPasswordHashing(t *testing.T) {
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

	if _, ok := row["password"]; ok {
		t.Error("password should have been removed")
	}

	hashStr, ok := row["password_hash"].(string)
	if !ok || hashStr == "" {
		t.Fatal("password_hash should be set")
	}

	err := bcrypt.CompareHashAndPassword([]byte(hashStr), []byte("secret123"))
	if err != nil {
		t.Errorf("bcrypt verification failed: %v", err)
	}

	err = bcrypt.CompareHashAndPassword([]byte(hashStr), []byte("wrong"))
	if err == nil {
		t.Error("wrong password should fail verification")
	}
}

func TestValidateDataColumns_UnknownColumn(t *testing.T) {
	e := &Engine{
		cfg: &domain.Config{
			Tables: map[string]domain.Table{
				"products": {
					Fields: []domain.Field{
						{Name: "id", Type: "bigserial", PrimaryKey: true},
						{Name: "name", Type: "text"},
					},
				},
			},
		},
	}

	records := []map[string]any{
		{"id": "1", "name": "Widget", "nonexistent": "bad"},
	}

	err := e.validateDataColumns("products", records)
	if err == nil {
		t.Error("expected error for unknown column")
	}
}

func TestValidateDataColumns_ValidColumns(t *testing.T) {
	e := &Engine{
		cfg: &domain.Config{
			Tables: map[string]domain.Table{
				"products": {
					Fields: []domain.Field{
						{Name: "id", Type: "bigserial", PrimaryKey: true},
						{Name: "name", Type: "text"},
					},
				},
			},
		},
	}

	records := []map[string]any{
		{"id": "1", "name": "Widget"},
	}

	err := e.validateDataColumns("products", records)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
