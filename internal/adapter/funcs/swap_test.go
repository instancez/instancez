package funcs

import (
	"context"
	"errors"
	"testing"

	"github.com/saedx1/instancez/internal/domain"
)

func TestSwapRuntimeNilBehavesEmpty(t *testing.T) {
	s := NewSwapRuntime(nil)
	if s.Has("anything") {
		t.Fatalf("nil SwapRuntime should report Has=false")
	}
	if _, err := s.Invoke(context.Background(), domain.FunctionRequest{Name: "x"}); !errors.Is(err, ErrWorkerFailed) {
		t.Fatalf("Invoke on empty SwapRuntime err = %v, want ErrWorkerFailed", err)
	}
	if s.Current() != nil {
		t.Fatalf("Current() should be nil")
	}
	// Close on an empty runtime is a no-op and idempotent.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("double Close: %v", err)
	}
}

func TestSwapRuntimeSwapReturnsPrevAndAfterCloseClosesArg(t *testing.T) {
	// We only exercise the pointer/lifecycle logic, not a live node pool: a
	// zero-value *Runtime is never spawned, and we never call Invoke on it.
	a := &Runtime{fnNames: map[string]bool{"a": true}, done: make(chan struct{})}
	b := &Runtime{fnNames: map[string]bool{"b": true}, done: make(chan struct{})}

	s := NewSwapRuntime(a)
	if !s.Has("a") || s.Has("b") {
		t.Fatalf("expected current=a")
	}

	prev := s.Swap(b)
	if prev != a {
		t.Fatalf("Swap returned %p, want a (%p)", prev, a)
	}
	if !s.Has("b") || s.Has("a") {
		t.Fatalf("expected current=b after swap")
	}
	if s.Current() != b {
		t.Fatalf("Current() != b after swap")
	}

	// After Close, the current runtime (b) is closed and further swaps are
	// no-ops that close their argument.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if s.Current() != nil {
		t.Fatalf("Current() should be nil after Close")
	}

	c := &Runtime{fnNames: map[string]bool{"c": true}, done: make(chan struct{})}
	if got := s.Swap(c); got != nil {
		t.Fatalf("Swap after Close returned %p, want nil", got)
	}
	// c must have been closed by the no-op swap; closing a Runtime with no
	// workers is safe and idempotent.
	if !c.closed {
		t.Fatalf("Swap after Close should have closed its argument")
	}
}
