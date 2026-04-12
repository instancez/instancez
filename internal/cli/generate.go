package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	ultrahttp "github.com/saedx1/ultrabase/internal/adapter/http"
	"github.com/saedx1/ultrabase/internal/config"
	"github.com/saedx1/ultrabase/internal/domain"
	"github.com/spf13/cobra"
)

func newGenerateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate SDK or OpenAPI spec",
	}

	cmd.AddCommand(
		newGenerateOpenAPICmd(),
		newGenerateSDKCmd(),
	)

	return cmd
}

func newGenerateOpenAPICmd() *cobra.Command {
	var (
		configPath string
		output     string
	)

	cmd := &cobra.Command{
		Use:   "openapi",
		Short: "Generate OpenAPI 3.0 spec to file",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}

			errs := config.Validate(cfg)
			if errs != nil {
				return errs
			}

			spec := ultrahttp.GenerateOpenAPI(cfg)

			data, err := json.MarshalIndent(spec, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal: %w", err)
			}

			if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(output, data, 0o644); err != nil {
				return err
			}

			fmt.Printf("  \u2713 Generated OpenAPI 3.0 spec\n")
			fmt.Printf("  \u2713 Written to %s\n", output)
			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "ultrabase.yaml", "config file path")
	cmd.Flags().StringVar(&output, "output", "openapi.json", "output file path")
	return cmd
}

func newGenerateSDKCmd() *cobra.Command {
	var (
		configPath string
		lang       string
		output     string
	)

	cmd := &cobra.Command{
		Use:   "sdk",
		Short: "Generate typed client SDK",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}

			errs := config.Validate(cfg)
			if errs != nil {
				return errs
			}

			switch lang {
			case "typescript", "ts":
				return generateTypeScriptSDK(cfg, output)
			case "python", "py":
				return generatePythonSDK(cfg, output)
			case "go":
				return generateGoSDK(cfg, output)
			default:
				return fmt.Errorf("unsupported language: %s (supported: typescript, python, go)", lang)
			}
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "ultrabase.yaml", "config file path")
	cmd.Flags().StringVar(&lang, "lang", "typescript", "target language (typescript|python|go)")
	cmd.Flags().StringVar(&output, "output", "./sdk", "output directory")
	return cmd
}

// SDK generation stubs — these will produce real output.
func generateTypeScriptSDK(cfg *domain.Config, output string) error {
	if err := os.MkdirAll(output, 0o755); err != nil {
		return err
	}

	// Generate types
	var types string
	for name, table := range cfg.Tables {
		types += fmt.Sprintf("export interface %s {\n", capitalize(name))
		for fname, field := range table.Fields {
			tsType := pgTypeToTS(field.Type)
			if field.ForeignKey != nil {
				tsType = "number"
			}
			optional := ""
			if !field.Required && !field.PrimaryKey {
				optional = "?"
			}
			types += fmt.Sprintf("  %s%s: %s;\n", fname, optional, tsType)
		}
		types += "}\n\n"
	}

	if err := os.WriteFile(filepath.Join(output, "types.ts"), []byte(types), 0o644); err != nil {
		return err
	}

	// Generate client
	client := `import type { ` + importList(cfg) + ` } from './types';

export class UltrabaseClient {
  private baseUrl: string;
  private token: string | null = null;

  constructor(baseUrl: string) {
    this.baseUrl = baseUrl;
  }

  auth = {
    signUp: async (email: string, password: string, extra?: Record<string, any>) => {
      return this.post('/api/auth/signup', { email, password, extra });
    },
    signIn: async (email: string, password: string) => {
      const result = await this.post('/api/auth/login', { email, password });
      if (result.access_token) this.token = result.access_token;
      return result;
    },
    signOut: async () => {
      await this.post('/api/auth/logout', {});
      this.token = null;
    },
    user: async () => {
      return this.get('/api/auth/me');
    },
  };

  from(table: string) {
    return new QueryBuilder(this.baseUrl, table, this.token);
  }

  private async get(path: string) {
    const res = await fetch(this.baseUrl + path, {
      headers: this.headers(),
    });
    return res.json();
  }

  private async post(path: string, body: any) {
    const res = await fetch(this.baseUrl + path, {
      method: 'POST',
      headers: { ...this.headers(), 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    return res.json();
  }

  private headers(): Record<string, string> {
    const h: Record<string, string> = {};
    if (this.token) h['Authorization'] = 'Bearer ' + this.token;
    return h;
  }
}

class QueryBuilder {
  private url: string;
  private token: string | null;
  private params: URLSearchParams = new URLSearchParams();
  private preferHeaders: string[] = [];

  constructor(baseUrl: string, table: string, token: string | null) {
    this.url = baseUrl + '/api/' + table;
    this.token = token;
  }

  select(fields: string) { this.params.set('select', fields); return this; }
  eq(col: string, val: string) { this.params.set(col, 'eq.' + val); return this; }
  neq(col: string, val: string) { this.params.set(col, 'neq.' + val); return this; }
  gt(col: string, val: string) { this.params.set(col, 'gt.' + val); return this; }
  gte(col: string, val: string) { this.params.set(col, 'gte.' + val); return this; }
  lt(col: string, val: string) { this.params.set(col, 'lt.' + val); return this; }
  lte(col: string, val: string) { this.params.set(col, 'lte.' + val); return this; }
  order(col: string, opts?: { ascending?: boolean }) {
    const dir = opts?.ascending === false ? '.desc' : '.asc';
    this.params.set('order', col + dir);
    return this;
  }
  limit(n: number) { this.params.set('limit', String(n)); return this; }
  offset(n: number) { this.params.set('offset', String(n)); return this; }

  async execute() {
    const url = this.url + '?' + this.params.toString();
    const headers: Record<string, string> = {};
    if (this.token) headers['Authorization'] = 'Bearer ' + this.token;
    if (this.preferHeaders.length) headers['Prefer'] = this.preferHeaders.join(', ');
    const res = await fetch(url, { headers });
    return { data: await res.json(), count: parseContentRange(res.headers.get('Content-Range')) };
  }
}

function parseContentRange(header: string | null): number | null {
  if (!header) return null;
  const match = header.match(/\/(\d+)$/);
  return match ? parseInt(match[1]) : null;
}
`

	// Append function methods if any
	if len(cfg.Functions) > 0 {
		client = strings.TrimRight(client, "\n") + "\n\n" + generateTSFunctions(cfg)
	}

	if err := os.WriteFile(filepath.Join(output, "client.ts"), []byte(client), 0o644); err != nil {
		return err
	}

	// index.ts
	index := "export * from './types';\nexport { UltrabaseClient } from './client';\n"
	if err := os.WriteFile(filepath.Join(output, "index.ts"), []byte(index), 0o644); err != nil {
		return err
	}

	fmt.Printf("  \u2713 Generated TypeScript SDK (%d types, %d functions)\n", len(cfg.Tables), len(cfg.Functions))
	fmt.Printf("  \u2713 Written to %s/\n", output)
	return nil
}

func generatePythonSDK(cfg *domain.Config, output string) error {
	if err := os.MkdirAll(output, 0o755); err != nil {
		return err
	}

	// Generate types (dataclasses)
	var types string
	types += "from __future__ import annotations\n"
	types += "from dataclasses import dataclass\n"
	types += "from typing import Any, Optional\n\n\n"

	for name, table := range cfg.Tables {
		types += fmt.Sprintf("@dataclass\nclass %s:\n", capitalize(name))
		for fname, field := range table.Fields {
			pyType := pgTypeToPython(field.Type)
			if !field.Required && !field.PrimaryKey {
				pyType = fmt.Sprintf("Optional[%s]", pyType)
			}
			types += fmt.Sprintf("    %s: %s\n", fname, pyType)
		}
		types += "\n\n"
	}

	if err := os.WriteFile(filepath.Join(output, "types.py"), []byte(types), 0o644); err != nil {
		return err
	}

	// Generate client
	client := `"""Ultrabase Python client — auto-generated."""
from __future__ import annotations

import json
from typing import Any, Optional
from urllib.request import Request, urlopen
from urllib.parse import urlencode


class UltrabaseClient:
    def __init__(self, base_url: str) -> None:
        self.base_url = base_url.rstrip("/")
        self.token: str | None = None
        self.auth = AuthClient(self)

    def from_(self, table: str) -> QueryBuilder:
        return QueryBuilder(self.base_url, table, self.token)

    def _headers(self) -> dict[str, str]:
        h: dict[str, str] = {}
        if self.token:
            h["Authorization"] = f"Bearer {self.token}"
        return h

    def _get(self, path: str) -> Any:
        req = Request(f"{self.base_url}{path}", headers=self._headers())
        with urlopen(req) as resp:
            return json.loads(resp.read())

    def _post(self, path: str, body: Any) -> Any:
        data = json.dumps(body).encode()
        headers = {**self._headers(), "Content-Type": "application/json"}
        req = Request(f"{self.base_url}{path}", data=data, headers=headers, method="POST")
        with urlopen(req) as resp:
            return json.loads(resp.read())


class AuthClient:
    def __init__(self, client: UltrabaseClient) -> None:
        self._client = client

    def sign_up(self, email: str, password: str, extra: dict[str, Any] | None = None) -> Any:
        return self._client._post("/api/auth/signup", {"email": email, "password": password, "extra": extra})

    def sign_in(self, email: str, password: str) -> Any:
        result = self._client._post("/api/auth/login", {"email": email, "password": password})
        if "access_token" in result:
            self._client.token = result["access_token"]
        return result

    def sign_out(self) -> None:
        self._client._post("/api/auth/logout", {})
        self._client.token = None

    def user(self) -> Any:
        return self._client._get("/api/auth/me")


class QueryBuilder:
    def __init__(self, base_url: str, table: str, token: str | None) -> None:
        self._url = f"{base_url}/api/{table}"
        self._token = token
        self._params: dict[str, str] = {}
        self._prefer: list[str] = []

    def select(self, fields: str) -> QueryBuilder:
        self._params["select"] = fields
        return self

    def eq(self, col: str, val: str) -> QueryBuilder:
        self._params[col] = f"eq.{val}"
        return self

    def neq(self, col: str, val: str) -> QueryBuilder:
        self._params[col] = f"neq.{val}"
        return self

    def gt(self, col: str, val: str) -> QueryBuilder:
        self._params[col] = f"gt.{val}"
        return self

    def gte(self, col: str, val: str) -> QueryBuilder:
        self._params[col] = f"gte.{val}"
        return self

    def lt(self, col: str, val: str) -> QueryBuilder:
        self._params[col] = f"lt.{val}"
        return self

    def lte(self, col: str, val: str) -> QueryBuilder:
        self._params[col] = f"lte.{val}"
        return self

    def order(self, col: str, ascending: bool = True) -> QueryBuilder:
        direction = ".asc" if ascending else ".desc"
        self._params["order"] = f"{col}{direction}"
        return self

    def limit(self, n: int) -> QueryBuilder:
        self._params["limit"] = str(n)
        return self

    def offset(self, n: int) -> QueryBuilder:
        self._params["offset"] = str(n)
        return self

    def execute(self) -> dict[str, Any]:
        url = f"{self._url}?{urlencode(self._params)}" if self._params else self._url
        headers: dict[str, str] = {}
        if self._token:
            headers["Authorization"] = f"Bearer {self._token}"
        if self._prefer:
            headers["Prefer"] = ", ".join(self._prefer)
        req = Request(url, headers=headers)
        with urlopen(req) as resp:
            data = json.loads(resp.read())
            content_range = resp.headers.get("Content-Range")
            count = _parse_content_range(content_range)
            return {"data": data, "count": count}


def _parse_content_range(header: str | None) -> int | None:
    if not header:
        return None
    parts = header.split("/")
    if len(parts) == 2 and parts[1].isdigit():
        return int(parts[1])
    return None
`

	// Append function methods
	if len(cfg.Functions) > 0 {
		client = strings.TrimRight(client, "\n") + "\n\n" + generatePythonFunctions(cfg)
	}

	if err := os.WriteFile(filepath.Join(output, "client.py"), []byte(client), 0o644); err != nil {
		return err
	}

	// __init__.py
	init := "from .types import *  # noqa: F401,F403\nfrom .client import UltrabaseClient  # noqa: F401\n"
	if err := os.WriteFile(filepath.Join(output, "__init__.py"), []byte(init), 0o644); err != nil {
		return err
	}

	fmt.Printf("  ✓ Generated Python SDK (%d types, %d functions)\n", len(cfg.Tables), len(cfg.Functions))
	fmt.Printf("  ✓ Written to %s/\n", output)
	return nil
}

func generateGoSDK(cfg *domain.Config, output string) error {
	if err := os.MkdirAll(output, 0o755); err != nil {
		return err
	}

	// Generate types
	var types string
	types += "// Code generated by ultrabase. DO NOT EDIT.\n"
	types += "package ultrabase\n\n"
	types += "import \"time\"\n\n"

	for name, table := range cfg.Tables {
		types += fmt.Sprintf("type %s struct {\n", capitalize(name))
		for fname, field := range table.Fields {
			goType := pgTypeToGo(field.Type)
			jsonTag := fname
			if !field.Required && !field.PrimaryKey {
				goType = "*" + goType
				jsonTag += ",omitempty"
			}
			types += fmt.Sprintf("\t%s %s `json:\"%s\"`\n", goFieldName(fname), goType, jsonTag)
		}
		types += "}\n\n"
	}

	if err := os.WriteFile(filepath.Join(output, "types.go"), []byte(types), 0o644); err != nil {
		return err
	}

	// Generate client
	client := `// Code generated by ultrabase. DO NOT EDIT.
package ultrabase

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTPClient: http.DefaultClient,
	}
}

func (c *Client) From(table string) *QueryBuilder {
	return &QueryBuilder{
		client: c,
		table:  table,
		params: url.Values{},
	}
}

type AuthClient struct {
	client *Client
}

func (c *Client) Auth() *AuthClient {
	return &AuthClient{client: c}
}

func (a *AuthClient) SignUp(email, password string, extra map[string]any) (map[string]any, error) {
	return a.client.post("/api/auth/signup", map[string]any{
		"email": email, "password": password, "extra": extra,
	})
}

func (a *AuthClient) SignIn(email, password string) (map[string]any, error) {
	result, err := a.client.post("/api/auth/login", map[string]any{
		"email": email, "password": password,
	})
	if err != nil {
		return nil, err
	}
	if token, ok := result["access_token"].(string); ok {
		a.client.Token = token
	}
	return result, nil
}

func (a *AuthClient) SignOut() error {
	_, err := a.client.post("/api/auth/logout", map[string]any{})
	a.client.Token = ""
	return err
}

func (a *AuthClient) User() (map[string]any, error) {
	return a.client.get("/api/auth/me")
}

type QueryBuilder struct {
	client *Client
	table  string
	params url.Values
	prefer []string
}

func (q *QueryBuilder) Select(fields string) *QueryBuilder {
	q.params.Set("select", fields)
	return q
}

func (q *QueryBuilder) Eq(col, val string) *QueryBuilder {
	q.params.Set(col, "eq."+val)
	return q
}

func (q *QueryBuilder) Neq(col, val string) *QueryBuilder {
	q.params.Set(col, "neq."+val)
	return q
}

func (q *QueryBuilder) Gt(col, val string) *QueryBuilder {
	q.params.Set(col, "gt."+val)
	return q
}

func (q *QueryBuilder) Gte(col, val string) *QueryBuilder {
	q.params.Set(col, "gte."+val)
	return q
}

func (q *QueryBuilder) Lt(col, val string) *QueryBuilder {
	q.params.Set(col, "lt."+val)
	return q
}

func (q *QueryBuilder) Lte(col, val string) *QueryBuilder {
	q.params.Set(col, "lte."+val)
	return q
}

func (q *QueryBuilder) Order(col string, ascending bool) *QueryBuilder {
	dir := ".asc"
	if !ascending {
		dir = ".desc"
	}
	q.params.Set("order", col+dir)
	return q
}

func (q *QueryBuilder) Limit(n int) *QueryBuilder {
	q.params.Set("limit", strconv.Itoa(n))
	return q
}

func (q *QueryBuilder) Offset(n int) *QueryBuilder {
	q.params.Set("offset", strconv.Itoa(n))
	return q
}

type QueryResult struct {
	Data  []map[string]any
	Count *int
}

func (q *QueryBuilder) Execute() (*QueryResult, error) {
	u := fmt.Sprintf("%s/api/%s", q.client.BaseURL, q.table)
	if len(q.params) > 0 {
		u += "?" + q.params.Encode()
	}

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	if q.client.Token != "" {
		req.Header.Set("Authorization", "Bearer "+q.client.Token)
	}
	if len(q.prefer) > 0 {
		req.Header.Set("Prefer", strings.Join(q.prefer, ", "))
	}

	resp, err := q.client.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var data []map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	result := &QueryResult{Data: data}
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		if idx := strings.LastIndex(cr, "/"); idx != -1 {
			if n, err := strconv.Atoi(cr[idx+1:]); err == nil {
				result.Count = &n
			}
		}
	}
	return result, nil
}

func (c *Client) get(path string) (map[string]any, error) {
	req, err := http.NewRequest("GET", c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	return result, json.Unmarshal(body, &result)
}

func (c *Client) post(path string, payload map[string]any) (map[string]any, error) {
	data, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", c.BaseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result map[string]any
	return result, json.Unmarshal(body, &result)
}
`

	// Append function methods
	if len(cfg.Functions) > 0 {
		client = strings.TrimRight(client, "\n") + "\n\n" + generateGoFunctions(cfg)
	}

	if err := os.WriteFile(filepath.Join(output, "client.go"), []byte(client), 0o644); err != nil {
		return err
	}

	fmt.Printf("  ✓ Generated Go SDK (%d types, %d functions)\n", len(cfg.Tables), len(cfg.Functions))
	fmt.Printf("  ✓ Written to %s/\n", output)
	return nil
}

func pgTypeToPython(pgType string) string {
	switch {
	case pgType == "bigserial" || pgType == "serial" || pgType == "integer" || pgType == "bigint" || pgType == "smallint":
		return "int"
	case pgType == "boolean" || pgType == "bool":
		return "bool"
	case pgType == "real" || pgType == "double precision" || strings.HasPrefix(pgType, "numeric"):
		return "float"
	case pgType == "jsonb" || pgType == "json":
		return "dict[str, Any]"
	case pgType == "text[]" || pgType == "varchar[]":
		return "list[str]"
	case pgType == "integer[]":
		return "list[int]"
	default:
		return "str"
	}
}

func pgTypeToGo(pgType string) string {
	switch {
	case pgType == "bigserial" || pgType == "bigint":
		return "int64"
	case pgType == "serial" || pgType == "integer":
		return "int32"
	case pgType == "smallint":
		return "int16"
	case pgType == "boolean" || pgType == "bool":
		return "bool"
	case pgType == "real":
		return "float32"
	case pgType == "double precision" || strings.HasPrefix(pgType, "numeric"):
		return "float64"
	case pgType == "jsonb" || pgType == "json":
		return "json.RawMessage"
	case pgType == "timestamptz" || pgType == "timestamp":
		return "time.Time"
	case pgType == "date":
		return "time.Time"
	case pgType == "text[]" || pgType == "varchar[]":
		return "[]string"
	case pgType == "integer[]":
		return "[]int32"
	default:
		return "string"
	}
}

func goFieldName(s string) string {
	// Convert snake_case to PascalCase
	parts := strings.Split(s, "_")
	var result string
	for _, p := range parts {
		if len(p) > 0 {
			result += strings.ToUpper(p[:1]) + p[1:]
		}
	}
	// Handle common abbreviations
	result = strings.ReplaceAll(result, "Id", "ID")
	result = strings.ReplaceAll(result, "Url", "URL")
	result = strings.ReplaceAll(result, "Api", "API")
	return result
}

func pgTypeToTS(pgType string) string {
	switch {
	case pgType == "bigserial" || pgType == "serial" || pgType == "integer" || pgType == "bigint" || pgType == "smallint":
		return "number"
	case pgType == "boolean" || pgType == "bool":
		return "boolean"
	case pgType == "jsonb" || pgType == "json":
		return "Record<string, any>"
	case pgType == "text[]" || pgType == "varchar[]":
		return "string[]"
	case pgType == "integer[]":
		return "number[]"
	default:
		return "string"
	}
}

func capitalize(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func importList(cfg *domain.Config) string {
	names := make([]string, 0, len(cfg.Tables))
	for name := range cfg.Tables {
		names = append(names, capitalize(name))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// generateTSFunctions generates TypeScript function client methods.
func generateTSFunctions(cfg *domain.Config) string {
	var b strings.Builder
	b.WriteString("export class FunctionsClient {\n")
	b.WriteString("  private baseUrl: string;\n")
	b.WriteString("  private token: string | null;\n\n")
	b.WriteString("  constructor(baseUrl: string, token: string | null) {\n")
	b.WriteString("    this.baseUrl = baseUrl;\n")
	b.WriteString("    this.token = token;\n")
	b.WriteString("  }\n\n")

	fnNames := sortedMapKeys(cfg.Functions)
	for _, name := range fnNames {
		fn := cfg.Functions[name]
		method := strings.ToUpper(fn.Method)
		if method == "" {
			method = "POST"
		}

		// Build param signature
		var params []string
		paramNames := sortedMapKeys(fn.Params)
		for _, pname := range paramNames {
			p := fn.Params[pname]
			tsType := pgTypeToTS(p.Type)
			opt := ""
			if !p.Required {
				opt = "?"
			}
			params = append(params, fmt.Sprintf("%s%s: %s", pname, opt, tsType))
		}

		// Return type
		returnType := "any"
		switch fn.Returns.Type {
		case "void":
			returnType = "{ affected_rows: number }"
		case "scalar":
			returnType = "Record<string, any>"
		case "row":
			returnType = "Record<string, any>"
		case "rows":
			returnType = "Record<string, any>[]"
		}

		b.WriteString(fmt.Sprintf("  async %s(%s): Promise<%s> {\n", name, strings.Join(params, ", "), returnType))

		if method == "GET" {
			b.WriteString("    const params = new URLSearchParams();\n")
			for _, pname := range paramNames {
				b.WriteString(fmt.Sprintf("    if (%s !== undefined) params.set('%s', String(%s));\n", pname, pname, pname))
			}
			b.WriteString(fmt.Sprintf("    const res = await fetch(this.baseUrl + '/api/fn/%s?' + params.toString(), {\n", name))
			b.WriteString("      headers: this.headers(),\n")
			b.WriteString("    });\n")
		} else {
			b.WriteString(fmt.Sprintf("    const res = await fetch(this.baseUrl + '/api/fn/%s', {\n", name))
			b.WriteString(fmt.Sprintf("      method: '%s',\n", method))
			b.WriteString("      headers: { ...this.headers(), 'Content-Type': 'application/json' },\n")
			bodyObj := "{"
			for i, pname := range paramNames {
				if i > 0 {
					bodyObj += ", "
				}
				bodyObj += pname
			}
			bodyObj += "}"
			b.WriteString(fmt.Sprintf("      body: JSON.stringify(%s),\n", bodyObj))
			b.WriteString("    });\n")
		}
		b.WriteString("    return res.json();\n")
		b.WriteString("  }\n\n")
	}

	b.WriteString("  private headers(): Record<string, string> {\n")
	b.WriteString("    const h: Record<string, string> = {};\n")
	b.WriteString("    if (this.token) h['Authorization'] = 'Bearer ' + this.token;\n")
	b.WriteString("    return h;\n")
	b.WriteString("  }\n")
	b.WriteString("}\n")

	return b.String()
}

// generatePythonFunctions generates Python function client methods.
func generatePythonFunctions(cfg *domain.Config) string {
	var b strings.Builder
	b.WriteString("class FunctionsClient:\n")
	b.WriteString("    def __init__(self, client: UltrabaseClient) -> None:\n")
	b.WriteString("        self._client = client\n\n")

	fnNames := sortedMapKeys(cfg.Functions)
	for _, name := range fnNames {
		fn := cfg.Functions[name]
		method := strings.ToUpper(fn.Method)
		if method == "" {
			method = "POST"
		}

		// Build param signature
		var params []string
		paramNames := sortedMapKeys(fn.Params)
		for _, pname := range paramNames {
			p := fn.Params[pname]
			pyType := pgTypeToPython(p.Type)
			if !p.Required {
				params = append(params, fmt.Sprintf("%s: Optional[%s] = None", pname, pyType))
			} else {
				params = append(params, fmt.Sprintf("%s: %s", pname, pyType))
			}
		}

		sig := "self"
		if len(params) > 0 {
			sig += ", " + strings.Join(params, ", ")
		}

		b.WriteString(fmt.Sprintf("    def %s(%s) -> Any:\n", name, sig))

		if method == "GET" {
			b.WriteString("        params = {}\n")
			for _, pname := range paramNames {
				b.WriteString(fmt.Sprintf("        if %s is not None:\n", pname))
				b.WriteString(fmt.Sprintf("            params['%s'] = str(%s)\n", pname, pname))
			}
			b.WriteString(fmt.Sprintf("        return self._client._get('/api/fn/%s?' + urlencode(params))\n\n", name))
		} else {
			bodyDict := "{"
			for i, pname := range paramNames {
				if i > 0 {
					bodyDict += ", "
				}
				bodyDict += fmt.Sprintf("'%s': %s", pname, pname)
			}
			bodyDict += "}"
			b.WriteString(fmt.Sprintf("        return self._client._post('/api/fn/%s', %s)\n\n", name, bodyDict))
		}
	}

	return b.String()
}

// generateGoFunctions generates Go function client methods.
func generateGoFunctions(cfg *domain.Config) string {
	var b strings.Builder
	b.WriteString("// FunctionsClient provides access to custom function endpoints.\n")
	b.WriteString("type FunctionsClient struct {\n")
	b.WriteString("\tclient *Client\n")
	b.WriteString("}\n\n")
	b.WriteString("func (c *Client) Functions() *FunctionsClient {\n")
	b.WriteString("\treturn &FunctionsClient{client: c}\n")
	b.WriteString("}\n\n")

	fnNames := sortedMapKeys(cfg.Functions)
	for _, name := range fnNames {
		fn := cfg.Functions[name]
		method := strings.ToUpper(fn.Method)
		if method == "" {
			method = "POST"
		}

		goName := goFieldName(name)
		paramNames := sortedMapKeys(fn.Params)

		// Build param list
		var params []string
		for _, pname := range paramNames {
			p := fn.Params[pname]
			goType := pgTypeToGo(p.Type)
			if !p.Required {
				goType = "*" + goType
			}
			params = append(params, fmt.Sprintf("%s %s", pname, goType))
		}

		b.WriteString(fmt.Sprintf("func (f *FunctionsClient) %s(%s) (map[string]any, error) {\n", goName, strings.Join(params, ", ")))

		if method == "GET" {
			b.WriteString("\tparams := url.Values{}\n")
			for _, pname := range paramNames {
				p := fn.Params[pname]
				if p.Required {
					b.WriteString(fmt.Sprintf("\tparams.Set(\"%s\", fmt.Sprint(%s))\n", pname, pname))
				} else {
					b.WriteString(fmt.Sprintf("\tif %s != nil { params.Set(\"%s\", fmt.Sprint(*%s)) }\n", pname, pname, pname))
				}
			}
			b.WriteString(fmt.Sprintf("\treturn f.client.get(\"/api/fn/%s?\" + params.Encode())\n", name))
		} else {
			b.WriteString("\tbody := map[string]any{\n")
			for _, pname := range paramNames {
				b.WriteString(fmt.Sprintf("\t\t\"%s\": %s,\n", pname, pname))
			}
			b.WriteString("\t}\n")
			b.WriteString(fmt.Sprintf("\treturn f.client.post(\"/api/fn/%s\", body)\n", name))
		}

		b.WriteString("}\n\n")
	}

	return b.String()
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

