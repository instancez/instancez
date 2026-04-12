// Package domain contains pure types and interfaces (ports) for Ultrabase.
// This package has zero imports from adapter/, app/, or external packages.
package domain

import "time"

// Config is the top-level Ultrabase configuration parsed from YAML.
type Config struct {
	Version    int               `yaml:"version" json:"version"`
	Project    Project           `yaml:"project" json:"project"`
	Extensions []string          `yaml:"extensions" json:"extensions"`
	Server     Server            `yaml:"server" json:"server"`
	Providers  Providers         `yaml:"providers" json:"providers"`
	Auth       *Auth             `yaml:"auth" json:"auth"`
	Tables     map[string]Table  `yaml:"tables" json:"tables"`
	Storage    map[string]Bucket `yaml:"storage" json:"storage"`
	On         map[string]Trigger `yaml:"on" json:"on"`
	Functions  map[string]Function `yaml:"functions" json:"functions"`
	Seeds      map[string][]map[string]any `yaml:"seeds" json:"seeds"`
}

// Project holds display-only metadata.
type Project struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
}

// Server groups runtime/HTTP concerns.
type Server struct {
	Port        int          `yaml:"port" json:"port"`
	CORS        CORS         `yaml:"cors" json:"cors"`
	Timeouts    Timeouts     `yaml:"timeouts" json:"timeouts"`
	MaxBodySize string       `yaml:"max_body_size" json:"max_body_size"`
	DB          DBConfig     `yaml:"db" json:"db"`
	DocsUI      *bool        `yaml:"docs_ui" json:"docs_ui"`
	MaxLimit    int          `yaml:"max_limit" json:"max_limit"`
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

type DBConfig struct {
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
	Type string `yaml:"type" json:"type"` // resend | sendgrid | ses
}

type StorageProvider struct {
	Type string `yaml:"type" json:"type"` // s3 | gcs | minio | local
}

// Auth configures authentication.
type Auth struct {
	JWTExpiry           string            `yaml:"jwt_expiry" json:"jwt_expiry"`
	RefreshTokens       bool              `yaml:"refresh_tokens" json:"refresh_tokens"`
	RefreshTokenExpiry  string            `yaml:"refresh_token_expiry" json:"refresh_token_expiry"`
	Fields              map[string]Field  `yaml:"fields" json:"fields"`
	Email               *AuthEmail        `yaml:"email" json:"email"`
	Google              *OAuthProvider    `yaml:"google" json:"google"`
	GitHub              *OAuthProvider    `yaml:"github" json:"github"`
}

type AuthEmail struct {
	VerifyEmail bool                    `yaml:"verify_email" json:"verify_email"`
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
	Fields       map[string]Field    `yaml:"fields" json:"fields"`
	Indexes      []Index             `yaml:"indexes" json:"indexes"`
	RLS          []RLSPolicy         `yaml:"rls" json:"rls"`
	AllowAnon    bool                `yaml:"allow_anon" json:"allow_anon"`
	Searchable   []string            `yaml:"searchable" json:"searchable"`
	SearchConfig string              `yaml:"search_config" json:"search_config"`
}

// Field defines a table column.
type Field struct {
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
	Ref        string      `yaml:"ref" json:"ref"`               // storage reference: "storage.<bucket>"
	OnDelete   string      `yaml:"on_delete" json:"on_delete"`   // for storage refs: cascade | keep
}

// ForeignKey defines a foreign key reference.
type ForeignKey struct {
	References string `yaml:"references" json:"references"` // "table.column"
	OnDelete   string `yaml:"on_delete" json:"on_delete"`   // cascade | restrict | set_null
}

// Index defines a table index.
type Index struct {
	Columns []string `yaml:"columns" json:"columns"`
	Unique  bool     `yaml:"unique" json:"unique"`
	Where   string   `yaml:"where" json:"where"` // partial index condition
}

// RLSPolicy defines a row-level security policy.
type RLSPolicy struct {
	Operations []string `yaml:"operations" json:"operations"` // select, insert, update, delete
	Check      string   `yaml:"check" json:"check"`           // SQL expression
	Type       string   `yaml:"type,omitempty" json:"type,omitempty"` // permissive (default) | restrictive
}

// Bucket defines a storage bucket.
type Bucket struct {
	MaxSize string      `yaml:"max_size" json:"max_size"`
	Types   []string    `yaml:"types" json:"types"`
	Public  bool        `yaml:"public" json:"public"`
	RLS     []RLSPolicy `yaml:"rls" json:"rls"`
}

// Trigger defines an event-driven trigger.
type Trigger struct {
	Events   []string        `yaml:"events" json:"events"`       // WAL events: "table.operation"
	Schedule string          `yaml:"schedule" json:"schedule"`   // cron expression
	Webhook  *WebhookAction  `yaml:"webhook" json:"webhook"`
	Email    *EmailAction    `yaml:"email" json:"email"`
}

type WebhookAction struct {
	URL     string            `yaml:"url" json:"url"`
	Headers map[string]string `yaml:"headers" json:"headers"`
	Retry   RetryConfig       `yaml:"retry" json:"retry"`
}

type RetryConfig struct {
	Max     int    `yaml:"max" json:"max"`
	Backoff string `yaml:"backoff" json:"backoff"` // exponential | linear
}

type EmailAction struct {
	To        string `yaml:"to" json:"to"`
	ToQuery   string `yaml:"to_query" json:"to_query"`
	DataQuery string `yaml:"data_query" json:"data_query"`
	Subject   string `yaml:"subject" json:"subject"`
	Body      string `yaml:"body" json:"body"`
	BodyFile  string `yaml:"body_file" json:"body_file"`
	Condition string `yaml:"condition" json:"condition"`
}

// Function defines a custom SQL function exposed as a REST endpoint.
type Function struct {
	Description  string              `yaml:"description" json:"description"`
	Method       string              `yaml:"method" json:"method"` // GET | POST | PUT | DELETE
	Query        string              `yaml:"query" json:"query"`
	Params       map[string]FuncParam `yaml:"params" json:"params"`
	Returns      FuncReturn          `yaml:"returns" json:"returns"`
	AuthRequired bool                `yaml:"auth_required" json:"auth_required"`
}

type FuncParam struct {
	Type     string   `yaml:"type" json:"type"`
	Required bool     `yaml:"required" json:"required"`
	Default  any      `yaml:"default" json:"default"`
	Enum     []string `yaml:"enum" json:"enum"`
	Min      *float64 `yaml:"min" json:"min"`
	Max      *float64 `yaml:"max" json:"max"`
}

type FuncReturn struct {
	Type   string            `yaml:"type" json:"type"` // rows | row | scalar | void
	Schema map[string]string `yaml:"schema" json:"schema"`
}

// --- Runtime types (not from YAML) ---

// Event represents a WAL change event dispatched to triggers.
type Event struct {
	ID        string         `json:"id"`
	EventName string         `json:"event"`
	Table     string         `json:"table"`
	Operation string         `json:"operation"`
	Timestamp time.Time      `json:"timestamp"`
	Data      map[string]any `json:"data"`
	OldData   map[string]any `json:"old_data"`
}

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
	ID        int64     `json:"id"`
	Checksum  string    `json:"checksum"`
	SQL       string    `json:"sql"`
	AppliedAt time.Time `json:"applied_at"`
}
