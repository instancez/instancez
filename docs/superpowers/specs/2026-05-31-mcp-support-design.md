# MCP Support Design

**Date:** 2026-05-31
**Status:** Approved

## Overview

Expose Ultrabase Cloud project management and data access as an MCP (Model Context Protocol) server so LLMs can create, deploy, and query ultrabase projects without human CLI intervention.

## Scope

- Remote MCP server hosted in the existing cloud backend (`instancez-coder/v2`)
- OAuth 2.0 authorization code + PKCE flow for MCP client authentication
- 9 project management tools + 1 raw SQL data tool
- No new deployment — changes land in `auth` and `data` services only

Out of scope: local `ultra dev` interaction, schema migrations via MCP, frontend/page management.

## Architecture

Two services change; no new service or deployment.

### `auth` service — new OAuth 2.0 endpoints

| Endpoint | Purpose |
|----------|---------|
| `GET /.well-known/oauth-authorization-server` | Discovery metadata required by MCP spec |
| `POST /oauth/register` | Dynamic client registration (MCP spec requirement) |
| `GET /oauth/authorize` | Redirects unauthenticated users to existing login page; issues auth code on valid session cookie |
| `POST /oauth/token` | Exchanges auth code + PKCE verifier for a PAT |

The existing session/cookie login infrastructure is reused unchanged for the `/oauth/authorize` user-facing step. Authorization codes are short-lived (5 min, single-use), stored in a new `oauth_codes` MongoDB collection (TTL-indexed on `expires_at`, unique index on `code`). The issued `access_token` is a standard `PersonalAccessToken` — no new token type.

### `data` service — ProjectService extraction + MCP server

**ProjectService** — a new struct that holds all business logic currently embedded in gin handlers. Existing handlers become thin wrappers:

```
Before: gin handler → MongoDB directly
After:  gin handler → ProjectService → MongoDB
        MCP tool handler → ProjectService → MongoDB
```

Methods:
- `Whoami(ctx, userID) (*WhoamiResult, error)`
- `ListProjects(ctx, userID) ([]*ProjectSummary, error)`
- `CreateProject(ctx, userID, name string) (*Project, error)`
- `GetProject(ctx, userID, projectID string) (*Project, error)`
- `DeleteProject(ctx, userID, projectID string) error`
- `GetDeployedYAML(ctx, userID, projectID string) (string, error)`
- `UploadYAML(ctx, userID, projectID, yaml string) error`
- `MigrationPreview(ctx, userID, projectID string) (string, error)`
- `DeployProject(ctx, userID, projectID string) (*DeployResult, error)`
- `ExecuteSQL(ctx, userID, projectID, sql string) (*SQLResult, error)`

Three new REST endpoints added alongside the refactor (needed by both MCP and future CLI):
- `GET /ultrabase/projects` — list all projects for authenticated user
- `GET /ultrabase/projects/:id` — get single project
- `DELETE /ultrabase/projects/:id` — delete project

**MCP server** — two endpoints added to the `data` service using the MCP 2025 Streamable HTTP transport:
- `POST /mcp` — receives client JSON-RPC messages (tool calls, pings)
- `GET /mcp` — opens the SSE stream for server→client responses

Both validate the bearer PAT using the existing `BearerAuthMiddleware`. Tool call dispatch goes to `ProjectService`.

## OAuth 2.0 Flow

```
MCP client                    auth service                  browser/user
    │                               │                              │
    │  GET /.well-known/...         │                              │
    │──────────────────────────────▶│                              │
    │  ← discovery metadata         │                              │
    │                               │                              │
    │  POST /oauth/register         │                              │
    │──────────────────────────────▶│                              │
    │  ← {client_id}                │                              │
    │                               │                              │
    │  GET /oauth/authorize         │                              │
    │  ?client_id=...               │                              │
    │  &code_challenge=...          │                              │
    │  &redirect_uri=localhost:...  │                              │
    │──────────────────────────────▶│                              │
    │                               │  redirect to login?next=...  │
    │                               │─────────────────────────────▶│
    │                               │                     user logs in
    │                               │◀─────────────────────────────│
    │                               │  (session cookie present)    │
    │                               │  redirect to redirect_uri    │
    │                               │  ?code=one_time_code         │
    │◀──────────────────────────────│                              │
    │                               │                              │
    │  POST /oauth/token            │                              │
    │  {code, code_verifier}        │                              │
    │──────────────────────────────▶│                              │
    │  ← {access_token: "pat_..."}  │                              │
    │                               │                              │
    │  POST /mcp                    data service                   │
    │  Authorization: Bearer pat_...│──────────────────────────────▶
```

PKCE prevents code interception. No client secret is required or stored.

## MCP Tools

### Project management tools

| Tool | Description | Key inputs |
|------|-------------|------------|
| `whoami` | Returns authenticated user identity | — |
| `list_projects` | Lists all projects owned by the user | — |
| `create_project` | Creates a new backend project | `name` |
| `get_project` | Gets project details and status | `project_id` |
| `delete_project` | Deletes a project | `project_id` |
| `get_yaml` | Returns the production `ultrabase.yaml` | `project_id` |
| `upload_yaml` | Uploads a new `ultrabase.yaml` draft | `project_id`, `yaml` |
| `migration_preview` | Returns DDL diff between draft and production | `project_id` |
| `deploy_project` | Triggers a production deploy | `project_id` |

### Data tool

| Tool | Description | Key inputs |
|------|-------------|------------|
| `execute_sql` | Executes a raw SQL query against the project's production database | `project_id`, `sql` |

**`execute_sql` safety constraints:**
- Runs under the `service_role` Postgres role (BYPASSRLS, DML grants only — no object ownership, no `CREATE`/`ALTER`/`DROP` possible)
- `SET statement_timeout = '10s'` applied per query
- Results row-capped at 1000 rows to prevent oversized responses
- `TRUNCATE` not granted to `service_role`, so it fails at the Postgres level

The LLM receives Postgres error messages verbatim so it can self-correct (wrong column name, syntax error, etc.).

## Error Handling

`ProjectService` methods return typed Go errors. The two transport layers translate independently:

- **REST handlers:** map to HTTP 4xx/5xx + `{"error": "..."}` JSON envelope (unchanged from current behavior)
- **MCP tool handlers:** map to JSON-RPC error responses with a human-readable `message` field

Internal details (MongoDB errors, stack traces) are never exposed in either path. Postgres errors from `execute_sql` are the exception — they are passed through as-is since they are useful for LLM self-correction.

## Testing

- `ProjectService` unit-tested directly (no gin, no HTTP) — same testcontainers Postgres setup used by existing integration tests
- OAuth endpoints tested with `httptest` — same pattern as existing auth service tests
- MCP endpoint tested with a plain HTTP client sending JSON-RPC payloads — no MCP SDK required in tests
- `execute_sql` integration-tested against testcontainers Postgres: verify DDL is rejected, statement timeout fires, row cap is enforced
