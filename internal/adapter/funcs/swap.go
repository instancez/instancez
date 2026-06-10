package funcs

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/saedx1/instancez/internal/domain"
)

var _ domain.FunctionRuntime = (*SwapRuntime)(nil)

// SwapRuntime is a domain.FunctionRuntime that delegates to a current *Runtime
// held behind an atomic pointer, so the active worker pool can be replaced
// (e.g. on a function-bundle version change) without tearing down the HTTP
// handler that holds the FunctionRuntime reference.
//
// The hot path (Has/Invoke) reads the pointer atomically with no lock. Swap
// installs a new runtime and returns the previous one so the caller can Close
// it after in-flight invocations drain. Close on the SwapRuntime closes the
// current runtime and prevents further swaps.
type SwapRuntime struct {
	cur atomic.Pointer[Runtime]

	mu     sync.Mutex
	closed bool
}

// NewSwapRuntime wraps rt (which may be nil; Has/Invoke then behave as an empty
// runtime until the first Swap).
func NewSwapRuntime(rt *Runtime) *SwapRuntime {
	s := &SwapRuntime{}
	if rt != nil {
		s.cur.Store(rt)
	}
	return s
}

// Current returns the currently-installed runtime, or nil.
func (s *SwapRuntime) Current() *Runtime { return s.cur.Load() }

// Swap installs next as the current runtime and returns the previous one (which
// the caller is responsible for Close()ing once it has drained). If the
// SwapRuntime is already closed, Swap closes next immediately and returns nil.
func (s *SwapRuntime) Swap(next *Runtime) (prev *Runtime) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		if next != nil {
			_ = next.Close()
		}
		return nil
	}
	prev = s.cur.Swap(next)
	s.mu.Unlock()
	return prev
}

func (s *SwapRuntime) Has(name string) bool {
	rt := s.cur.Load()
	if rt == nil {
		return false
	}
	return rt.Has(name)
}

// AuthRequired reports whether the named function requires an authenticated
// caller. Returns false when there is no current runtime (nil-safe).
func (s *SwapRuntime) AuthRequired(name string) bool {
	rt := s.cur.Load()
	if rt == nil {
		return false
	}
	return rt.AuthRequired(name)
}

func (s *SwapRuntime) Invoke(ctx context.Context, req domain.FunctionRequest) (*domain.FunctionResponse, error) {
	rt := s.cur.Load()
	if rt == nil {
		return nil, ErrWorkerFailed
	}
	return rt.Invoke(ctx, req)
}

// Close marks the SwapRuntime closed and closes the current runtime. Subsequent
// Swap calls are no-ops (they Close their argument). Idempotent.
func (s *SwapRuntime) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	rt := s.cur.Swap(nil)
	s.mu.Unlock()
	if rt != nil {
		return rt.Close()
	}
	return nil
}
