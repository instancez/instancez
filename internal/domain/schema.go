// Package domain contains pure types and interfaces (ports) for Instancez.
// This package has zero imports from adapter/, app/, or external packages.
package domain

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
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
	Data       map[string]TableData    `yaml:"data" json:"data"`

	// FunctionsBundle is a pointer to the pre-built functions bundle that
	// `serve` consumes at runtime (it never builds). `inz deploy` builds the
	// bundle (vendoring node_modules), uploads it, and records the pointer here.
	// The value is an object URI carrying a version token, e.g.
	// "s3://bucket/key#<sha256>". Empty when the project has no code functions
	// or the deploy was run without a bundle destination. Consumed by Task 12;
	// not read by the runtime yet.
	FunctionsBundle string `yaml:"functions_bundle" json:"functions_bundle"`
}

// TableData holds either inline rows (a list) or CSV file references (a label→path map).
// The YAML value under data.<table> can be either format.
type TableData struct {
	Rows     []map[string]any  // set when YAML value is a sequence
	CSVFiles map[string]string // set when YAML value is a mapping (label → file path)
}

func (td *TableData) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.SequenceNode:
		return value.Decode(&td.Rows)
	case yaml.MappingNode:
		return value.Decode(&td.CSVFiles)
	default:
		return fmt.Errorf("data entry must be a sequence (inline rows) or mapping (csv files)")
	}
}

// Project holds display-only metadata.
type Project struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
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
	Type      string `yaml:"type" json:"type"`             // resend | sendgrid | ses
	FromEmail string `yaml:"from_email" json:"from_email"` // e.g. "instancez <noreply@example.com>"
}

type StorageProvider struct {
	Type string `yaml:"type" json:"type"` // s3 | local
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
	Email              *AuthEmail     `yaml:"email" json:"email"`
	Google             *OAuthProvider `yaml:"google" json:"google"`
	GitHub             *OAuthProvider `yaml:"github" json:"github"`
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
	Schema       string      `yaml:"schema" json:"schema"`
	Fields       []Field     `yaml:"fields" json:"fields"`
	Indexes      []Index     `yaml:"indexes" json:"indexes"`
	RLS          []RLSPolicy `yaml:"rls" json:"rls"`
	Searchable   []string    `yaml:"searchable" json:"searchable"`
	SearchConfig string      `yaml:"search_config" json:"search_config"`
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

// DataRecord tracks an applied CSV data import.
type DataRecord struct {
	Key       string    `json:"key"`
	TableName string    `json:"table_name"`
	Source    string    `json:"source"`
	Checksum  string    `json:"checksum"`
	RowCount  int       `json:"row_count"`
	AppliedAt time.Time `json:"applied_at"`
}
