# Error-envelope helpers and provider extensibility

Date: 2026-06-20
Status: approved, pre-implementation

## Why

We are getting the `instancez` backend ready to open source. Two findings from
the backend audit (`docs/backend-audit.md`) need fixing first, because both shape
how easy the code is to read and extend.

The first is duplication. The storage and admin HTTP handlers build their error
responses by hand, one `gin.H{...}` literal at a time: 54 copies in
`storage_v1_handler.go` and 18 in `admin_handler.go`. The storage shape is the
`@supabase/storage-js` client contract, so every hand-built copy is also a spot
where the contract can drift without anyone noticing. (The auth handler, which
the audit originally flagged, already routes through the shared `problemJSON`
helper, so it is left alone.)

The second is extensibility. The domain layer has clean port interfaces, but the
concrete providers behind them are picked by hardcoded `switch` statements.
Adding a storage or email backend means editing `cli/providers.go`; adding an
OAuth provider means touching seven `switch provider` sites across
`auth_handler.go` and `oauth.go`, including an authorize-URL block that is
copy-pasted between two handlers. For an outside contributor, "add a provider"
should be a new file, not a hunt.

The outcome: three error shapes each get one constructor, and new providers
register themselves. No wire formats change, so the supabase-js compatibility
contract holds throughout.

## Scope

In scope: the storage and admin error helpers, the OAuth provider interface with
map-based config, and the storage/email provider registry. Out of scope (audit
items not requested now): splitting oversized handler files, deduping test
fixtures, and the pre-publish secret-history scan.

## Part 1: error helpers

There are three distinct error wire-shapes, and they stay distinct because
supabase-js parses each one differently:

| Shape | Keys | Emitted by | Today |
| --- | --- | --- | --- |
| PostgREST | `code, message, details, hint` | REST, RPC, auth | centralized in `problemJSON` / `pgJSON` (no change) |
| Storage | `statusCode, error, message` | `/storage/v1` | 54 inline literals |
| Admin | `error, message` | `/_admin` dashboard API | 18 inline literals |

Two constructors:

```go
// storage_v1_handler.go, next to uploadWriteError
func storageErr(c *gin.Context, status int, errSlug, message string) {
    c.JSON(status, gin.H{
        "statusCode": strconv.Itoa(status),
        "error":      errSlug,
        "message":    message,
    })
}

// admin_handler.go, package level like problemJSON
func adminErr(c *gin.Context, status int, errSlug, message string) {
    c.JSON(status, gin.H{"error": errSlug, "message": message})
}
```

`statusCode` stays a string, which is what storage-js expects and the main thing
a test should pin. Every storage literal and the three branches of
`uploadWriteError` route through `storageErr` with their existing
status/slug/message unchanged. The admin literals that are exactly
`{error, message}` route through `adminErr`. The structural variants in admin
stay inline because they are a different shape: the `{errors: [...]}` validation
responses and the `{..., detail: ...}` npm-output errors. Each site is checked
during the edit rather than assumed.

This is a mechanical extraction. No status code, slug, or message text changes.

## Part 2: OAuth provider interface (clean break)

### Config becomes a map

`domain.AuthConfig` currently has concrete `Google` and `GitHub` fields. They are
replaced with:

```go
OAuth map[string]*OAuthProvider `yaml:"oauth" json:"oauth"`
```

In YAML this moves from `auth.google` / `auth.github` to `auth.oauth.<name>`.
Validation (`config/validate.go`) loops the map instead of checking two named
fields. Secrets keep flowing through the existing `${VAR}` interpolation in the
loader, so no credential handling changes. This is a clean break: the old keys
are removed, not shimmed.

### Provider interface and registry

A new file `internal/adapter/auth/oauthprovider.go` defines:

```go
type OAuthProvider interface {
    Name() string
    AuthorizeURL(cfg *domain.OAuthProvider, state string) string
    ExchangeCode(cfg *domain.OAuthProvider, code string) (accessToken string, err error)
    FetchUser(accessToken string) (*OAuthUserInfo, error)
}
```

plus a small registry: `Register(p OAuthProvider)` and
`Provider(name string) (OAuthProvider, bool)`. The `google` and `github`
implementations move into self-contained types that own their authorize-URL
template, token exchange, and user fetch. The existing `ExchangeCode`,
`FetchGoogleUser`, `FetchGitHubUser`, and `FetchGitHubPrimaryEmail` bodies
relocate into them; `OAuthUserInfo` stays exported.

### Handler cleanup

The seven `switch provider` sites in `auth_handler.go` and the switch in
`oauth.go` collapse to: resolve the config with `cfg.Auth.OAuth[name]`, look up
the provider with `Provider(name)`, and call the interface. The authorize-URL
block that is duplicated between `handleAuthorize` and `handleLinkIdentity`
becomes a single `provider.AuthorizeURL(cfg, state)` call. The id-token path stays
google-specific but reads its client ID from the map.

A new OAuth provider is then one new file plus one config block, with no edits to
the handler or the config struct.

## Part 3: storage and email registry

`cli/providers.go` keeps the same behavior but swaps the switches for factory
registries behind `domain.ObjectStore` and `domain.EmailSender`:

```go
type storageFactory func(ctx context.Context, p *domain.StorageProvider) (domain.ObjectStore, error)
type emailFactory   func(p *domain.EmailProvider) (domain.EmailSender, error)
```

The built-ins `s3`, `local`, and `resend` register through an explicit
`registerBuiltins()` rather than `init()`, so registration order is deterministic
and easy to test. `initStorageProvider` and `initEmailProvider` look up the
factory by `cfg.Providers.*.Type` and call it; the "unsupported provider
(supported: ...)" message is built from the registry's keys. The `newS3Store`,
`NewLocalStore`, and `resend.New` bodies become the factory bodies unchanged, and
the `nil` / empty-type early returns stay.

## Cross-repo changes

The clean break to the OAuth config touches both repos.

instancez:
- `internal/domain/schema.go`: the struct field
- `internal/config/validate.go`: map-loop validation
- `instancez.yaml`: example config
- `internal/cli/init.go`: scaffolding output
- `docs/site/src/content/docs/build/auth.md`: documented shape

instancez-platform (`../instancez-platform/main`):
- `ai/prompts/tools/instancez-yaml.md`: LLM tool description example
- `data/pkg/server/mcp_handler.go`: tool description text and YAML example
- `web/src/components/blocks/ai/ai-code-and-preview/config-tab.tsx`: reads
  `auth.google` / `auth.github` to list providers
- sweep `ai/prompts/generate-yaml.txt` and `data/pkg/server/configdiff_test.go`
  for the old keys

## Testing

Everything is test-driven. Each step writes or extends a failing test first.

- Storage helper: a handler test asserts `storageErr` produces
  `{"statusCode":"404","error":"not_found","message":...}` with the string
  status code. Existing storage handler tests stay green.
- Admin helper: existing admin handler tests cover the converted sites.
- OAuth: a new `oauthprovider_test.go` covers registry register/lookup, each
  provider's `AuthorizeURL` output against the current templates, and
  `ExchangeCode` / `FetchUser` against an `httptest.Server`. Existing OAuth
  handler tests stay green.
- Provider registry: `cli/providers_test.go` gains registry lookup and an
  unknown-provider error assertion; s3, local, and resend still resolve.
- Contract gate: `TestSupabaseJSCompat` (integration, Docker plus node) covers
  storage error responses and the OAuth flow end to end.
- Platform: its own build and tests, plus a visual check of the config tab.

## Sequencing

1. `storageErr` (isolated, lowest risk)
2. `adminErr`
3. storage/email registry (isolated to `cli`)
4. OAuth interface and config break (contract-critical, last)

Each step is independently green-able. Run the full feedback loop between steps:
`go build ./...`, `go test -race` for touched packages, and the integration suite
for the HTTP and OAuth changes.
