package domain

import "fmt"

// ValidationError represents a single schema validation error with location info.
type ValidationError struct {
	Path       string // e.g. "tables.todos.fields.category_id"
	Message    string
	Line       int    // YAML line number (0 if unknown)
	Suggestion string // optional fix suggestion
}

func (e *ValidationError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s (line %d): %s", e.Path, e.Line, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
}

// ValidationErrors collects multiple validation errors.
type ValidationErrors []*ValidationError

func (ve ValidationErrors) Error() string {
	return fmt.Sprintf("found %d validation error(s)", len(ve))
}

// ConfigError is returned when the config file cannot be loaded.
type ConfigError struct {
	Path    string
	Message string
	Err     error
}

func (e *ConfigError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("config %s: %s: %v", e.Path, e.Message, e.Err)
	}
	return fmt.Sprintf("config %s: %s", e.Path, e.Message)
}

func (e *ConfigError) Unwrap() error { return e.Err }

// MissingEnvError is returned when required env vars are not set.
type MissingEnvError struct {
	Vars []string
}

func (e *MissingEnvError) Error() string {
	return fmt.Sprintf("missing required environment variables: %v", e.Vars)
}

// DatabaseError wraps database connection/query errors.
type DatabaseError struct {
	Op  string // operation: "connect", "migrate", "query", etc.
	Err error
}

func (e *DatabaseError) Error() string {
	return fmt.Sprintf("database %s: %v", e.Op, e.Err)
}

func (e *DatabaseError) Unwrap() error { return e.Err }
