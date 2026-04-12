package config

import (
	"fmt"
	"strings"

	"github.com/saedx1/ultrabase/internal/domain"
)

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
	"":           true, // default (permissive)
	"permissive": true,
	"restrictive": true,
}

var validRLSOps = map[string]bool{
	"select": true,
	"insert": true,
	"update": true,
	"delete": true,
}

var validFuncMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "DELETE": true,
}

var validReturnTypes = map[string]bool{
	"rows": true, "row": true, "scalar": true, "void": true,
}

var validEmailProviders = map[string]bool{
	"resend": true, "sendgrid": true, "ses": true,
}

var validStorageProviders = map[string]bool{
	"s3": true, "gcs": true, "minio": true, "local": true,
}

var validBackoff = map[string]bool{
	"exponential": true, "linear": true,
}

// allowedDefaults is the allowlist of SQL functions usable in field defaults.
var allowedDefaults = map[string]bool{
	"now()":         true,
	"uuid_v7()":     true,
	"uuid_v4()":     true,
	"current_date":  true,
	"current_time":  true,
}

// reservedTableNames cannot be used by user-defined tables.
var reservedTableNames = map[string]bool{
	"users": true,
}

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
	errs = append(errs, validateTables(cfg.Tables)...)
	errs = append(errs, validateStorage(cfg.Storage)...)
	errs = append(errs, validateTriggers(cfg.On, cfg.Tables)...)
	errs = append(errs, validateFunctions(cfg.Functions)...)
	errs = append(errs, validateSeeds(cfg.Seeds, cfg.Tables)...)

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
				Suggestion: "Supported types: s3, gcs, minio, local",
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

	// Validate custom fields on the users table
	for name, f := range auth.Fields {
		path := fmt.Sprintf("auth.fields.%s", name)
		errs = append(errs, validateFieldType(path, f.Type)...)
	}

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

func validateTables(tables map[string]domain.Table) domain.ValidationErrors {
	var errs domain.ValidationErrors

	for name, table := range tables {
		path := fmt.Sprintf("tables.%s", name)

		// Reserved name check
		if reservedTableNames[name] {
			errs = append(errs, &domain.ValidationError{
				Path:       path,
				Message:    fmt.Sprintf("%q is reserved for auth", name),
				Suggestion: "Use a different table name",
			})
		}
		if strings.HasPrefix(name, "_") {
			errs = append(errs, &domain.ValidationError{
				Path:       path,
				Message:    "table names starting with '_' are reserved for framework tables",
				Suggestion: "Use a name without the '_' prefix",
			})
		}

		// Must have at least one field
		if len(table.Fields) == 0 {
			errs = append(errs, &domain.ValidationError{
				Path:    path,
				Message: "table must have at least one field",
			})
			continue
		}

		// Validate fields
		hasPK := false
		for fname, field := range table.Fields {
			fpath := fmt.Sprintf("%s.fields.%s", path, fname)

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

		if !hasPK {
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
				if _, ok := table.Fields[col]; !ok {
					// Could be a FK field — check that too
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

		// Validate searchable columns exist
		for _, col := range table.Searchable {
			if _, ok := table.Fields[col]; !ok {
				errs = append(errs, &domain.ValidationError{
					Path:       path + ".searchable",
					Message:    fmt.Sprintf("searchable column %q not found in table fields", col),
					Suggestion: fmt.Sprintf("Add %q to the fields section", col),
				})
			}
		}
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

func validateTriggers(triggers map[string]domain.Trigger, tables map[string]domain.Table) domain.ValidationErrors {
	var errs domain.ValidationErrors
	for name, trigger := range triggers {
		path := fmt.Sprintf("on.%s", name)

		// Must have either events or schedule
		if len(trigger.Events) == 0 && trigger.Schedule == "" {
			errs = append(errs, &domain.ValidationError{
				Path:    path,
				Message: "trigger must have events or schedule",
			})
		}
		if len(trigger.Events) > 0 && trigger.Schedule != "" {
			errs = append(errs, &domain.ValidationError{
				Path:    path,
				Message: "trigger cannot have both events and schedule",
			})
		}

		// Must have at least one action
		if trigger.Webhook == nil && trigger.Email == nil {
			errs = append(errs, &domain.ValidationError{
				Path:    path,
				Message: "trigger must have at least one action (webhook or email)",
			})
		}

		// Validate event patterns
		for _, evt := range trigger.Events {
			parts := strings.SplitN(evt, ".", 2)
			if len(parts) != 2 {
				errs = append(errs, &domain.ValidationError{
					Path:       path + ".events",
					Message:    fmt.Sprintf("invalid event pattern %q", evt),
					Suggestion: "Use format: table.operation (e.g. todos.insert, *.delete)",
				})
				continue
			}
			tableName, op := parts[0], parts[1]
			if tableName != "*" {
				// Check table exists (including "users" for auth events)
				if _, ok := tables[tableName]; !ok && tableName != "users" {
					errs = append(errs, &domain.ValidationError{
						Path:       path + ".events",
						Message:    fmt.Sprintf("event references unknown table %q", tableName),
						Suggestion: fmt.Sprintf("Define a %q table or use '*' for all tables", tableName),
					})
				}
			}
			if op != "*" && op != "insert" && op != "update" && op != "delete" {
				errs = append(errs, &domain.ValidationError{
					Path:       path + ".events",
					Message:    fmt.Sprintf("invalid operation %q in event pattern", op),
					Suggestion: "Supported: insert, update, delete, *",
				})
			}
		}

		// Validate webhook action
		if trigger.Webhook != nil {
			if trigger.Webhook.URL == "" {
				errs = append(errs, &domain.ValidationError{
					Path:    path + ".webhook.url",
					Message: "url is required",
				})
			}
			if trigger.Webhook.Retry.Backoff != "" && !validBackoff[trigger.Webhook.Retry.Backoff] {
				errs = append(errs, &domain.ValidationError{
					Path:       path + ".webhook.retry.backoff",
					Message:    fmt.Sprintf("invalid backoff %q", trigger.Webhook.Retry.Backoff),
					Suggestion: "Supported: exponential, linear",
				})
			}
		}

		// Validate email action
		if trigger.Email != nil {
			if trigger.Email.To == "" && trigger.Email.ToQuery == "" {
				errs = append(errs, &domain.ValidationError{
					Path:    path + ".email",
					Message: "either to or to_query is required",
				})
			}
			if trigger.Email.Subject == "" {
				errs = append(errs, &domain.ValidationError{
					Path:    path + ".email.subject",
					Message: "subject is required",
				})
			}
			if trigger.Email.Body == "" && trigger.Email.BodyFile == "" {
				errs = append(errs, &domain.ValidationError{
					Path:    path + ".email",
					Message: "either body or body_file is required",
				})
			}
		}
	}
	return errs
}

func validateFunctions(functions map[string]domain.Function) domain.ValidationErrors {
	var errs domain.ValidationErrors
	for name, fn := range functions {
		path := fmt.Sprintf("functions.%s", name)

		if fn.Query == "" {
			errs = append(errs, &domain.ValidationError{
				Path:    path + ".query",
				Message: "query is required",
			})
		}

		if fn.Method != "" && !validFuncMethods[strings.ToUpper(fn.Method)] {
			errs = append(errs, &domain.ValidationError{
				Path:       path + ".method",
				Message:    fmt.Sprintf("invalid method %q", fn.Method),
				Suggestion: "Supported: GET, POST, PUT, DELETE",
			})
		}

		if !validReturnTypes[fn.Returns.Type] {
			errs = append(errs, &domain.ValidationError{
				Path:       path + ".returns.type",
				Message:    fmt.Sprintf("invalid return type %q", fn.Returns.Type),
				Suggestion: "Supported: rows, row, scalar, void",
			})
		}

		// Validate param count matches $N placeholders
		if fn.Query != "" {
			errs = append(errs, validateFunctionParams(path, fn)...)
		}
	}
	return errs
}

func validateFunctionParams(path string, fn domain.Function) domain.ValidationErrors {
	var errs domain.ValidationErrors
	paramCount := len(fn.Params)

	// Count $N placeholders in query
	maxPlaceholder := 0
	for i := 1; i <= paramCount+5; i++ {
		placeholder := fmt.Sprintf("$%d", i)
		if strings.Contains(fn.Query, placeholder) {
			if i > maxPlaceholder {
				maxPlaceholder = i
			}
		}
	}

	if maxPlaceholder != paramCount {
		errs = append(errs, &domain.ValidationError{
			Path:    path,
			Message: fmt.Sprintf("query has %d placeholder(s) but %d param(s) defined", maxPlaceholder, paramCount),
		})
	}

	return errs
}

func validateSeeds(seeds map[string][]map[string]any, tables map[string]domain.Table) domain.ValidationErrors {
	var errs domain.ValidationErrors
	for name := range seeds {
		path := fmt.Sprintf("seeds.%s", name)
		// "users" is valid (auth table)
		if name == "users" {
			continue
		}
		if _, ok := tables[name]; !ok {
			errs = append(errs, &domain.ValidationError{
				Path:       path,
				Message:    fmt.Sprintf("seed references unknown table %q", name),
				Suggestion: fmt.Sprintf("Define a %q table or check spelling", name),
			})
		}
	}
	return errs
}

func validateForeignKeys(tables map[string]domain.Table) domain.ValidationErrors {
	var errs domain.ValidationErrors

	// Build set of known tables + their fields
	knownTables := map[string]map[string]domain.Field{
		"users": {"id": {Type: "bigserial", PrimaryKey: true}}, // auth table always exists
	}
	for name, table := range tables {
		knownTables[name] = table.Fields
	}

	for tableName, table := range tables {
		for fieldName, field := range table.Fields {
			if field.ForeignKey == nil || field.ForeignKey.References == "" {
				continue
			}
			fkPath := fmt.Sprintf("tables.%s.fields.%s.foreign_key", tableName, fieldName)
			parts := strings.SplitN(field.ForeignKey.References, ".", 2)
			if len(parts) != 2 {
				continue // already caught by structural validation
			}

			refTable, refCol := parts[0], parts[1]
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
