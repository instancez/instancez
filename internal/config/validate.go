package config

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/instancez/instancez/internal/domain"
)

// identRE enforces safe SQL identifiers for all user-defined names (tables,
// columns, buckets, triggers, functions, args). Must start with a lowercase
// letter, then lowercase letters/digits/underscores, max 63 chars total.
var identRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

const identRule = "must start with a lowercase letter, followed by lowercase letters, digits, or underscores (max 63 chars)"

// reservedSQLKeywords contains Postgres keywords that cannot appear as a bare
// identifier in a DDL or query without double-quoting. We reject them at
// validate time because the migrator and PostgREST surface interpolate
// identifiers raw — e.g. `ALTER TABLE %s ADD COLUMN %s ...`. Sourced from
// the "reserved" and "reserved (can be function or type name)" rows of
// Postgres Appendix C: https://www.postgresql.org/docs/current/sql-keywords-appendix.html
//
// `users` is intentionally absent: it's the auth users table and is used as
// a bare identifier in our own migrations.
var reservedSQLKeywords = map[string]bool{
	"all": true, "analyse": true, "analyze": true, "and": true, "any": true,
	"array": true, "as": true, "asc": true, "asymmetric": true, "authorization": true,
	"binary": true, "both": true, "case": true, "cast": true, "check": true,
	"collate": true, "collation": true, "column": true, "concurrently": true,
	"constraint": true, "create": true, "cross": true, "current_catalog": true,
	"current_date": true, "current_role": true, "current_schema": true,
	"current_time": true, "current_timestamp": true, "current_user": true,
	"default": true, "deferrable": true, "desc": true, "distinct": true,
	"do": true, "else": true, "end": true, "except": true, "false": true,
	"fetch": true, "for": true, "foreign": true, "freeze": true, "from": true,
	"full": true, "grant": true, "group": true, "having": true, "ilike": true,
	"in": true, "initially": true, "inner": true, "intersect": true,
	"into": true, "is": true, "isnull": true, "join": true, "lateral": true,
	"leading": true, "left": true, "like": true, "limit": true,
	"localtime": true, "localtimestamp": true, "natural": true, "not": true,
	"notnull": true, "null": true, "offset": true, "on": true, "only": true,
	"or": true, "order": true, "outer": true, "overlaps": true, "placing": true,
	"primary": true, "references": true, "returning": true, "right": true,
	"select": true, "session_user": true, "similar": true, "some": true,
	"symmetric": true, "system_user": true, "table": true, "tablesample": true,
	"then": true, "to": true, "trailing": true, "true": true, "union": true,
	"unique": true, "user": true, "using": true, "variadic": true, "verbose": true,
	"when": true, "where": true, "window": true, "with": true,
}

func validateIdent(path, name string) *domain.ValidationError {
	if !identRE.MatchString(name) {
		return &domain.ValidationError{
			Path:    path,
			Message: fmt.Sprintf("invalid identifier %q: %s", name, identRule),
		}
	}
	if reservedSQLKeywords[name] {
		return &domain.ValidationError{
			Path:       path,
			Message:    fmt.Sprintf("%q is a reserved SQL keyword", name),
			Suggestion: "Pick a different name (e.g. add a prefix or suffix)",
		}
	}
	return nil
}

// dbFunctionTypeRE permits a conservative subset of Postgres type syntax:
// identifiers, whitespace, commas, parentheses and brackets. This covers
// scalar types, "setof foo", and "table(id int, name text)". It deliberately
// excludes semicolons, quotes, and dollar signs so a malicious YAML can't
// inject CREATE/DROP statements through the returns or arg type fields.
var dbFunctionTypeRE = regexp.MustCompile(`^[a-zA-Z0-9_ ,()\[\]]{1,256}$`)

// validPostgresTypes is a non-exhaustive allowlist of common Postgres type prefixes.
var validPostgresTypes = map[string]bool{
	"bigserial":   true,
	"serial":      true,
	"smallserial": true,
	"integer":     true,
	"bigint":      true,
	"smallint":    true,
	"int":         true,
	"text":        true,
	"varchar":     true,
	"char":        true,
	"boolean":     true,
	"bool":        true,
	"numeric":     true,
	"decimal":     true,
	"real":        true,
	"double":      true,
	"float":       true,
	"date":        true,
	"time":        true,
	"timetz":      true,
	"timestamp":   true,
	"timestamptz": true,
	"interval":    true,
	"uuid":        true,
	"jsonb":       true,
	"json":        true,
	"bytea":       true,
	"inet":        true,
	"cidr":        true,
	"macaddr":     true,
	"money":       true,
	"point":       true,
	"line":        true,
	"polygon":     true,
	"circle":      true,
	"box":         true,
	"tsquery":     true,
	"tsvector":    true,
	"xml":         true,
	"bit":         true,
}

var validOnDelete = map[string]bool{
	"cascade":  true,
	"restrict": true,
	"set_null": true,
}

var validRLSTypes = map[string]bool{
	"":            true, // default (permissive)
	"permissive":  true,
	"restrictive": true,
}

var validRLSOps = map[string]bool{
	"select": true,
	"insert": true,
	"update": true,
	"delete": true,
}

var validEmailProviders = map[string]bool{
	"resend": true, "sendgrid": true, "ses": true,
}

var validStorageProviders = map[string]bool{
	"s3": true, "local": true,
}

// allowedDefaults is the allowlist of SQL functions usable in field defaults.
var allowedDefaults = map[string]bool{
	"now()":        true,
	"uuid_v7()":    true,
	"uuid_v4()":    true,
	"current_date": true,
	"current_time": true,
}

// reservedTableNames cannot be used by user-defined tables.
var reservedTableNames = map[string]bool{}

// Validate checks a Config for all errors and returns them as a collected list.
func Validate(cfg *domain.Config) domain.ValidationErrors {
	var errs domain.ValidationErrors

	if cfg.Version != 1 {
		errs = append(errs, &domain.ValidationError{
			Path:       "version",
			Message:    fmt.Sprintf("unsupported version %d (expected 1)", cfg.Version),
			Suggestion: "Set version: 1",
		})
	}

	errs = append(errs, validateProviders(&cfg.Providers)...)
	errs = append(errs, validateAuth(cfg.Auth)...)
	errs = append(errs, validateTables(cfg.Tables, cfg.Auth)...)
	errs = append(errs, validateStorage(cfg.Storage)...)
	errs = append(errs, validateRPC(cfg.RPC)...)
	errs = append(errs, validateCodeFunctions(cfg.Functions)...)
	errs = append(errs, validateData(cfg.Data, cfg.Tables)...)

	// Cross-cutting: FK reference validation
	errs = append(errs, validateForeignKeys(cfg.Tables)...)

	if len(errs) == 0 {
		return nil
	}
	return errs
}

func validateProviders(p *domain.Providers) domain.ValidationErrors {
	var errs domain.ValidationErrors
	if p.Email != nil {
		if !validEmailProviders[p.Email.Type] {
			errs = append(errs, &domain.ValidationError{
				Path:       "providers.email.type",
				Message:    fmt.Sprintf("unknown email provider type %q", p.Email.Type),
				Suggestion: "Supported types: resend, sendgrid, ses",
			})
		}
	}
	if p.Storage != nil {
		if !validStorageProviders[p.Storage.Type] {
			errs = append(errs, &domain.ValidationError{
				Path:       "providers.storage.type",
				Message:    fmt.Sprintf("unknown storage provider type %q", p.Storage.Type),
				Suggestion: "Supported types: s3, local",
			})
		}
	}
	return errs
}

func validateAuth(auth *domain.Auth) domain.ValidationErrors {
	if auth == nil {
		return nil
	}
	var errs domain.ValidationErrors

	// Validate email templates
	if auth.Email != nil {
		for name, tmpl := range auth.Email.Templates {
			path := fmt.Sprintf("auth.email.templates.%s", name)
			if tmpl.Subject == "" {
				errs = append(errs, &domain.ValidationError{
					Path:    path + ".subject",
					Message: "subject is required",
				})
			}
			if tmpl.Body == "" && tmpl.BodyFile == "" {
				errs = append(errs, &domain.ValidationError{
					Path:    path,
					Message: "either body or body_file is required",
				})
			}
			if tmpl.Body != "" && tmpl.BodyFile != "" {
				errs = append(errs, &domain.ValidationError{
					Path:    path,
					Message: "specify body or body_file, not both",
				})
			}
		}
	}

	// Validate OAuth providers
	if auth.Google != nil {
		if auth.Google.ClientID == "" {
			errs = append(errs, &domain.ValidationError{Path: "auth.google.client_id", Message: "required"})
		}
		if auth.Google.ClientSecret == "" {
			errs = append(errs, &domain.ValidationError{Path: "auth.google.client_secret", Message: "required"})
		}
	}
	if auth.GitHub != nil {
		if auth.GitHub.ClientID == "" {
			errs = append(errs, &domain.ValidationError{Path: "auth.github.client_id", Message: "required"})
		}
		if auth.GitHub.ClientSecret == "" {
			errs = append(errs, &domain.ValidationError{Path: "auth.github.client_secret", Message: "required"})
		}
	}

	return errs
}

func validateTables(tables map[string]domain.Table, auth *domain.Auth) domain.ValidationErrors {
	var errs domain.ValidationErrors

	for name, table := range tables {
		path := fmt.Sprintf("tables.%s", name)

		if err := validateIdent(path, name); err != nil {
			errs = append(errs, err)
		}

		if table.Schema != "" {
			if err := validateIdent(path+".schema", table.Schema); err != nil {
				errs = append(errs, err)
			}
			// auth and storage schemas are owned by the framework.
			if table.Schema == "auth" || table.Schema == "storage" {
				errs = append(errs, &domain.ValidationError{
					Path:       fmt.Sprintf("tables.%s.schema", name),
					Message:    fmt.Sprintf("schema %q is reserved by the framework", table.Schema),
					Suggestion: "Use a different schema or omit it to default to public.",
				})
				continue
			}
		}

		// Reserved name check
		if reservedTableNames[name] {
			errs = append(errs, &domain.ValidationError{
				Path:       path,
				Message:    fmt.Sprintf("%q is reserved for auth", name),
				Suggestion: "Use a different table name",
			})
		}

		// Must have at least one field (users table may have zero custom fields
		// since core columns are auto-generated by migrations)
		if len(table.Fields) == 0 && (name != "users" || auth == nil) {
			errs = append(errs, &domain.ValidationError{
				Path:    path,
				Message: "table must have at least one field",
			})
			continue
		}

		// Validate fields
		hasPK := false
		for _, field := range table.Fields {
			fpath := fmt.Sprintf("%s.fields.%s", path, field.Name)

			if err := validateIdent(fpath, field.Name); err != nil {
				errs = append(errs, err)
			}

			if field.PrimaryKey {
				hasPK = true
			}

			// FK fields infer type — others must declare it
			if field.ForeignKey == nil && field.Type == "" {
				errs = append(errs, &domain.ValidationError{
					Path:    fpath,
					Message: "type is required (or use foreign_key to infer type)",
				})
			}
			if field.Type != "" {
				errs = append(errs, validateFieldType(fpath, field.Type)...)
			}

			// Validate default values
			if field.Default != nil {
				errs = append(errs, validateDefault(fpath, field.Default)...)
			}

			// FK validation (structural — cross-ref checked separately)
			if field.ForeignKey != nil {
				fkPath := fpath + ".foreign_key"
				if field.ForeignKey.References == "" {
					errs = append(errs, &domain.ValidationError{
						Path:    fkPath + ".references",
						Message: "references is required",
					})
				} else if !strings.Contains(field.ForeignKey.References, ".") {
					errs = append(errs, &domain.ValidationError{
						Path:       fkPath + ".references",
						Message:    fmt.Sprintf("invalid reference %q", field.ForeignKey.References),
						Suggestion: "Use format: table.column",
					})
				}
				if field.ForeignKey.OnDelete != "" && !validOnDelete[field.ForeignKey.OnDelete] {
					errs = append(errs, &domain.ValidationError{
						Path:       fkPath + ".on_delete",
						Message:    fmt.Sprintf("invalid on_delete %q", field.ForeignKey.OnDelete),
						Suggestion: "Supported: cascade, restrict, set_null",
					})
				}
			}

			// Validate enum
			if len(field.Enum) > 0 && field.Type != "" && !isStringType(field.Type) {
				errs = append(errs, &domain.ValidationError{
					Path:    fpath + ".enum",
					Message: "enum is only supported on text/varchar types",
				})
			}
		}

		isAuthUsers := name == "users" && auth != nil
		if !hasPK && !isAuthUsers {
			errs = append(errs, &domain.ValidationError{
				Path:       path,
				Message:    "table must have a primary key",
				Suggestion: "Add primary_key: true to one of the fields",
			})
		}

		// Validate indexes
		for i, idx := range table.Indexes {
			idxPath := fmt.Sprintf("%s.indexes[%d]", path, i)
			if len(idx.Columns) == 0 {
				errs = append(errs, &domain.ValidationError{
					Path:    idxPath,
					Message: "index must have at least one column",
				})
			}
			for _, col := range idx.Columns {
				if _, ok := table.GetField(col); !ok {
					errs = append(errs, &domain.ValidationError{
						Path:       idxPath,
						Message:    fmt.Sprintf("column %q not found in table fields", col),
						Suggestion: fmt.Sprintf("Add %q to the fields section or check spelling", col),
					})
				}
			}
		}

		// Validate RLS
		errs = append(errs, validateRLS(path, table.RLS)...)
	}

	return errs
}

func validateRLS(parentPath string, policies []domain.RLSPolicy) domain.ValidationErrors {
	var errs domain.ValidationErrors
	for i, p := range policies {
		ppath := fmt.Sprintf("%s.rls[%d]", parentPath, i)
		if len(p.Operations) == 0 {
			errs = append(errs, &domain.ValidationError{
				Path:    ppath + ".operations",
				Message: "at least one operation is required",
			})
		}
		for _, op := range p.Operations {
			if !validRLSOps[op] {
				errs = append(errs, &domain.ValidationError{
					Path:       ppath + ".operations",
					Message:    fmt.Sprintf("invalid operation %q", op),
					Suggestion: "Supported: select, insert, update, delete",
				})
			}
		}
		if !validRLSTypes[p.Type] {
			errs = append(errs, &domain.ValidationError{
				Path:       ppath + ".type",
				Message:    fmt.Sprintf("invalid policy type %q", p.Type),
				Suggestion: "Supported: permissive, restrictive",
			})
		}
		if p.Check == "" {
			errs = append(errs, &domain.ValidationError{
				Path:    ppath + ".check",
				Message: "check expression is required",
			})
		}
	}
	return errs
}

func validateStorage(storage map[string]domain.Bucket) domain.ValidationErrors {
	var errs domain.ValidationErrors
	for name, bucket := range storage {
		path := fmt.Sprintf("storage.%s", name)

		if err := validateIdent(path, name); err != nil {
			errs = append(errs, err)
		}

		errs = append(errs, validateRLS(path, bucket.RLS)...)

		if bucket.MaxSize != "" {
			if !isValidSize(bucket.MaxSize) {
				errs = append(errs, &domain.ValidationError{
					Path:       path + ".max_size",
					Message:    fmt.Sprintf("invalid size %q", bucket.MaxSize),
					Suggestion: "Use format like 2MB, 50MB, 1GB",
				})
			}
		}
	}
	return errs
}

func validateRPC(functions map[string]domain.Function) domain.ValidationErrors {
	var errs domain.ValidationErrors
	for name, fn := range functions {
		path := fmt.Sprintf("rpc.%s", name)
		errs = append(errs, validateRPCFunction(path, name, fn)...)
	}
	return errs
}

var validRPCLanguages = map[string]bool{
	"sql":     true,
	"plpgsql": true,
}

var validRPCVolatility = map[string]bool{
	"volatile":  true,
	"stable":    true,
	"immutable": true,
}

var validRPCSecurity = map[string]bool{
	"invoker": true,
	"definer": true,
}

func validateRPCFunction(path, name string, fn domain.Function) domain.ValidationErrors {
	var errs domain.ValidationErrors

	if err := validateIdent(path, name); err != nil {
		errs = append(errs, err)
		return errs
	}

	if fn.Body == "" {
		errs = append(errs, &domain.ValidationError{
			Path:    path + ".body",
			Message: "body is required",
		})
	}
	if strings.Contains(fn.Body, "$ub$") {
		errs = append(errs, &domain.ValidationError{
			Path:       path + ".body",
			Message:    "body must not contain the reserved dollar-quote tag $ub$",
			Suggestion: "Rename any local dollar-quoted literals",
		})
	}

	if fn.Returns.Type == "" {
		errs = append(errs, &domain.ValidationError{
			Path:    path + ".returns.type",
			Message: "returns.type is required (use \"void\" for no return value)",
		})
	} else if !dbFunctionTypeRE.MatchString(fn.Returns.Type) {
		errs = append(errs, &domain.ValidationError{
			Path:    path + ".returns.type",
			Message: fmt.Sprintf("invalid return type %q", fn.Returns.Type),
		})
	}

	if !validRPCLanguages[strings.ToLower(fn.Language)] {
		errs = append(errs, &domain.ValidationError{
			Path:       path + ".language",
			Message:    fmt.Sprintf("unsupported language %q", fn.Language),
			Suggestion: "Supported: sql, plpgsql",
		})
	}
	if !validRPCVolatility[strings.ToLower(fn.Volatility)] {
		errs = append(errs, &domain.ValidationError{
			Path:       path + ".volatility",
			Message:    fmt.Sprintf("invalid volatility %q", fn.Volatility),
			Suggestion: "Supported: volatile, stable, immutable",
		})
	}
	if !validRPCSecurity[strings.ToLower(fn.Security)] {
		errs = append(errs, &domain.ValidationError{
			Path:       path + ".security",
			Message:    fmt.Sprintf("invalid security %q", fn.Security),
			Suggestion: "Supported: invoker (default), definer",
		})
	}

	seenArgs := make(map[string]bool, len(fn.Args))
	for i, arg := range fn.Args {
		argPath := fmt.Sprintf("%s.args[%d]", path, i)
		if err := validateIdent(argPath+".name", arg.Name); err != nil {
			errs = append(errs, err)
			continue
		}
		if seenArgs[arg.Name] {
			errs = append(errs, &domain.ValidationError{
				Path:    argPath + ".name",
				Message: fmt.Sprintf("duplicate arg name %q", arg.Name),
			})
		}
		seenArgs[arg.Name] = true
		if arg.Type == "" {
			errs = append(errs, &domain.ValidationError{
				Path:    argPath + ".type",
				Message: "type is required",
			})
		} else if !dbFunctionTypeRE.MatchString(arg.Type) {
			errs = append(errs, &domain.ValidationError{
				Path:    argPath + ".type",
				Message: fmt.Sprintf("invalid type %q", arg.Type),
			})
		}
	}
	return errs
}

// codeFunctionNameRE enforces safe URL path segments for code function names.
// Must start with an alphanumeric character, followed by alphanumerics, hyphens, or underscores.
var codeFunctionNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

func validateCodeFunctions(functions map[string]domain.CodeFunction) domain.ValidationErrors {
	var errs domain.ValidationErrors
	for name, fn := range functions {
		path := fmt.Sprintf("functions.%s", name)

		// Detect old-RPC-shape accidentally left under functions: (runtime and file both unset).
		// This is a strong signal the user copy-pasted a Postgres function block, but it also
		// covers an empty/stub entry where the user simply forgot to fill in the fields.
		if fn.Runtime == "" && fn.File == "" {
			errs = append(errs, &domain.ValidationError{
				Path:       path,
				Message:    fmt.Sprintf("function %q: runtime and file are required", name),
				Suggestion: "If this is a Postgres function, move it to the `rpc:` block.",
			})
			continue
		}

		if !codeFunctionNameRE.MatchString(name) {
			errs = append(errs, &domain.ValidationError{
				Path:       path,
				Message:    fmt.Sprintf("invalid function name %q", name),
				Suggestion: "Use letters, digits, hyphens or underscores; must not start with a hyphen.",
			})
		}

		if fn.Runtime != "node" {
			errs = append(errs, &domain.ValidationError{
				Path:       path + ".runtime",
				Message:    fmt.Sprintf("unsupported runtime %q", fn.Runtime),
				Suggestion: `Supported: "node"`,
			})
		}

		if fn.File == "" {
			errs = append(errs, &domain.ValidationError{
				Path:    path + ".file",
				Message: "file is required",
			})
		}

		if fn.Timeout != "" {
			if _, err := time.ParseDuration(fn.Timeout); err != nil {
				errs = append(errs, &domain.ValidationError{
					Path:       path + ".timeout",
					Message:    fmt.Sprintf("invalid timeout %q: %v", fn.Timeout, err),
					Suggestion: `Use a Go duration string, e.g. "30s", "1m30s"`,
				})
			}
		}
	}
	return errs
}

func validateData(data map[string]domain.TableData, tables map[string]domain.Table) domain.ValidationErrors {
	var errs domain.ValidationErrors
	for tableName, td := range data {
		basePath := fmt.Sprintf("data.%s", tableName)
		if tableName != "users" {
			if _, ok := tables[tableName]; !ok {
				errs = append(errs, &domain.ValidationError{
					Path:       basePath,
					Message:    fmt.Sprintf("data references unknown table %q", tableName),
					Suggestion: fmt.Sprintf("Define a %q table or check spelling", tableName),
				})
			}
		}
		if td.Rows != nil && len(td.Rows) == 0 {
			errs = append(errs, &domain.ValidationError{
				Path:    basePath,
				Message: "data list is empty",
			})
		}
		for key, source := range td.CSVFiles {
			if source == "" {
				errs = append(errs, &domain.ValidationError{
					Path:    fmt.Sprintf("data.%s.%s", tableName, key),
					Message: "source path is empty",
				})
			}
		}
	}
	return errs
}

func validateForeignKeys(tables map[string]domain.Table) domain.ValidationErrors {
	var errs domain.ValidationErrors

	// Build set of known tables + their fields.
	// The users table has implicit core columns (id, email, etc.) added by
	// migrations, so seed them before merging user-declared fields.
	knownTables := map[string]map[string]domain.Field{
		"users": {"id": {Type: "uuid", PrimaryKey: true}, "email": {Type: "text"}},
	}
	for name, table := range tables {
		if name == "users" {
			for _, f := range table.Fields {
				knownTables["users"][f.Name] = f
			}
		} else {
			knownTables[name] = table.FieldMap()
		}
	}

	for tableName, table := range tables {
		for _, field := range table.Fields {
			fieldName := field.Name
			if field.ForeignKey == nil || field.ForeignKey.References == "" {
				continue
			}
			fkPath := fmt.Sprintf("tables.%s.fields.%s.foreign_key", tableName, fieldName)
			schema, refTable, refCol, err := domain.ParseFKReference(field.ForeignKey.References)
			if err != nil {
				continue // already caught by structural validation
			}
			// Cross-schema references (e.g. auth.users.id) target tables outside this
			// config's tables map. We don't validate their existence here — that's the
			// migrator's job at apply time.
			if schema != "public" {
				continue
			}
			targetFields, tableExists := knownTables[refTable]
			if !tableExists {
				errs = append(errs, &domain.ValidationError{
					Path:       fkPath + ".references",
					Message:    fmt.Sprintf("references table %q which does not exist", refTable),
					Suggestion: fmt.Sprintf("Define a %q table or check spelling", refTable),
				})
				continue
			}

			if _, colExists := targetFields[refCol]; !colExists {
				errs = append(errs, &domain.ValidationError{
					Path:    fkPath + ".references",
					Message: fmt.Sprintf("references %s.%s but column %q does not exist on table %q", refTable, refCol, refCol, refTable),
				})
			}
		}
	}

	return errs
}

// validateFieldType checks if a type string looks like a valid Postgres type.
func validateFieldType(path, typ string) domain.ValidationErrors {
	var errs domain.ValidationErrors
	if typ == "" {
		return errs
	}

	// Normalize: strip array suffix, parameterization
	base := strings.TrimSuffix(typ, "[]")
	if idx := strings.Index(base, "("); idx != -1 {
		base = base[:idx]
	}
	base = strings.ToLower(strings.TrimSpace(base))

	// "double precision" is two words
	if base == "double precision" || base == "double" {
		return errs
	}

	if !validPostgresTypes[base] {
		errs = append(errs, &domain.ValidationError{
			Path:    path + ".type",
			Message: fmt.Sprintf("unknown type %q", typ),
		})
	}
	return errs
}

func validateDefault(path string, val any) domain.ValidationErrors {
	var errs domain.ValidationErrors
	switch v := val.(type) {
	case string:
		lower := strings.ToLower(v)
		// Check if it's a SQL function call (contains parens or is a known keyword)
		if strings.Contains(v, "(") || lower == "current_date" || lower == "current_time" {
			if !allowedDefaults[lower] {
				errs = append(errs, &domain.ValidationError{
					Path:       path + ".default",
					Message:    fmt.Sprintf("SQL expression %q is not in the allowed defaults list", v),
					Suggestion: "Allowed: now(), uuid_v7(), uuid_v4(), current_date, current_time",
				})
			}
		}
		// Plain string literals are always OK
	case int, int64, float64, bool:
		// Literal values are always OK
	default:
		errs = append(errs, &domain.ValidationError{
			Path:    path + ".default",
			Message: fmt.Sprintf("unsupported default value type %T", val),
		})
	}
	return errs
}

func isStringType(typ string) bool {
	lower := strings.ToLower(typ)
	return lower == "text" || strings.HasPrefix(lower, "varchar") || strings.HasPrefix(lower, "char")
}

func isValidSize(s string) bool {
	s = strings.ToUpper(strings.TrimSpace(s))
	for _, suffix := range []string{"KB", "MB", "GB", "TB"} {
		if strings.HasSuffix(s, suffix) {
			num := strings.TrimSuffix(s, suffix)
			num = strings.TrimSpace(num)
			if num == "" {
				return false
			}
			for _, c := range num {
				if (c < '0' || c > '9') && c != '.' {
					return false
				}
			}
			return true
		}
	}
	return false
}
