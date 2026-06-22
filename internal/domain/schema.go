// Package domain contains pure types and interfaces (ports) for Instancez.
// This package has zero imports from adapter/, app/, or external packages.
package domain

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Config is the top-level Instancez configuration parsed from YAML.
type Config struct {
	Version    int                     `yaml:"version" json:"version"`
	Project    Project                 `yaml:"project" json:"project"`
	Extensions []string                `yaml:"extensions" json:"extensions"`
	Database   DatabaseConfig          `yaml:"database" json:"database"`
	Server     Server                  `yaml:"server" json:"server"`
	Providers  Providers               `yaml:"providers" json:"providers"`
	Auth       *Auth                   `yaml:"auth" json:"auth"`
	Tables     map[string]Table        `yaml:"tables" json:"tables"`
	Storage    map[string]Bucket       `yaml:"storage" json:"storage"`
	RPC        map[string]Function     `yaml:"rpc" json:"rpc"`
	Functions  map[string]CodeFunction `yaml:"functions" json:"functions"`
	// FunctionsBundle is a pointer to the pre-built functions bundle that
	// `serve` consumes at runtime (it never builds). For self-hosted deployments,
	// `inz bundle` builds the bundle (vendoring node_modules), uploads it, and
	// records the pointer here. For the managed cloud path, the cloud builds and
	// stamps the pointer server-side from uploaded sources; `inz cloud deploy`
	// does not write this field. The value is an object URI carrying a version
	// token, e.g. "s3://bucket/key#sha256". Empty when the project has no code
	// functions or no bundle destination was configured. Consumed by Task 12;
	// not read by the runtime yet.
	FunctionsBundle string `yaml:"functions_bundle" json:"functions_bundle"`

	// UnknownKeys carries config keys that did not map to any field, detected at
	// decode time and surfaced by Validate alongside structural errors (so a
	// caller sees unknown keys and other problems in one pass). It is never
	// serialized — the decoders populate it, Validate reads it.
	UnknownKeys ValidationErrors `yaml:"-" json:"-"`
}


// Project holds display-only metadata.
type Project struct {
	Name        string        `yaml:"name" json:"name"`
	Description string        `yaml:"description" json:"description"`
	Cloud       *ProjectCloud `yaml:"cloud" json:"cloud"`
}

// ProjectCloud links a project to instancez Cloud. The fields are read by the
// `cloud` package (deploy, status) via a standalone YAML walk; they are declared
// here so the strict config decoder recognizes the project.cloud block.
type ProjectCloud struct {
	ProjectID string `yaml:"project_id" json:"project_id"`
	APIURL    string `yaml:"api_url" json:"api_url"`
}

// Server groups runtime/HTTP concerns.
type Server struct {
	Port        int      `yaml:"port" json:"port"`
	CORS        CORS     `yaml:"cors" json:"cors"`
	Timeouts    Timeouts `yaml:"timeouts" json:"timeouts"`
	MaxBodySize string   `yaml:"max_body_size" json:"max_body_size"`
	DocsUI      *bool    `yaml:"docs_ui" json:"docs_ui"`
	MaxLimit    int      `yaml:"max_limit" json:"max_limit"`
}

type CORS struct {
	Origins     []string `yaml:"origins" json:"origins"`
	Methods     []string `yaml:"methods" json:"methods"`
	Headers     []string `yaml:"headers" json:"headers"`
	Credentials bool     `yaml:"credentials" json:"credentials"`
	MaxAge      int      `yaml:"max_age" json:"max_age"`
}

type Timeouts struct {
	Request  string `yaml:"request" json:"request"`
	DBQuery  string `yaml:"db_query" json:"db_query"`
	Upload   string `yaml:"upload" json:"upload"`
	Shutdown string `yaml:"shutdown" json:"shutdown"`
}

type DatabaseConfig struct {
	Pool PoolConfig `yaml:"pool" json:"pool"`
}

type PoolConfig struct {
	Max         int    `yaml:"max" json:"max"`
	Min         int    `yaml:"min" json:"min"`
	IdleTimeout string `yaml:"idle_timeout" json:"idle_timeout"`
}

// Providers connects to external services.
type Providers struct {
	Email   *EmailProvider   `yaml:"email" json:"email"`
	Storage *StorageProvider `yaml:"storage" json:"storage"`
}

type EmailProvider struct {
	Type             string `yaml:"type" json:"type"`
	APIKey           string `yaml:"api_key" json:"api_key"`
	DefaultFromEmail string `yaml:"default_from_email" json:"default_from_email"`
}

type StorageProvider struct {
	Type            string `yaml:"type" json:"type"`
	Bucket          string `yaml:"bucket" json:"bucket"`
	Region          string `yaml:"region" json:"region"`
	AccessKeyID     string `yaml:"access_key_id" json:"access_key_id"`
	SecretAccessKey string `yaml:"secret_access_key" json:"secret_access_key"`
	Endpoint        string `yaml:"endpoint" json:"endpoint"`
	Path            string `yaml:"path" json:"path"`
}

// Auth configures authentication. Custom user fields are defined in
// tables.users like any other table; core columns (id, email, password_hash,
// etc.) are auto-emitted by the migrator.
//
// AllowSignup / AllowAnonymous are pointers so we can distinguish "unset"
// (defaults to allowed, preserving backward compatibility) from "explicitly
// false" (public registration is disabled). Always read them through the
// SignupAllowed() / AnonymousAllowed() helpers.
type Auth struct {
	JWTExpiry          string         `yaml:"jwt_expiry" json:"jwt_expiry"`
	RefreshTokens      bool           `yaml:"refresh_tokens" json:"refresh_tokens"`
	RefreshTokenExpiry string         `yaml:"refresh_token_expiry" json:"refresh_token_expiry"`
	AllowSignup        *bool          `yaml:"allow_signup" json:"allow_signup"`
	AllowAnonymous     *bool          `yaml:"allow_anonymous" json:"allow_anonymous"`
	// RedirectURLs is the allowlist of external origins that post-auth flows
	// (password recovery, email verification, OAuth) may redirect to. The
	// server's own base URL is always allowed; relative same-origin paths are
	// always allowed. Anything else must match one of these origins, otherwise
	// the flow falls back to the base URL. This prevents an attacker-supplied
	// redirect_to from exfiltrating the session tokens placed in the redirect.
	RedirectURLs []string   `yaml:"redirect_urls" json:"redirect_urls"`
	Email        *AuthEmail `yaml:"email" json:"email"`
	// OAuth maps a provider name (e.g. "google", "github") to its credentials.
	// The name keys into the OAuth provider registry in internal/adapter/auth,
	// so adding a provider needs only a new registry entry plus a config block.
	OAuth map[string]*OAuthProvider `yaml:"oauth" json:"oauth"`
}

// IsRedirectAllowed reports whether target is a safe post-auth redirect
// destination. baseURL is the server's own external origin and is always
// allowed. An empty target is allowed (callers substitute the base URL).
// Relative paths ("/foo") are same-origin and allowed; protocol-relative
// ("//host"), non-http(s), and parser-differential (backslash/NUL) targets are
// rejected; absolute URLs must match the origin of baseURL or a configured
// allowlist entry.
func (a *Auth) IsRedirectAllowed(target, baseURL string) bool {
	if target == "" {
		return true
	}
	// Browsers treat backslashes as forward slashes; Go's url.Parse does not.
	// Reject them (and NULs) outright to avoid parser-differential bypasses.
	if strings.ContainsAny(target, "\\\x00") {
		return false
	}
	u, err := url.Parse(target)
	if err != nil {
		return false
	}
	// Relative same-origin path. A protocol-relative "//host" has Host set and
	// is therefore NOT treated as relative here.
	if u.Scheme == "" && u.Host == "" {
		return strings.HasPrefix(target, "/")
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	targetOrigin := strings.ToLower(u.Scheme + "://" + u.Host)
	for _, allowed := range a.allowedOrigins(baseURL) {
		if targetOrigin == allowed {
			return true
		}
	}
	return false
}

// allowedOrigins returns the lowercased scheme://host origins that redirects
// may target: the server base URL plus each configured allowlist entry. Safe
// to call on a nil receiver.
func (a *Auth) allowedOrigins(baseURL string) []string {
	out := make([]string, 0, 4)
	if o := redirectOrigin(baseURL); o != "" {
		out = append(out, o)
	}
	if a != nil {
		for _, entry := range a.RedirectURLs {
			if o := redirectOrigin(entry); o != "" {
				out = append(out, o)
			}
		}
	}
	return out
}

func redirectOrigin(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

// SignupAllowed reports whether public /auth/v1/signup with credentials is
// permitted. Defaults to true when AllowSignup is unset. The admin-keyed
// /auth/v1/admin/users and /auth/v1/invite endpoints ignore this flag.
func (a *Auth) SignupAllowed() bool {
	if a == nil || a.AllowSignup == nil {
		return true
	}
	return *a.AllowSignup
}

// AnonymousAllowed reports whether anonymous sign-in (POST /auth/v1/signup
// with an empty body) is permitted. Defaults to true when AllowAnonymous is
// unset.
func (a *Auth) AnonymousAllowed() bool {
	if a == nil || a.AllowAnonymous == nil {
		return true
	}
	return *a.AllowAnonymous
}

type AuthEmail struct {
	VerifyEmail bool                     `yaml:"verify_email" json:"verify_email"`
	Templates   map[string]EmailTemplate `yaml:"templates" json:"templates"`
}

type EmailTemplate struct {
	Subject  string `yaml:"subject" json:"subject"`
	Body     string `yaml:"body" json:"body"`
	BodyFile string `yaml:"body_file" json:"body_file"`
}

type OAuthProvider struct {
	ClientID     string `yaml:"client_id" json:"client_id"`
	ClientSecret string `yaml:"client_secret" json:"client_secret"`
	RedirectURL  string `yaml:"redirect_url" json:"redirect_url"`
}

// Table defines a database table.
type Table struct {
	Schema  string      `yaml:"schema" json:"schema"`
	Fields  []Field     `yaml:"fields" json:"fields"`
	Indexes []Index     `yaml:"indexes" json:"indexes"`
	RLS     []RLSPolicy `yaml:"rls" json:"rls"`
}

// EffectiveSchema returns the table's schema, defaulting to "public".
func (t Table) EffectiveSchema() string {
	if t.Schema == "" {
		return "public"
	}
	return t.Schema
}

// GetField returns the named field and true, or zero value and false.
func (t Table) GetField(name string) (Field, bool) {
	for _, f := range t.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// FieldMap returns a map view of the fields for code that needs key-based lookup.
func (t Table) FieldMap() map[string]Field {
	m := make(map[string]Field, len(t.Fields))
	for _, f := range t.Fields {
		m[f.Name] = f
	}
	return m
}

// Field defines a table column.
type Field struct {
	Name       string      `yaml:"name" json:"name"`
	Type       string      `yaml:"type" json:"type"`
	PrimaryKey bool        `yaml:"primary_key" json:"primary_key"`
	Required   bool        `yaml:"required" json:"required"`
	Unique     bool        `yaml:"unique" json:"unique"`
	Default    any         `yaml:"default" json:"default"`
	Enum       []string    `yaml:"enum" json:"enum"`
	Pattern    string      `yaml:"pattern" json:"pattern"`
	Min        *float64    `yaml:"min" json:"min"`
	Max        *float64    `yaml:"max" json:"max"`
	Check      string      `yaml:"check" json:"check"`
	ForeignKey *ForeignKey `yaml:"foreign_key" json:"foreign_key"`
	Ref        string      `yaml:"ref" json:"ref"`             // storage reference: "storage.<bucket>"
	OnDelete   string      `yaml:"on_delete" json:"on_delete"` // for storage refs: cascade | keep
}

// ForeignKey defines a foreign key reference.
type ForeignKey struct {
	References string `yaml:"references" json:"references"` // "table.column"
	OnDelete   string `yaml:"on_delete" json:"on_delete"`   // cascade | restrict | set_null
}

// ParseFKReference splits a foreign-key target string into (schema, table, column).
// 2-part inputs default to the public schema; 3-part inputs are schema-qualified.
// Anything else is an error.
func ParseFKReference(ref string) (schema, table, column string, err error) {
	parts := strings.Split(ref, ".")
	switch len(parts) {
	case 2:
		return "public", parts[0], parts[1], nil
	case 3:
		return parts[0], parts[1], parts[2], nil
	default:
		return "", "", "", fmt.Errorf("invalid foreign_key.references %q: expected table.column or schema.table.column", ref)
	}
}

// Index defines a table index.
type Index struct {
	Columns []string `yaml:"columns" json:"columns"`
	Unique  bool     `yaml:"unique" json:"unique"`
	Where   string   `yaml:"where" json:"where"` // partial index condition
}

// RLSPolicy defines a row-level security policy.
type RLSPolicy struct {
	Operations []string `yaml:"operations" json:"operations"`         // select, insert, update, delete
	Check      string   `yaml:"check" json:"check"`                   // SQL expression
	Type       string   `yaml:"type,omitempty" json:"type,omitempty"` // permissive (default) | restrictive
}

// Bucket defines a storage bucket.
type Bucket struct {
	MaxSize string      `yaml:"max_size" json:"max_size"`
	Types   []string    `yaml:"types" json:"types"`
	Public  bool        `yaml:"public" json:"public"`
	RLS     []RLSPolicy `yaml:"rls" json:"rls"`
}

// Function defines a user-declared RPC function. Each function becomes a real
// Postgres stored procedure (CREATE OR REPLACE FUNCTION), exposed at
// /rest/v1/rpc/<name> for supabase-js .rpc() compatibility.
type Function struct {
	Description  string     `yaml:"description" json:"description"`
	AuthRequired bool       `yaml:"auth_required" json:"auth_required"`
	Language     string     `yaml:"language,omitempty" json:"language,omitempty"`
	Volatility   string     `yaml:"volatility,omitempty" json:"volatility,omitempty"`
	Security     string     `yaml:"security,omitempty" json:"security,omitempty"`
	Args         []FuncArg  `yaml:"args,omitempty" json:"args,omitempty"`
	Body         string     `yaml:"body,omitempty" json:"body,omitempty"`
	Returns      FuncReturn `yaml:"returns" json:"returns"`

	// ReturnCategory is derived from Returns.Type at config load.
	// Values: "void" | "setof" | "scalar".
	ReturnCategory string `yaml:"-" json:"-"`
}

type FuncReturn struct {
	Type string `yaml:"type" json:"type"`
}

// FuncArg is one argument to an rpc-kind Function. Order matters: it drives
// $1/$2 positional mapping and the CREATE FUNCTION signature.
type FuncArg struct {
	Name     string `yaml:"name" json:"name"`
	Type     string `yaml:"type" json:"type"`
	Default  any    `yaml:"default" json:"default"`
	Required bool   `yaml:"required" json:"required"`
}

// CodeFunction is a user-declared HTTP handler written in JS, served at
// /functions/v1/<name>. Distinct from Function (the Postgres-RPC block, now
// under `rpc:`).
type CodeFunction struct {
	Runtime      string            `yaml:"runtime" json:"runtime"` // "node" (v1)
	File         string            `yaml:"file" json:"file"`       // path relative to config root
	AuthRequired bool              `yaml:"auth_required" json:"auth_required"`
	Timeout      string            `yaml:"timeout" json:"timeout"` // e.g. "30s"; default applied at runtime
	Env          map[string]string `yaml:"env" json:"env"`         // name -> literal or ${INSTANCEZ_ENV_*}
}

// --- Runtime types (not from YAML) ---

// User represents an authenticated user.
type User struct {
	ID            int64          `json:"id"`
	Email         string         `json:"email"`
	PasswordHash  string         `json:"-"`
	EmailVerified bool           `json:"email_verified"`
	CreatedAt     time.Time      `json:"created_at"`
	Extra         map[string]any `json:"extra,omitempty"` // auth.fields values
}

// Session holds JWT claims for the current request.
//
// UserID is the authenticated user's UUID as a string (empty when anonymous
// or when the request is authenticated with the admin key). Role mirrors the
// GoTrue/PostgREST contract: "anon" for unauthenticated requests,
// "authenticated" for normal user JWTs, "service_role" for admin-key
// requests. Email and JWT (raw encoded token) are populated from JWT claims
// so auth.email() / auth.jwt() SQL helpers can expose them to RLS policies.
type Session struct {
	UserID          string
	Role            string
	Email           string
	JWT             string
	IsAuthenticated bool
}

// StorageObject represents a file in the _objects table.
type StorageObject struct {
	ID         string         `json:"id"`
	BucketID   string         `json:"bucket_id"`
	Size       int64          `json:"size"`
	MIME       string         `json:"mime"`
	UploadedBy string         `json:"uploaded_by"`
	UploadedAt time.Time      `json:"uploaded_at"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// Migration records an applied migration.
type Migration struct {
	ID         int64     `json:"id"`
	Checksum   string    `json:"checksum"`
	SQL        string    `json:"sql"`
	ConfigJSON string    `json:"config_json"`
	AppliedAt  time.Time `json:"applied_at"`
}

