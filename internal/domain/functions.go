package domain

import "context"

// FunctionRequest is the invocation passed to a code function.
type FunctionRequest struct {
	Name    string
	Method  string
	Path    string
	Query   map[string][]string
	Headers map[string][]string
	Body    []byte
	Claims  map[string]any // nil when anonymous
	// CallerToken is the caller's bearer JWT (the raw token from the
	// Authorization header), or "" when the request is anonymous. The runtime
	// forwards it to the worker so ctx.supabase acts as the caller (RLS applies).
	CallerToken string
	// RequestID is a short unique identifier for this invocation, used to
	// correlate worker log lines with the originating HTTP request. When set
	// by the HTTP handler it matches the X-Request-Id header on the response.
	RequestID string
}

// FunctionResponse is the handler's HTTP response.
type FunctionResponse struct {
	Status  int
	Headers map[string][]string
	Body    []byte
}

// FunctionRuntime invokes code functions. Implemented by internal/adapter/funcs (later task).
type FunctionRuntime interface {
	Has(name string) bool
	// AuthRequired reports whether the named function requires an authenticated
	// caller (non-nil JWT claims). When true, the HTTP handler returns 401 for
	// anonymous requests before invoking the function.
	AuthRequired(name string) bool
	Invoke(ctx context.Context, req FunctionRequest) (*FunctionResponse, error)
	Close() error
}
