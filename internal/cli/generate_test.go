package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saedx1/ultrabase/internal/domain"
)

func TestPgTypeToTS(t *testing.T) {
	tests := []struct {
		pgType string
		want   string
	}{
		{"bigserial", "number"},
		{"serial", "number"},
		{"integer", "number"},
		{"boolean", "boolean"},
		{"text", "string"},
		{"varchar(255)", "string"},
		{"uuid", "string"},
		{"timestamptz", "string"},
		{"jsonb", "Record<string, any>"},
		{"json", "Record<string, any>"},
		{"text[]", "string[]"},
		{"integer[]", "number[]"},
	}
	for _, tt := range tests {
		t.Run(tt.pgType, func(t *testing.T) {
			got := pgTypeToTS(tt.pgType)
			if got != tt.want {
				t.Errorf("pgTypeToTS(%q) = %q, want %q", tt.pgType, got, tt.want)
			}
		})
	}
}

func TestPgTypeToPython(t *testing.T) {
	tests := []struct {
		pgType string
		want   string
	}{
		{"bigserial", "int"},
		{"integer", "int"},
		{"boolean", "bool"},
		{"text", "str"},
		{"real", "float"},
		{"numeric(10,2)", "float"},
		{"jsonb", "dict[str, Any]"},
		{"text[]", "list[str]"},
		{"integer[]", "list[int]"},
		{"uuid", "str"},
	}
	for _, tt := range tests {
		t.Run(tt.pgType, func(t *testing.T) {
			got := pgTypeToPython(tt.pgType)
			if got != tt.want {
				t.Errorf("pgTypeToPython(%q) = %q, want %q", tt.pgType, got, tt.want)
			}
		})
	}
}

func TestPgTypeToGo(t *testing.T) {
	tests := []struct {
		pgType string
		want   string
	}{
		{"bigserial", "int64"},
		{"serial", "int32"},
		{"integer", "int32"},
		{"smallint", "int16"},
		{"boolean", "bool"},
		{"real", "float32"},
		{"numeric(10,2)", "float64"},
		{"text", "string"},
		{"uuid", "string"},
		{"timestamptz", "time.Time"},
		{"date", "time.Time"},
		{"jsonb", "json.RawMessage"},
		{"text[]", "[]string"},
	}
	for _, tt := range tests {
		t.Run(tt.pgType, func(t *testing.T) {
			got := pgTypeToGo(tt.pgType)
			if got != tt.want {
				t.Errorf("pgTypeToGo(%q) = %q, want %q", tt.pgType, got, tt.want)
			}
		})
	}
}

func TestGoFieldName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"id", "ID"},
		{"user_id", "UserID"},
		{"title", "Title"},
		{"created_at", "CreatedAt"},
		{"api_key", "APIKey"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := goFieldName(tt.input)
			if got != tt.want {
				t.Errorf("goFieldName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCapitalize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"todos", "Todos"},
		{"users", "Users"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := capitalize(tt.input)
			if got != tt.want {
				t.Errorf("capitalize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGenerateTypeScriptSDK(t *testing.T) {
	cfg := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields: map[string]domain.Field{
					"id":    {Type: "bigserial", PrimaryKey: true},
					"title": {Type: "text", Required: true},
					"done":  {Type: "boolean"},
				},
			},
		},
	}

	dir := t.TempDir()
	err := generateTypeScriptSDK(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}

	// Check types.ts exists and has content
	typesContent, err := os.ReadFile(filepath.Join(dir, "types.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(typesContent), "export interface Todos") {
		t.Error("types.ts should contain Todos interface")
	}

	// Check client.ts exists
	clientContent, err := os.ReadFile(filepath.Join(dir, "client.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(clientContent), "class UltrabaseClient") {
		t.Error("client.ts should contain UltrabaseClient class")
	}

	// Check index.ts exists
	indexContent, err := os.ReadFile(filepath.Join(dir, "index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(indexContent), "export") {
		t.Error("index.ts should have exports")
	}
}

func TestGenerateTypeScriptSDK_WithFunctions(t *testing.T) {
	min5 := 5.0
	cfg := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields: map[string]domain.Field{
					"id": {Type: "bigserial", PrimaryKey: true},
				},
			},
		},
		Functions: map[string]domain.Function{
			"user_stats": {
				Method: "GET",
				Params: map[string]domain.FuncParam{
					"user_id": {Type: "bigint", Required: true, Min: &min5},
				},
				Returns: domain.FuncReturn{Type: "row"},
			},
			"reset_counts": {
				Method: "POST",
				Returns: domain.FuncReturn{Type: "void"},
			},
		},
	}

	dir := t.TempDir()
	err := generateTypeScriptSDK(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}

	clientContent, err := os.ReadFile(filepath.Join(dir, "client.ts"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(clientContent)

	if !strings.Contains(content, "class FunctionsClient") {
		t.Error("client.ts should contain FunctionsClient class")
	}
	if !strings.Contains(content, "async user_stats") {
		t.Error("client.ts should contain user_stats method")
	}
	if !strings.Contains(content, "async reset_counts") {
		t.Error("client.ts should contain reset_counts method")
	}
	if !strings.Contains(content, "/api/fn/user_stats") {
		t.Error("client.ts should reference function endpoint path")
	}
}

func TestGeneratePythonSDK(t *testing.T) {
	cfg := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields: map[string]domain.Field{
					"id":    {Type: "bigserial", PrimaryKey: true},
					"title": {Type: "text", Required: true},
				},
			},
		},
	}

	dir := t.TempDir()
	err := generatePythonSDK(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}

	typesContent, err := os.ReadFile(filepath.Join(dir, "types.py"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(typesContent), "@dataclass") {
		t.Error("types.py should contain dataclass decorator")
	}
	if !strings.Contains(string(typesContent), "class Todos") {
		t.Error("types.py should contain Todos class")
	}

	clientContent, err := os.ReadFile(filepath.Join(dir, "client.py"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(clientContent), "class UltrabaseClient") {
		t.Error("client.py should contain UltrabaseClient class")
	}
	if !strings.Contains(string(clientContent), "class QueryBuilder") {
		t.Error("client.py should contain QueryBuilder class")
	}

	initContent, err := os.ReadFile(filepath.Join(dir, "__init__.py"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(initContent), "UltrabaseClient") {
		t.Error("__init__.py should export UltrabaseClient")
	}
}

func TestGenerateGoSDK(t *testing.T) {
	cfg := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields: map[string]domain.Field{
					"id":    {Type: "bigserial", PrimaryKey: true},
					"title": {Type: "text", Required: true},
					"done":  {Type: "boolean"},
				},
			},
		},
	}

	dir := t.TempDir()
	err := generateGoSDK(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}

	typesContent, err := os.ReadFile(filepath.Join(dir, "types.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(typesContent), "type Todos struct") {
		t.Error("types.go should contain Todos struct")
	}
	if !strings.Contains(string(typesContent), "int64") {
		t.Error("types.go should map bigserial to int64")
	}

	clientContent, err := os.ReadFile(filepath.Join(dir, "client.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(clientContent), "type Client struct") {
		t.Error("client.go should contain Client struct")
	}
	if !strings.Contains(string(clientContent), "type QueryBuilder struct") {
		t.Error("client.go should contain QueryBuilder struct")
	}
}

func TestGenerateGoSDK_WithFunctions(t *testing.T) {
	cfg := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields: map[string]domain.Field{
					"id": {Type: "bigserial", PrimaryKey: true},
				},
			},
		},
		Functions: map[string]domain.Function{
			"get_stats": {
				Method: "GET",
				Params: map[string]domain.FuncParam{
					"user_id": {Type: "bigint", Required: true},
				},
				Returns: domain.FuncReturn{Type: "rows"},
			},
		},
	}

	dir := t.TempDir()
	err := generateGoSDK(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}

	clientContent, err := os.ReadFile(filepath.Join(dir, "client.go"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(clientContent)

	if !strings.Contains(content, "type FunctionsClient struct") {
		t.Error("client.go should contain FunctionsClient struct")
	}
	if !strings.Contains(content, "func (f *FunctionsClient) GetStats") {
		t.Error("client.go should contain GetStats method")
	}
	if !strings.Contains(content, "/api/fn/get_stats") {
		t.Error("client.go should reference function endpoint path")
	}
}

func TestGeneratePythonSDK_WithFunctions(t *testing.T) {
	cfg := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields: map[string]domain.Field{
					"id": {Type: "bigserial", PrimaryKey: true},
				},
			},
		},
		Functions: map[string]domain.Function{
			"send_report": {
				Method: "POST",
				Params: map[string]domain.FuncParam{
					"email": {Type: "text", Required: true},
					"limit": {Type: "integer"},
				},
				Returns: domain.FuncReturn{Type: "void"},
			},
		},
	}

	dir := t.TempDir()
	err := generatePythonSDK(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}

	clientContent, err := os.ReadFile(filepath.Join(dir, "client.py"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(clientContent)

	if !strings.Contains(content, "class FunctionsClient") {
		t.Error("client.py should contain FunctionsClient class")
	}
	if !strings.Contains(content, "def send_report") {
		t.Error("client.py should contain send_report method")
	}
	if !strings.Contains(content, "/api/fn/send_report") {
		t.Error("client.py should reference function endpoint path")
	}
}

func TestGenerateTSFunctions(t *testing.T) {
	cfg := &domain.Config{
		Functions: map[string]domain.Function{
			"void_fn": {
				Method:  "POST",
				Returns: domain.FuncReturn{Type: "void"},
			},
			"scalar_fn": {
				Method: "GET",
				Params: map[string]domain.FuncParam{
					"id": {Type: "bigint", Required: true},
				},
				Returns: domain.FuncReturn{Type: "scalar"},
			},
		},
	}

	result := generateTSFunctions(cfg)
	if !strings.Contains(result, "class FunctionsClient") {
		t.Error("should contain FunctionsClient")
	}
	if !strings.Contains(result, "async scalar_fn") {
		t.Error("should contain scalar_fn method")
	}
	if !strings.Contains(result, "async void_fn") {
		t.Error("should contain void_fn method")
	}
	if !strings.Contains(result, "{ affected_rows: number }") {
		t.Error("void function should return affected_rows type")
	}
}

func TestSortedMapKeys(t *testing.T) {
	m := map[string]domain.Function{
		"zebra": {},
		"alpha": {},
		"mid":   {},
	}
	keys := sortedMapKeys(m)
	if len(keys) != 3 || keys[0] != "alpha" || keys[1] != "mid" || keys[2] != "zebra" {
		t.Errorf("expected sorted keys, got %v", keys)
	}
}
