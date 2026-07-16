package domain

import (
	"context"
	"testing"
)

func TestRequestIDContext(t *testing.T) {
	base := context.Background()

	if got := RequestIDFromContext(base); got != "" {
		t.Errorf("empty ctx: got %q, want empty", got)
	}

	ctx := ContextWithRequestID(base, "abc-123")
	if got := RequestIDFromContext(ctx); got != "abc-123" {
		t.Errorf("round-trip: got %q, want abc-123", got)
	}

	// Empty ID must not shadow an existing one — callers should be able
	// to call ContextWithRequestID("") unconditionally without erasing
	// a previously set value.
	ctx2 := ContextWithRequestID(ctx, "")
	if got := RequestIDFromContext(ctx2); got != "abc-123" {
		t.Errorf("empty overwrite: got %q, want abc-123", got)
	}

	// Children override parents.
	ctx3 := ContextWithRequestID(ctx, "xyz-9")
	if got := RequestIDFromContext(ctx3); got != "xyz-9" {
		t.Errorf("override: got %q, want xyz-9", got)
	}
}
