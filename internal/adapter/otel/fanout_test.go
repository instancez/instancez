package otel

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// Both children must see a record, and an attr added via WithAttrs must reach
// both — the case that silently vanishes if WithAttrs doesn't fan out.
func TestFanoutForwardsToAllChildrenWithAttrs(t *testing.T) {
	var a, b bytes.Buffer
	h := newFanout(
		slog.NewJSONHandler(&a, nil),
		slog.NewJSONHandler(&b, nil),
	)
	logger := slog.New(h).With("trace_id", "abc123")
	logger.Info("hello")

	for name, buf := range map[string]*bytes.Buffer{"a": &a, "b": &b} {
		out := buf.String()
		if !strings.Contains(out, "hello") {
			t.Fatalf("child %s missing message: %q", name, out)
		}
		if !strings.Contains(out, "abc123") {
			t.Fatalf("child %s missing WithAttrs attr: %q", name, out)
		}
	}
}

// A group must nest attributes on every child, not just the first. If WithGroup
// forgets a child, that side logs the attr unnested and the two streams disagree.
func TestFanoutWithGroupNestsOnAllChildren(t *testing.T) {
	var a, b bytes.Buffer
	logger := slog.New(newFanout(
		slog.NewJSONHandler(&a, nil),
		slog.NewJSONHandler(&b, nil),
	)).WithGroup("req").With("id", "42")
	logger.Info("hello")

	for name, buf := range map[string]*bytes.Buffer{"a": &a, "b": &b} {
		out := buf.String()
		if !strings.Contains(out, `"req":{"id":"42"}`) {
			t.Fatalf("child %s missing grouped attr: %q", name, out)
		}
	}
}

func TestFanoutEnabledIsOr(t *testing.T) {
	// A handler at LevelError is disabled for Info; one at LevelDebug is enabled.
	off := slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError})
	on := slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelDebug})
	h := newFanout(off, on)
	if !h.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("Enabled should be true when any child is enabled")
	}
	if newFanout(off).Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("Enabled should be false when no child is enabled")
	}
}
