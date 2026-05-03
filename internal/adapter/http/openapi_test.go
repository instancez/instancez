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
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "title", Type: "text", Required: true},
					{Name: "status", Type: "text", Enum: []string{"pending", "done"}},
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

func TestGenerateOpenAPI_RPCPaths(t *testing.T) {
	cfg := &domain.Config{
		Version: 1,
		Project: domain.Project{Name: "rpc"},
		Server:  domain.Server{Port: 8080},
		Functions: map[string]domain.Function{
			"add_numbers": {
				Volatility: "immutable", Returns: domain.FuncReturn{Type: "int"}, ReturnCategory: "scalar",
				Args: []domain.FuncArg{
					{Name: "a", Type: "int", Required: true},
					{Name: "b", Type: "int", Required: true},
				},
			},
			"touch_row": {
				Volatility: "volatile", Returns: domain.FuncReturn{Type: "void"}, ReturnCategory: "void",
			},
			"users_by_status": {
				Volatility: "stable", Returns: domain.FuncReturn{Type: "setof users"}, ReturnCategory: "setof",
				Args: []domain.FuncArg{
					{Name: "target", Type: "text", Required: true},
				},
			},
		},
	}

	spec := GenerateOpenAPI(cfg)
	paths := spec["paths"].(map[string]any)

	// Every function gets a POST path.
	for _, fn := range []string{"add_numbers", "touch_row", "users_by_status"} {
		p, ok := paths["/rest/v1/rpc/"+fn].(map[string]any)
		if !ok {
			t.Errorf("missing POST path for %s", fn)
			continue
		}
		if _, ok := p["post"]; !ok {
			t.Errorf("%s: missing post op", fn)
		}
	}

	// Non-volatile functions additionally expose GET.
	immutable := paths["/rest/v1/rpc/add_numbers"].(map[string]any)
	if _, ok := immutable["get"]; !ok {
		t.Error("immutable function should expose GET")
	}
	stable := paths["/rest/v1/rpc/users_by_status"].(map[string]any)
	if _, ok := stable["get"]; !ok {
		t.Error("stable function should expose GET")
	}
	volatile := paths["/rest/v1/rpc/touch_row"].(map[string]any)
	if _, ok := volatile["get"]; ok {
		t.Error("volatile function must not expose GET")
	}

	// Scalar request body carries the declared arg names as required properties.
	post := immutable["post"].(map[string]any)
	reqBody := post["requestBody"].(map[string]any)
	content := reqBody["content"].(map[string]any)
	appJSON := content["application/json"].(map[string]any)
	schema := appJSON["schema"].(map[string]any)
	props := schema["properties"].(map[string]any)
	if _, ok := props["a"]; !ok {
		t.Error("add_numbers schema missing property a")
	}
	if _, ok := props["b"]; !ok {
		t.Error("add_numbers schema missing property b")
	}
	required, _ := schema["required"].([]string)
	if len(required) != 2 {
		t.Errorf("expected 2 required args, got %v", required)
	}

	// Void function response is 204 with no content.
	vPost := volatile["post"].(map[string]any)
	vResponses := vPost["responses"].(map[string]any)
	if _, ok := vResponses["204"]; !ok {
		t.Error("void function should respond 204")
	}

	// Setof function response is array.
	sPost := stable["post"].(map[string]any)
	sResponses := sPost["responses"].(map[string]any)
	r200 := sResponses["200"].(map[string]any)
	sContent := r200["content"].(map[string]any)
	sJSON := sContent["application/json"].(map[string]any)
	sSchema := sJSON["schema"].(map[string]any)
	if sSchema["type"] != "array" {
		t.Errorf("setof response schema type = %v, want array", sSchema["type"])
	}

	// GET for stable function exposes each arg as a query parameter.
	sGet := stable["get"].(map[string]any)
	params := sGet["parameters"].([]map[string]any)
	if len(params) != 1 || params[0]["name"] != "target" {
		t.Errorf("expected target query param, got %v", params)
	}
}

func TestGenerateOpenAPI_NoAuth(t *testing.T) {
	cfg := &domain.Config{
		Version: 1,
		Project: domain.Project{Name: "Simple"},
		Server:  domain.Server{Port: 8080},
		Tables: map[string]domain.Table{
			"posts": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "title", Type: "text"},
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
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "title", Type: "text", Required: true},
			{Name: "status", Type: "text", Enum: []string{"pending", "done"}},
			{Name: "count", Type: "integer"},
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
