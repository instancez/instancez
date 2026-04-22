package domain

import "context"

// requestIDKey is the context key for the per-request ID. Keeping it in the
// domain package lets the http middleware that produces the ID and the
// postgres adapter that publishes it to SQL share a key without either
// depending on the other.
type requestIDKeyType int

const requestIDKey requestIDKeyType = 0

// ContextWithRequestID returns a copy of ctx carrying the given request ID.
// Pass "" to leave ctx unchanged — this is a convenience for callers that
// don't want to branch on empty IDs.
func ContextWithRequestID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFromContext returns the request ID stashed in ctx, or "" if none.
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}
