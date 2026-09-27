//go:build integration

package funcs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/instancez/instancez/internal/adapter/funcs"
	"github.com/instancez/instancez/internal/domain"
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
  if (req.headers["x-inz-crash"]) {
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
		Headers: map[string][]string{"X-Inz-Crash": {"1"}},
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
	// The handler crashes the whole process only when x-inz-crash header is
	// set; otherwise it returns 200. This isolates the crash to the first call.
	writeFn(t, dir, "boom.js", `
export default async (req) => {
  if (req.headers["x-inz-crash"]) {
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
		Headers: map[string][]string{"X-Inz-Crash": {"1"}},
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

const pidFn = `export default async () => ({ status: 200, body: { pid: process.pid } });`

// workerPID asks the pool's worker for its process id.
func workerPID(t *testing.T, rt *funcs.Runtime) int {
	t.Helper()
	resp, err := rt.Invoke(context.Background(), domain.FunctionRequest{Name: "pid", Method: "POST", Body: []byte("{}")})
	if err != nil || resp.Status != 200 {
		t.Fatalf("pid: resp=%v err=%v", resp, err)
	}
	var out struct{ PID int }
	if err := json.Unmarshal(resp.Body, &out); err != nil || out.PID == 0 {
		t.Fatalf("pid: bad body %q: %v", resp.Body, err)
	}
	return out.PID
}

// waitPID polls until the pool answers the pid function again, e.g. after a restart.
func waitPID(t *testing.T, rt *funcs.Runtime) int {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		resp, err := rt.Invoke(context.Background(), domain.FunctionRequest{Name: "pid", Method: "POST", Body: []byte("{}")})
		if err == nil && resp.Status == 200 {
			var out struct{ PID int }
			if json.Unmarshal(resp.Body, &out) == nil && out.PID != 0 {
				return out.PID
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pool never recovered; last err %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// childProcs returns the state letter of each direct child process, keyed by pid.
func childProcs(t *testing.T) map[int]string {
	t.Helper()
	stats, _ := filepath.Glob("/proc/[0-9]*/stat")
	me := os.Getpid()
	out := map[int]string{}
	for _, p := range stats {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		// Fields after the ")" that closes comm: state, ppid, ...
		f := strings.Fields(string(b[bytes.LastIndexByte(b, ')')+1:]))
		if len(f) < 2 {
			continue
		}
		ppid, _ := strconv.Atoi(f[1])
		if ppid != me {
			continue
		}
		pid, _ := strconv.Atoi(strings.Fields(string(b))[0])
		out[pid] = f[0]
	}
	return out
}

// A CPU-bound handler blocks Node's event loop; the pool must replace that worker, not keep routing to it.
func TestPoolReplacesWedgedWorker(t *testing.T) {
	dir := t.TempDir()
	writeFn(t, dir, "spin.js", `export default async () => { while (true) {} };`)
	writeFn(t, dir, "pid.js", pidFn)
	rt, err := funcs.New(funcs.Options{
		Dir:      dir,
		PoolSize: 1,
		Functions: map[string]domain.CodeFunction{
			"spin": {Runtime: "node", File: "spin.js", Timeout: "200ms"},
			"pid":  {Runtime: "node", File: "pid.js", Timeout: "1s"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	before := workerPID(t, rt)
	if _, err := rt.Invoke(context.Background(), domain.FunctionRequest{Name: "spin", Method: "POST"}); !errors.Is(err, funcs.ErrTimeout) {
		t.Fatalf("spin: got %v, want ErrTimeout", err)
	}
	after := waitPID(t, rt)
	if after == before {
		t.Fatalf("worker %d was not replaced", before)
	}

	// The killed worker must be reaped: no zombie children, only the live worker.
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		kids := childProcs(t)
		zombie := false
		for _, st := range kids {
			zombie = zombie || st == "Z"
		}
		_, liveOK := kids[after]
		if !zombie && liveOK && len(kids) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("children after replace: %v (want only %d, no zombies)", kids, after)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A slow handler that awaits leaves the event loop free, so its worker must survive the timeout.
func TestPoolKeepsSlowButHealthyWorker(t *testing.T) {
	dir := t.TempDir()
	writeFn(t, dir, "lag.js", `export default async () => { await new Promise(r => setTimeout(r, 3000)); return { status: 200 }; };`)
	writeFn(t, dir, "pid.js", pidFn)
	rt, err := funcs.New(funcs.Options{
		Dir:      dir,
		PoolSize: 1,
		Functions: map[string]domain.CodeFunction{
			"lag": {Runtime: "node", File: "lag.js", Timeout: "150ms"},
			"pid": {Runtime: "node", File: "pid.js", Timeout: "1s"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	before := workerPID(t, rt)
	if _, err := rt.Invoke(context.Background(), domain.FunctionRequest{Name: "lag", Method: "POST"}); !errors.Is(err, funcs.ErrTimeout) {
		t.Fatalf("lag: got %v, want ErrTimeout", err)
	}
	// Outlast the probe deadline so a wrong kill would have landed.
	time.Sleep(1500 * time.Millisecond)
	if after := workerPID(t, rt); after != before {
		t.Fatalf("healthy slow worker was replaced: pid %d -> %d", before, after)
	}
}

// Many requests timing out on one wedged worker must replace it exactly once.
func TestPoolConcurrentTimeoutsRestartOnce(t *testing.T) {
	var mu sync.Mutex
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&lockedWriter{&mu, &buf}, nil))

	dir := t.TempDir()
	writeFn(t, dir, "spin.js", `export default async () => { while (true) {} };`)
	writeFn(t, dir, "pid.js", pidFn)
	rt, err := funcs.New(funcs.Options{
		Dir:      dir,
		PoolSize: 1,
		Logger:   logger,
		Functions: map[string]domain.CodeFunction{
			"spin": {Runtime: "node", File: "spin.js", Timeout: "300ms"},
			"pid":  {Runtime: "node", File: "pid.js", Timeout: "1s"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	before := workerPID(t, rt)
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := rt.Invoke(context.Background(), domain.FunctionRequest{Name: "spin", Method: "POST"})
			if !errors.Is(err, funcs.ErrTimeout) {
				t.Errorf("spin: got %v, want ErrTimeout", err)
			}
		}()
	}
	wg.Wait()
	if after := waitPID(t, rt); after == before {
		t.Fatalf("worker %d was not replaced", before)
	}
	// Let any stray second probe finish before counting.
	time.Sleep(1500 * time.Millisecond)
	mu.Lock()
	n := strings.Count(buf.String(), "worker unresponsive")
	mu.Unlock()
	if n != 1 {
		t.Fatalf("got %d replacements, want 1; log:\n%s", n, buf.String())
	}
}

func TestPoolRejectsOversizeResponse(t *testing.T) {
	const limit = 6 << 20
	dir := t.TempDir()
	// String bodies are written as-is, so these hit the cap byte-exact.
	writeFn(t, dir, "at.js", fmt.Sprintf(`export default async () => ({ status: 200, body: "x".repeat(%d) });`, limit))
	writeFn(t, dir, "over.js", fmt.Sprintf(`export default async () => ({ status: 200, body: "x".repeat(%d) });`, limit+1))
	writeFn(t, dir, "huge.js", `export default async () => ({ status: 200, body: { s: "x".repeat(64 * 1024 * 1024) } });`)
	writeFn(t, dir, "pid.js", pidFn)
	rt, err := funcs.New(funcs.Options{
		Dir:      dir,
		PoolSize: 1,
		Functions: map[string]domain.CodeFunction{
			"at":   {Runtime: "node", File: "at.js"},
			"over": {Runtime: "node", File: "over.js"},
			"huge": {Runtime: "node", File: "huge.js"},
			"pid":  {Runtime: "node", File: "pid.js"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()

	before := workerPID(t, rt)
	resp, err := rt.Invoke(context.Background(), domain.FunctionRequest{Name: "at", Method: "POST"})
	if err != nil || resp.Status != 200 || len(resp.Body) != limit {
		t.Fatalf("at cap: err=%v len=%d", err, func() int {
			if resp == nil {
				return -1
			}
			return len(resp.Body)
		}())
	}
	for _, name := range []string{"over", "huge"} {
		_, err = rt.Invoke(context.Background(), domain.FunctionRequest{Name: name, Method: "POST"})
		if !errors.Is(err, funcs.ErrResponseTooLarge) || !errors.Is(err, funcs.ErrWorkerFailed) {
			t.Fatalf("%s: got %v, want ErrResponseTooLarge", name, err)
		}
	}
	// An oversize reply is not a crash: the same worker keeps serving.
	if after := workerPID(t, rt); after != before {
		t.Fatalf("worker replaced after oversize reply: pid %d -> %d", before, after)
	}
}
