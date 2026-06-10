//go:build integration

package funcs_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/saedx1/instancez/internal/adapter/funcs"
	"github.com/saedx1/instancez/internal/domain"
)

// writeFn writes a function source file into dir and returns nothing; it fails
// the test on error.
func writeFn(t *testing.T, dir, name, src string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPoolConcurrency proves the pool serves many invocations concurrently
// rather than serializing them. A handler that sleeps ~50ms is invoked 20 times
// concurrently; if invocations were serialized the wall clock would be ~1s, so
// a generous <700ms ceiling demonstrates concurrency. Node's event loop
// multiplexes the timers within a single worker, so even PoolSize 1 passes —
// here we leave the pool at its default.
func TestPoolConcurrency(t *testing.T) {
	dir := t.TempDir()
	writeFn(t, dir, "slow.js",
		`export default async () => { await new Promise(r => setTimeout(r, 50)); return { status: 200, body: { ok: true } }; };`)

	rt, err := funcs.New(funcs.Options{
		Dir:       dir,
		Functions: map[string]domain.CodeFunction{"slow": {Runtime: "node", File: "slow.js"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Warm one invocation so first-call JIT/import costs don't skew the timing.
	if _, err := rt.Invoke(context.Background(), domain.FunctionRequest{Name: "slow", Method: "GET"}); err != nil {
		t.Fatalf("warmup invoke: %v", err)
	}

	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	codes := make([]int, n)
	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := rt.Invoke(context.Background(), domain.FunctionRequest{Name: "slow", Method: "GET"})
			errs[i] = err
			if err == nil {
				codes[i] = resp.Status
			}
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("invoke %d failed: %v", i, errs[i])
		}
		if codes[i] != 200 {
			t.Fatalf("invoke %d status %d, want 200", i, codes[i])
		}
	}
	if elapsed > 700*time.Millisecond {
		t.Fatalf("20 concurrent ~50ms invokes took %v (>700ms) — looks serialized", elapsed)
	}
}

// TestPoolDistinctWorkers proves that round-robin dispatch actually fans out
// across multiple Node PROCESSES, not just multiplexing on one worker's event
// loop. The function returns its own process.pid; firing many concurrent
// invokes against a PoolSize-3 runtime must observe more than one distinct pid.
// We assert >1 (not exactly 3) to stay non-flaky: scheduling could land a burst
// on a subset of workers, but it cannot collapse onto a single process.
func TestPoolDistinctWorkers(t *testing.T) {
	dir := t.TempDir()
	writeFn(t, dir, "pid.js",
		`export default async () => { return { status: 200, body: { pid: process.pid } }; };`)

	rt, err := funcs.New(funcs.Options{
		Dir:         dir,
		PoolSize:    3,
		MaxInFlight: 64,
		Functions:   map[string]domain.CodeFunction{"pid": {Runtime: "node", File: "pid.js"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	const n = 30
	var wg sync.WaitGroup
	var mu sync.Mutex
	pids := map[int]int{}
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := rt.Invoke(context.Background(), domain.FunctionRequest{Name: "pid", Method: "GET"})
			if err != nil {
				errs[i] = err
				return
			}
			var out struct {
				Pid int `json:"pid"`
			}
			if jerr := json.Unmarshal(resp.Body, &out); jerr != nil {
				errs[i] = jerr
				return
			}
			mu.Lock()
			pids[out.Pid]++
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			t.Fatalf("invoke %d failed: %v", i, e)
		}
	}
	if len(pids) <= 1 {
		t.Fatalf("expected >1 distinct worker pid across %d invokes, got %d: %v", n, len(pids), pids)
	}
	t.Logf("observed %d distinct worker pids across %d invokes: %v", len(pids), n, pids)
}

// TestPoolCloseDuringRestart proves the interruptible backoff keeps Close prompt
// even when a worker restart is in flight: it crashes the single worker (which
// triggers an async restart goroutine) and then immediately calls Close,
// asserting Close returns within a bound. Without the done-channel wakeup a
// pending backoff could stall Close.
func TestPoolCloseDuringRestart(t *testing.T) {
	dir := t.TempDir()
	writeFn(t, dir, "boom.js", `
export default async (req) => {
  if (req.headers["x-ultra-crash"]) {
    setTimeout(() => process.exit(1), 0);
    await new Promise(r => setTimeout(r, 1000));
    return { status: 200, body: { unreachable: true } };
  }
  return { status: 200, body: { ok: true } };
};`)

	rt, err := funcs.New(funcs.Options{
		Dir:       dir,
		PoolSize:  1,
		Functions: map[string]domain.CodeFunction{"boom": {Runtime: "node", File: "boom.js"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Trigger the crash → async restart goroutine is now in flight.
	_, err = rt.Invoke(context.Background(), domain.FunctionRequest{
		Name:    "boom",
		Method:  "GET",
		Headers: map[string][]string{"X-Ultra-Crash": {"1"}},
	})
	if !errors.Is(err, funcs.ErrWorkerFailed) {
		t.Fatalf("crash invoke: got err %v, want ErrWorkerFailed", err)
	}

	// Close while the restart is plausibly mid-backoff or mid-spawn. The bound
	// is generous because a spawn already in waitHealthy (not interruptible) can
	// take up to ~5s before the loop next observes done; the done-channel still
	// prevents an unbounded hang behind the backoff itself.
	done := make(chan error, 1)
	go func() { done <- rt.Close() }()
	select {
	case cerr := <-done:
		if cerr != nil {
			t.Fatalf("Close returned error: %v", cerr)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("Close did not return within 8s during in-flight restart — backoff likely not interruptible")
	}
}

// TestPoolTimeout verifies that a per-request timeout returns ErrTimeout AND
// leaves the worker healthy (its late response is discarded, the worker is not
// crashed/restarted). PoolSize 1 makes the survival check meaningful: the
// follow-up invoke must land on the SAME worker and succeed.
func TestPoolTimeout(t *testing.T) {
	dir := t.TempDir()
	// Handler sleeps ~2s and deliberately IGNORES ctx.signal — this exercises
	// the realistic path where the Go-side deadline destroys the socket while
	// the handler is still running and later tries to write its response.
	writeFn(t, dir, "lag.js",
		`export default async () => { await new Promise(r => setTimeout(r, 2000)); return { status: 200, body: { done: true } }; };`)

	rt, err := funcs.New(funcs.Options{
		Dir:       dir,
		PoolSize:  1,
		Functions: map[string]domain.CodeFunction{"lag": {Runtime: "node", File: "lag.js", Timeout: "150ms"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	_, err = rt.Invoke(context.Background(), domain.FunctionRequest{Name: "lag", Method: "GET"})
	if !errors.Is(err, funcs.ErrTimeout) {
		t.Fatalf("got err %v, want ErrTimeout", err)
	}

	// Wait out the first handler's 2s tail so its (now destroyed) socket write
	// has already fired-and-been-discarded. If that late write crashed the
	// worker, the next invoke would return ErrWorkerFailed instead of ErrTimeout.
	time.Sleep(2500 * time.Millisecond)

	// Second invoke on the SAME (PoolSize 1) worker. The handler still sleeps 2s
	// so this also times out — but a SURVIVING worker returns ErrTimeout, whereas
	// a crashed/restarted-but-dead worker would surface ErrWorkerFailed (pickWorker
	// → nil during the restart window). ErrTimeout here proves the worker lived.
	_, err2 := rt.Invoke(context.Background(), domain.FunctionRequest{Name: "lag", Method: "GET"})
	if errors.Is(err2, funcs.ErrWorkerFailed) {
		t.Fatalf("worker did not survive timeout: second invoke returned ErrWorkerFailed: %v", err2)
	}
	if !errors.Is(err2, funcs.ErrTimeout) {
		t.Fatalf("second invoke: got err %v, want ErrTimeout (worker alive but slow)", err2)
	}
}

// TestPoolCrashRecovery verifies that a worker crash (process.exit) is isolated
// (returns ErrWorkerFailed) and that the pool recovers via async restart so a
// later invoke succeeds. PoolSize 1 is essential: with more workers the recovery
// invoke would round-robin to a different healthy worker and never exercise the
// restart path.
func TestPoolCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	// The handler crashes the whole process only when x-ultra-crash header is
	// set; otherwise it returns 200. This isolates the crash to the first call.
	writeFn(t, dir, "boom.js", `
export default async (req) => {
  if (req.headers["x-ultra-crash"]) {
    // Kill the worker process to simulate a hard crash.
    setTimeout(() => process.exit(1), 0);
    // Give the timer a tick so the connection is severed by process death.
    await new Promise(r => setTimeout(r, 1000));
    return { status: 200, body: { unreachable: true } };
  }
  return { status: 200, body: { ok: true } };
};`)

	rt, err := funcs.New(funcs.Options{
		Dir:       dir,
		PoolSize:  1,
		Functions: map[string]domain.CodeFunction{"boom": {Runtime: "node", File: "boom.js"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Trigger the crash.
	_, err = rt.Invoke(context.Background(), domain.FunctionRequest{
		Name:    "boom",
		Method:  "GET",
		Headers: map[string][]string{"X-Ultra-Crash": {"1"}},
	})
	if !errors.Is(err, funcs.ErrWorkerFailed) {
		t.Fatalf("crash invoke: got err %v, want ErrWorkerFailed", err)
	}

	// Poll until the async restart lands and a normal invoke succeeds.
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, ierr := rt.Invoke(context.Background(), domain.FunctionRequest{Name: "boom", Method: "GET"})
		if ierr == nil && resp.Status == 200 {
			return // recovered
		}
		lastErr = ierr
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("pool did not recover within 5s; last invoke err: %v", lastErr)
}

// TestPoolSaturation verifies the bounded in-flight gate: with MaxInFlight 1,
// a single slow invocation occupies the only slot, so a concurrent invoke is
// rejected with ErrSaturated rather than blocking. Synchronization uses a
// readiness signal (the slow handler pings a loopback server on entry) so the
// second call definitely fires while the first holds the slot — no bare-sleep
// race.
func TestPoolSaturation(t *testing.T) {
	ready := make(chan struct{}, 1)
	sig := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case ready <- struct{}{}:
		default:
		}
		w.WriteHeader(200)
	}))
	defer sig.Close()

	dir := t.TempDir()
	// On entry the handler signals readiness to the loopback server, then sleeps
	// ~1s while holding the single in-flight slot.
	writeFn(t, dir, "hold.js", `
export default async (req) => {
  try { await fetch(req.headers["x-sig-url"]); } catch (e) {}
  await new Promise(r => setTimeout(r, 1000));
  return { status: 200, body: { ok: true } };
};`)

	rt, err := funcs.New(funcs.Options{
		Dir:         dir,
		PoolSize:    1,
		MaxInFlight: 1,
		Functions:   map[string]domain.CodeFunction{"hold": {Runtime: "node", File: "hold.js", Timeout: "5s"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Fire the slow invoke that grabs the only slot.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = rt.Invoke(context.Background(), domain.FunctionRequest{
			Name:    "hold",
			Method:  "GET",
			Headers: map[string][]string{"X-Sig-Url": {sig.URL}},
		})
	}()

	// Wait until the slow handler is actually running (slot held).
	select {
	case <-ready:
	case <-time.After(4 * time.Second):
		t.Fatal("slow handler never signaled readiness")
	}
	// Tiny grace so the slow Invoke goroutine has the sem slot acquired. The slot
	// is acquired at the very top of Invoke, BEFORE the worker runs the handler,
	// so by the time the handler signals readiness the slot is already held.

	// A concurrent invoke must be rejected immediately with ErrSaturated.
	_, err2 := rt.Invoke(context.Background(), domain.FunctionRequest{Name: "hold", Method: "GET", Headers: map[string][]string{"X-Sig-Url": {sig.URL}}})
	if !errors.Is(err2, funcs.ErrSaturated) {
		t.Fatalf("concurrent invoke under MaxInFlight=1: got err %v, want ErrSaturated", err2)
	}

	wg.Wait()
}
