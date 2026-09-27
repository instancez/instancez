package funcs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/instancez/instancez/internal/domain"
)

func bareRuntime(inflight int) *Runtime {
	r := &Runtime{
		sem:    make(chan struct{}, 4),
		done:   make(chan struct{}),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for i := 0; i < inflight; i++ {
		r.sem <- struct{}{}
	}
	return r
}

func TestCloseWaitsForInFlight(t *testing.T) {
	r := bareRuntime(1)
	closed := make(chan struct{})
	go func() { _ = r.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned while a call was in flight")
	case <-time.After(150 * time.Millisecond):
	}
	<-r.sem
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after the call finished")
	}
}

func TestCloseDrainTimesOut(t *testing.T) {
	defer SetDrainTimeoutForTest(50 * time.Millisecond)()
	r := bareRuntime(4)
	start := time.Now()
	_ = r.Close()
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Close blocked %v past the drain timeout", d)
	}
}

func TestCloseIdleAndTwice(t *testing.T) {
	r := bareRuntime(0)
	start := time.Now()
	_ = r.Close()
	_ = r.Close()
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("idle Close took %v", d)
	}
}

func TestInvokeAfterCloseIsSaturated(t *testing.T) {
	r := bareRuntime(0)
	_ = r.Close()
	if _, err := r.Invoke(context.Background(), domain.FunctionRequest{Name: "x"}); !errors.Is(err, ErrSaturated) {
		t.Fatalf("got %v, want ErrSaturated", err)
	}
}

func TestDrainTimeoutCoversMaxTimeout(t *testing.T) {
	if drainTimeout <= maxTimeout {
		t.Fatalf("drainTimeout %v must exceed maxTimeout %v", drainTimeout, maxTimeout)
	}
}
