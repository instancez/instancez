package http

import (
	"testing"

	"github.com/saedx1/ultrabase/internal/domain"
)

func TestGenerateOpenAPI_Structure(t *testing.T) {
	cfg := &domain.Config{
		Version: 1,
		Project: domain.Project{Name: "Test App", Description: "Test"},
		Server:  domain.Server{Port: 8080},
		Auth: &domain.Auth{
			JWTExpiry:     "15m",
			RefreshTokens: true,
			Email:         &domain.AuthEmail{VerifyEmail: true},
		},
		Tables: map[string]domain.Table{
			"todos": {
				Fields: map[string]domain.Field{
					"id":     {Type: "bigserial", PrimaryKey: true},
					"title":  {Type: "text", Required: true},
					"status": {Type: "text", Enum: []string{"pending", "done"}},
				},
			},
		},
		Storage: map[string]domain.Bucket{
			"avatars": {MaxSize: "2MB", Public: true},
		},
	}

	spec := GenerateOpenAPI(cfg)

	// Check top-level structure
	if spec["openapi"] != "3.0.3" {
		t.Errorf("openapi version = %v, want 3.0.3", spec["openapi"])
	}

	info, ok := spec["info"].(map[string]any)
	if !ok {
		t.Fatal("missing info")
	}
	if info["title"] != "Test App" {
		t.Errorf("info.title = %v, want 'Test App'", info["title"])
	}

	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatal("missing paths")
	}

	// Check table CRUD endpoints exist (PostgREST-compatible path)
	if _, ok := paths["/rest/v1/todos"]; !ok {
		t.Error("missing /rest/v1/todos path")
	}

	// Check auth endpoints (GoTrue-compatible paths)
	if _, ok := paths["/auth/v1/signup"]; !ok {
		t.Error("missing /auth/v1/signup path")
	}
	if _, ok := paths["/auth/v1/token"]; !ok {
		t.Error("missing /auth/v1/token path")
	}
	if _, ok := paths["/auth/v1/user"]; !ok {
		t.Error("missing /auth/v1/user path")
	}
	if _, ok := paths["/auth/v1/verify"]; !ok {
		t.Error("missing /auth/v1/verify path")
	}

	// Check storage endpoints
	if _, ok := paths["/api/storage/avatars/sign"]; !ok {
		t.Error("missing /api/storage/avatars/sign path")
	}

	// Check health endpoints
	if _, ok := paths["/live"]; !ok {
		t.Error("missing /live path")
	}
	if _, ok := paths["/health"]; !ok {
		t.Error("missing /health path")
	}
	if _, ok := paths["/ready"]; !ok {
		t.Error("missing /ready path")
	}

	// Check admin endpoints
	if _, ok := paths["/api/_admin/events"]; !ok {
		t.Error("missing /api/_admin/events path")
	}
	if _, ok := paths["/api/_admin/status"]; !ok {
		t.Error("missing /api/_admin/status path")
	}

	// Check schemas
	components, ok := spec["components"].(map[string]any)
	if !ok {
		t.Fatal("missing components")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatal("missing schemas")
	}
	if _, ok := schemas["todos"]; !ok {
		t.Error("missing todos schema")
	}
	if _, ok := schemas["todos_input"]; !ok {
		t.Error("missing todos_input schema")
	}
}

func TestGenerateOpenAPI_NoAuth(t *testing.T) {
	cfg := &domain.Config{
		Version: 1,
		Project: domain.Project{Name: "Simple"},
		Server:  domain.Server{Port: 8080},
		Tables: map[string]domain.Table{
			"posts": {
				Fields: map[string]domain.Field{
					"id":    {Type: "bigserial", PrimaryKey: true},
					"title": {Type: "text"},
				},
			},
		},
	}

	spec := GenerateOpenAPI(cfg)
	paths := spec["paths"].(map[string]any)

	// Auth endpoints should NOT exist
	if _, ok := paths["/auth/v1/signup"]; ok {
		t.Error("/auth/v1/signup should not exist when auth is nil")
	}
}

func TestGenerateTableSchema(t *testing.T) {
	table := domain.Table{
		Fields: map[string]domain.Field{
			"id":     {Type: "bigserial", PrimaryKey: true},
			"title":  {Type: "text", Required: true},
			"status": {Type: "text", Enum: []string{"pending", "done"}},
			"count":  {Type: "integer"},
		},
	}

	schema := generateTableSchema("todos", table)
	props := schema["properties"].(map[string]any)

	if len(props) != 4 {
		t.Errorf("expected 4 properties, got %d", len(props))
	}

	// Check type mapping
	idProp := props["id"].(map[string]any)
	if idProp["type"] != "integer" {
		t.Errorf("id type = %v, want integer", idProp["type"])
	}

	statusProp := props["status"].(map[string]any)
	if statusProp["enum"] == nil {
		t.Error("status should have enum values")
	}
}
