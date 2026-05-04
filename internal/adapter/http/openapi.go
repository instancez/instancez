package http

import (
	"fmt"
	"strings"

	"github.com/saedx1/ultrabase/internal/domain"
)

// GenerateOpenAPI builds an OpenAPI 3.0 spec from the config.
func GenerateOpenAPI(cfg *domain.Config) map[string]any {
	paths := map[string]any{}
	schemas := map[string]any{}

	// Table CRUD endpoints (PostgREST-compatible)
	for tableName, table := range cfg.Tables {
		basePath := "/rest/v1/" + tableName
		tableSchema := generateTableSchema(tableName, table)
		schemas[tableName] = tableSchema
		schemas[tableName+"_input"] = generateTableInputSchema(tableName, table)

		paths[basePath] = map[string]any{
			"get":    generateListOp(tableName, table),
			"post":   generateCreateOp(tableName),
			"patch":  generateUpdateOp(tableName),
			"delete": generateDeleteOp(tableName),
		}
	}

	// Auth endpoints (GoTrue-compatible)
	if cfg.Auth != nil {
		paths["/auth/v1/signup"] = map[string]any{
			"post": opSummary("Sign up", "Create a new user account", []string{"auth"}, 200),
		}
		paths["/auth/v1/token"] = map[string]any{
			"post": opSummary("Token", "Exchange credentials or refresh token for an access token (grant_type=password|refresh_token)", []string{"auth"}, 200),
		}
		paths["/auth/v1/logout"] = map[string]any{
			"post": opSummary("Log out", "Revoke refresh tokens (scope=global|local|others)", []string{"auth"}, 204),
		}
		paths["/auth/v1/user"] = map[string]any{
			"get": opSummary("Get current user", "Returns the authenticated user", []string{"auth"}, 200),
			"put": opSummary("Update current user", "Update email, password, or user_metadata", []string{"auth"}, 200),
		}
		paths["/auth/v1/settings"] = map[string]any{
			"get": opSummary("Auth settings", "Public auth configuration (enabled providers etc.)", []string{"auth"}, 200),
		}
		if cfg.Auth.Email != nil {
			paths["/auth/v1/recover"] = map[string]any{
				"post": opSummary("Request password reset", "Send password reset email", []string{"auth"}, 200),
			}
			paths["/auth/v1/verify"] = map[string]any{
				"post": opSummary("Verify OTP", "Consume a signup/recovery/magiclink token and return a session", []string{"auth"}, 200),
				"get":  opSummary("Verify email (browser)", "Email verification landing URL", []string{"auth"}, 200),
			}
		}
		paths["/auth/v1/authorize"] = map[string]any{
			"get": opSummary("OAuth redirect", "Begin an OAuth flow (provider=google|github, redirect_to=...)", []string{"auth"}, 307),
		}
	}

	// Storage endpoints
	for bucketName := range cfg.Storage {
		basePath := "/api/storage/" + bucketName
		paths[basePath+"/sign"] = map[string]any{
			"post": opSummary("Sign upload", "Get a presigned upload URL for "+bucketName, []string{"storage"}, 200),
		}
		paths[basePath+"/{id}"] = map[string]any{
			"get":    opSummary("Sign download", "Get a presigned download URL", []string{"storage"}, 200),
			"delete": opSummary("Delete object", "Delete a file from "+bucketName, []string{"storage"}, 204),
		}
	}

	for fnName, fn := range cfg.Functions {
		path := "/rest/v1/rpc/" + fnName
		ops := map[string]any{
			"post": generateRPCPostOp(fnName, fn),
		}
		if !strings.EqualFold(fn.Volatility, "volatile") {
			ops["get"] = generateRPCGetOp(fnName, fn)
		}
		paths[path] = ops
	}

	// Admin endpoints
	paths["/api/_admin/events"] = map[string]any{
		"get": opSummary("List events", "List event log", []string{"admin"}, 200),
	}
	paths["/api/_admin/events/dead"] = map[string]any{
		"get": opSummary("Dead letter queue", "List dead-lettered events", []string{"admin"}, 200),
	}
	paths["/api/_admin/status"] = map[string]any{
		"get": opSummary("Server status", "Health and runtime info", []string{"admin"}, 200),
	}
	paths["/api/_admin/migrations"] = map[string]any{
		"get": opSummary("List migrations", "Applied migration history", []string{"admin"}, 200),
	}
	paths["/api/_admin/users"] = map[string]any{
		"get": opSummary("List users", "List all users", []string{"admin"}, 200),
	}

	// Health endpoints
	paths["/live"] = map[string]any{
		"get": opSummary("Liveness", "Process alive check", []string{"health"}, 200),
	}
	paths["/health"] = map[string]any{
		"get": opSummary("Health", "App initialized check", []string{"health"}, 200),
	}
	paths["/ready"] = map[string]any{
		"get": opSummary("Readiness", "Deep dependency check", []string{"health"}, 200),
	}

	spec := map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       cfg.Project.Name,
			"description": cfg.Project.Description,
			"version":     "1.0.0",
		},
		"servers": []map[string]any{
			{"url": fmt.Sprintf("http://localhost:%d", cfg.Server.Port)},
		},
		"paths": paths,
		"components": map[string]any{
			"schemas": schemas,
			"securitySchemes": map[string]any{
				"bearerAuth": map[string]any{
					"type":         "http",
					"scheme":       "bearer",
					"bearerFormat": "JWT",
				},
				"adminKey": map[string]any{
					"type":        "http",
					"scheme":      "bearer",
					"description": "Admin API key (ULTRABASE_ADMIN_KEY)",
				},
			},
		},
	}

	return spec
}

func generateTableSchema(tableName string, table domain.Table) map[string]any {
	properties := map[string]any{}
	required := []string{}

	for _, field := range table.Fields {
		prop := postgresTypeToOpenAPI(field.Type)

		if field.ForeignKey != nil {
			prop = map[string]any{"type": "integer", "format": "int64"}
		}
		if len(field.Enum) > 0 {
			prop["enum"] = field.Enum
		}

		properties[field.Name] = prop

		if field.Required || field.PrimaryKey {
			required = append(required, field.Name)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func generateTableInputSchema(tableName string, table domain.Table) map[string]any {
	properties := map[string]any{}
	required := []string{}

	for _, field := range table.Fields {
		if field.PrimaryKey {
			continue // PK usually auto-generated
		}

		prop := postgresTypeToOpenAPI(field.Type)
		if field.ForeignKey != nil {
			prop = map[string]any{"type": "integer", "format": "int64"}
		}
		if len(field.Enum) > 0 {
			prop["enum"] = field.Enum
		}

		properties[field.Name] = prop

		if field.Required && field.Default == nil {
			required = append(required, field.Name)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func generateListOp(tableName string, table domain.Table) map[string]any {
	params := []map[string]any{
		{"name": "select", "in": "query", "schema": map[string]any{"type": "string"}, "description": "Fields to return (PostgREST select syntax)"},
		{"name": "order", "in": "query", "schema": map[string]any{"type": "string"}, "description": "Sort order (e.g. created_at.desc)"},
		{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer", "default": 20}},
		{"name": "offset", "in": "query", "schema": map[string]any{"type": "integer", "default": 0}},
	}

	// Add filter params for each column
	for _, field := range table.Fields {
		params = append(params, map[string]any{
			"name":        field.Name,
			"in":          "query",
			"schema":      map[string]any{"type": "string"},
			"description": fmt.Sprintf("Filter by %s (e.g. eq.value, gt.5)", field.Name),
		})
	}

	tags := []string{tableName}

	return map[string]any{
		"summary":    "List " + tableName,
		"tags":       tags,
		"parameters": params,
		"responses": map[string]any{
			"200": map[string]any{
				"description": "Array of " + tableName,
				"headers": map[string]any{
					"Content-Range": map[string]any{
						"schema":      map[string]any{"type": "string"},
						"description": "Pagination range (e.g. 0-19/42)",
					},
				},
				"content": map[string]any{
					"application/json": map[string]any{
						"schema": map[string]any{
							"type":  "array",
							"items": map[string]any{"$ref": "#/components/schemas/" + tableName},
						},
					},
				},
			},
		},
	}
}

func generateCreateOp(tableName string) map[string]any {
	return map[string]any{
		"summary": "Create " + tableName,
		"tags":    []string{tableName},
		"requestBody": map[string]any{
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{"$ref": "#/components/schemas/" + tableName + "_input"},
				},
			},
		},
		"responses": map[string]any{
			"201": map[string]any{"description": "Created"},
			"400": map[string]any{"description": "Bad request"},
			"409": map[string]any{"description": "Conflict (duplicate key)"},
			"422": map[string]any{"description": "Validation error"},
		},
	}
}

func generateUpdateOp(tableName string) map[string]any {
	return map[string]any{
		"summary":     "Update " + tableName,
		"description": "PATCH with filters updates matching rows",
		"tags":        []string{tableName},
		"requestBody": map[string]any{
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{"$ref": "#/components/schemas/" + tableName + "_input"},
				},
			},
		},
		"responses": map[string]any{
			"204": map[string]any{"description": "Updated"},
			"400": map[string]any{"description": "Bad request"},
			"422": map[string]any{"description": "Validation error"},
		},
	}
}

func generateDeleteOp(tableName string) map[string]any {
	return map[string]any{
		"summary":     "Delete " + tableName,
		"description": "DELETE with filters deletes matching rows",
		"tags":        []string{tableName},
		"responses": map[string]any{
			"204": map[string]any{"description": "Deleted"},
		},
	}
}

// rpcArgsSchema produces the request-body JSON schema for an RPC Function.
// Each declared arg becomes a property, required if the arg has no default.
// An empty function (no args) still returns a valid empty object schema so
// clients can send `{}`.
func rpcArgsSchema(fn domain.Function) map[string]any {
	properties := map[string]any{}
	var required []string
	for _, a := range fn.Args {
		properties[a.Name] = postgresTypeToOpenAPI(a.Type)
		if a.Required {
			required = append(required, a.Name)
		}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// rpcResponseSchema maps the function's ReturnCategory/Returns to an
// OpenAPI response schema. scalar → bare value, setof → array of objects,
// void → no content (204). For setof we leave the item type loose since
// resolving TABLE(...)/SETOF to actual columns lives outside the OpenAPI
// path for now.
func rpcResponseSchema(fn domain.Function) (int, map[string]any) {
	switch fn.ReturnCategory {
	case "void":
		return 204, nil
	case "setof":
		return 200, map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "object"},
		}
	default: // scalar
		return 200, postgresTypeToOpenAPI(fn.Returns.Type)
	}
}

func generateRPCPostOp(fnName string, fn domain.Function) map[string]any {
	status, schema := rpcResponseSchema(fn)
	op := map[string]any{
		"summary": "Call " + fnName,
		"tags":    []string{"rpc"},
		"requestBody": map[string]any{
			"required": true,
			"content": map[string]any{
				"application/json": map[string]any{"schema": rpcArgsSchema(fn)},
			},
		},
	}
	responses := map[string]any{}
	if schema == nil {
		responses[fmt.Sprintf("%d", status)] = map[string]any{"description": "No content"}
	} else {
		responses[fmt.Sprintf("%d", status)] = map[string]any{
			"description": "Success",
			"content": map[string]any{
				"application/json": map[string]any{"schema": schema},
			},
		}
	}
	op["responses"] = responses
	return op
}

func generateRPCGetOp(fnName string, fn domain.Function) map[string]any {
	var params []map[string]any
	for _, a := range fn.Args {
		params = append(params, map[string]any{
			"name":     a.Name,
			"in":       "query",
			"required": a.Required,
			"schema":   postgresTypeToOpenAPI(a.Type),
		})
	}
	status, schema := rpcResponseSchema(fn)
	op := map[string]any{
		"summary":    "Call " + fnName + " (non-volatile)",
		"tags":       []string{"rpc"},
		"parameters": params,
	}
	responses := map[string]any{}
	if schema == nil {
		responses[fmt.Sprintf("%d", status)] = map[string]any{"description": "No content"}
	} else {
		responses[fmt.Sprintf("%d", status)] = map[string]any{
			"description": "Success",
			"content": map[string]any{
				"application/json": map[string]any{"schema": schema},
			},
		}
	}
	op["responses"] = responses
	return op
}

func opSummary(summary, description string, tags []string, status int) map[string]any {
	return map[string]any{
		"summary":     summary,
		"description": description,
		"tags":        tags,
		"responses": map[string]any{
			fmt.Sprintf("%d", status): map[string]any{"description": "Success"},
		},
	}
}

func postgresTypeToOpenAPI(pgType string) map[string]any {
	lower := strings.ToLower(pgType)

	switch {
	case lower == "bigserial" || lower == "bigint" || lower == "int8":
		return map[string]any{"type": "integer", "format": "int64"}
	case lower == "serial" || lower == "integer" || lower == "int" || lower == "int4":
		return map[string]any{"type": "integer", "format": "int32"}
	case lower == "smallserial" || lower == "smallint":
		return map[string]any{"type": "integer", "format": "int16"}
	case lower == "boolean" || lower == "bool":
		return map[string]any{"type": "boolean"}
	case lower == "real" || lower == "float4":
		return map[string]any{"type": "number", "format": "float"}
	case lower == "double precision" || lower == "float8":
		return map[string]any{"type": "number", "format": "double"}
	case strings.HasPrefix(lower, "numeric") || strings.HasPrefix(lower, "decimal"):
		return map[string]any{"type": "number"}
	case lower == "uuid":
		return map[string]any{"type": "string", "format": "uuid"}
	case lower == "date":
		return map[string]any{"type": "string", "format": "date"}
	case lower == "timestamptz" || lower == "timestamp":
		return map[string]any{"type": "string", "format": "date-time"}
	case lower == "time" || lower == "timetz":
		return map[string]any{"type": "string", "format": "time"}
	case lower == "jsonb" || lower == "json":
		return map[string]any{"type": "object"}
	case strings.HasSuffix(lower, "[]"):
		return map[string]any{"type": "array", "items": postgresTypeToOpenAPI(strings.TrimSuffix(pgType, "[]"))}
	case lower == "bytea":
		return map[string]any{"type": "string", "format": "byte"}
	case lower == "inet" || lower == "cidr":
		return map[string]any{"type": "string", "format": "ipv4"}
	default:
		return map[string]any{"type": "string"}
	}
}
