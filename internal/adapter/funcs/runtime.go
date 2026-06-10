package funcs

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/instancez/instancez/internal/domain"
)

// Typed errors the HTTP handler maps to status codes:
//   - ErrTimeout      → 504 Gateway Timeout (per-request deadline exceeded)
//   - ErrSaturated    → 503 Service Unavailable (in-flight cap reached)
//   - ErrWorkerFailed → 502 Bad Gateway (worker process died / no healthy worker)
var (
	// ErrTimeout is returned when a function invocation exceeds its configured
	// per-request timeout. The worker process is left running (it's healthy; the
	// handler was just slow), and its late response is discarded.
	ErrTimeout = errors.New("funcs: function invocation timed out")
	// ErrSaturated is returned when the bounded in-flight gate is full and a new
	// invocation cannot acquire a slot without blocking.
	ErrSaturated = errors.New("funcs: runtime saturated")
	// ErrWorkerFailed is returned when the worker process died (transport/connect
	// error) or when no healthy worker is available to serve the request.
	ErrWorkerFailed = errors.New("funcs: worker failed")
)

// defaultTimeout is used when a function's Timeout config is empty or fails to
// parse. Config validation rejects unparseable values, but we parse defensively.
const defaultTimeout = 30 * time.Second

// instancezEnvRefPattern matches ${INSTANCEZ_ENV_FOO} (and only that prefix/pattern).
// The submatch at index 1 is the full key name (e.g. "INSTANCEZ_ENV_FOO").
var instancezEnvRefPattern = regexp.MustCompile(`^\$\{(INSTANCEZ_ENV_[A-Za-z0-9_]+)\}$`)

// asInstancezEnvRef returns (refName, true) when v is an INSTANCEZ_ENV_ reference
// expression like "${INSTANCEZ_ENV_FOO}", or ("", false) for a plain literal.
func asInstancezEnvRef(v string) (string, bool) {
	m := instancezEnvRefPattern.FindStringSubmatch(v)
	if m == nil {
		return "", false
	}
	return m[1], true
}

//go:embed worker.js
var workerJS []byte

var _ domain.FunctionRuntime = (*Runtime)(nil)

type Options struct {
	Dir       string
	Functions map[string]domain.CodeFunction

	// LoopbackURL is the base URL of instancez's own HTTP API, reachable over
	// loopback from the worker. Injected into the data-access clients the shim
	// builds (ctx.supabase / ctx.serviceClient).
	LoopbackURL string
	// MintAnon lazily mints the anon apikey (sent by the data-access clients as
	// the `apikey` header, and as the Bearer token for anonymous calls) on the
	// first invoke, then caches it. Minting is deferred so it runs AFTER
	// migrations create auth.jwt_keys (the runtime is constructed before
	// migrations run). When nil (e.g. functions that never touch ctx.supabase),
	// no anon key is sent.
	MintAnon func(ctx context.Context) (string, error)
	// MintService mints a short-lived service_role JWT for ctx.serviceClient.
	// When nil, no service token is forwarded (serviceClient falls back to the
	// anon key, i.e. no escalation).
	MintService func(ctx context.Context) (string, error)

	// EnvMap is the in-memory INSTANCEZ_ENV_ namespace (built by config.LoadInstancezEnv).
	// Each function's env: values are resolved against this map at invoke time.
	// Keys must carry the "INSTANCEZ_ENV_" prefix. This map is NEVER written to
	// process env — it is passed to the worker via the X-Inz-Context header.
	EnvMap map[string]string

	// Logger receives log lines emitted by function handlers (both ctx.log.*
	// and patched console.*). When nil, slog.Default() is used.
	Logger *slog.Logger

	// PoolSize is the number of Node worker processes to spawn. When <= 0 a
	// default of min(4, GOMAXPROCS) is used. Each worker is a separate node
	// process; requests are dispatched across the pool round-robin and many
	// concurrent requests can share a single worker (Node's event loop is
	// inherently concurrent for I/O-bound handlers).
	PoolSize int

	// MaxInFlight bounds the number of concurrent invocations across the whole
	// pool. When <= 0 a default of PoolSize*64 is used (handlers are I/O-bound,
	// so a worker can multiplex many concurrent requests). Exceeding this cap
	// returns ErrSaturated (→503) rather than blocking.
	MaxInFlight int
}

// worker holds the per-process state for a single Node worker.
type worker struct {
	cmd    *exec.Cmd
	sock   string
	client *http.Client
	// healthy is true while the worker process is believed alive. A transport
	// error on client.Do flips it to false (via CompareAndSwap, so exactly one
	// restart is triggered per crash even under concurrent in-flight requests).
	healthy atomic.Bool
}

type Runtime struct {
	opts    Options
	shim    string
	fnNames map[string]bool
	envmap  map[string]string
	logger  *slog.Logger

	// slots holds the pool. Length is fixed at New time; entries are swapped
	// atomically on restart. The hot path reads via Load() with no lock.
	slots []atomic.Pointer[worker]
	// rr is the round-robin counter (atomic).
	rr uint64

	// sem is the bounded in-flight gate (buffered channel of size MaxInFlight).
	sem chan struct{}

	// scanWG is incremented for each scanner goroutine (2 per spawn) so Close
	// can wait for them to drain before returning (prevents the race detector
	// from flagging writes to the logger after teardown).
	scanWG sync.WaitGroup
	// restartWG tracks in-flight restart goroutines so Close can wait for them.
	restartWG sync.WaitGroup

	// mu guards closed and serializes the restart Store-vs-Close TOCTOU. The
	// hot path does NOT take mu (it uses atomics); only restart and Close do.
	mu     sync.Mutex
	closed bool
	// done is closed exactly once by Close (under mu, in the same critical
	// section that sets closed=true). The restart retry loop selects on it so
	// its interruptible backoff wakes immediately when Close runs, guaranteeing
	// restartWG.Wait() in Close cannot deadlock behind a pending backoff.
	done chan struct{}

	// anonMu/anonTok/anonDone lazily cache the anon apikey minted via
	// opts.MintAnon on first invoke (deferred past migrations).
	anonMu   sync.Mutex
	anonTok  string
	anonDone bool
}

// anonKey returns the anon apikey, lazily minting + caching it via opts.MintAnon
// on first use (so the mint runs after migrations create auth.jwt_keys). Returns
// "" when MintAnon is nil (functions that never use the data-access clients).
func (r *Runtime) anonKey(ctx context.Context) (string, error) {
	if r.opts.MintAnon == nil {
		return "", nil
	}
	r.anonMu.Lock()
	defer r.anonMu.Unlock()
	if r.anonDone {
		return r.anonTok, nil
	}
	tok, err := r.opts.MintAnon(ctx)
	if err != nil {
		return "", err
	}
	r.anonTok, r.anonDone = tok, true
	return tok, nil
}

// randHex returns n random hex-encoded bytes (2n chars).
func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// fallback: should never happen
		panic("funcs: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// workerLogLine is the NDJSON structure emitted by worker.js for each log line.
type workerLogLine struct {
	Ts        int64          `json:"ts"`
	Level     string         `json:"level"`
	RequestID string         `json:"requestId"`
	Fn        string         `json:"fn"`
	Msg       string         `json:"msg"`
	Fields    map[string]any `json:"fields"`
}

// maxLogLineBytes is the maximum number of bytes consumed from a single log
// line before truncation. Lines longer than this are almost certainly runaway
// output (e.g. a function logging a huge object). We read up to this limit and
// discard the rest to avoid unbounded memory growth.
const maxLogLineBytes = 64 * 1024

// readLineCapped reads one newline-terminated line from rd, consuming at most
// maxLogLineBytes bytes. If the line exceeds that limit, the remainder up to
// the next newline is discarded without buffering (per-call allocation is
// bounded to the bufio.Reader buffer size). Returns the line (without trailing
// newline) and any read error (io.EOF is normal and means the pipe closed).
func readLineCapped(rd *bufio.Reader) (string, error) {
	var buf []byte
	for {
		seg, err := rd.ReadSlice('\n')
		if len(buf)+len(seg) <= maxLogLineBytes {
			buf = append(buf, seg...)
		} else if len(buf) < maxLogLineBytes {
			buf = append(buf, seg[:maxLogLineBytes-len(buf)]...)
		}
		if err == nil {
			break // got '\n'
		}
		if err == bufio.ErrBufferFull {
			continue // line continues beyond buffer; keep draining
		}
		return strings.TrimRight(string(buf), "\n\r"), err // EOF or real error
	}
	return strings.TrimRight(string(buf), "\n\r"), nil
}

// fieldsToAttrs converts a map[string]any to an alternating key/value slice
// suitable for use with slog.Group. Entries are appended in an unspecified order.
func fieldsToAttrs(m map[string]any) []any {
	out := make([]any, 0, len(m)*2)
	for k, v := range m {
		out = append(out, k, v)
	}
	return out
}

// scanWorkerStdout reads NDJSON log lines from the worker's stdout pipe and
// forwards them to logger at the appropriate slog level. Non-JSON lines
// (e.g., unexpected output) are forwarded at Warn with worker=true.
// Lines longer than maxLogLineBytes are truncated; scanning continues.
// The goroutine exits when the pipe closes (on process exit or Close()).
func scanWorkerStdout(r io.Reader, logger *slog.Logger, wg *sync.WaitGroup) {
	defer wg.Done()
	rd := bufio.NewReaderSize(r, maxLogLineBytes)
	for {
		raw, err := readLineCapped(rd)
		if raw != "" {
			var entry workerLogLine
			if jsonErr := json.Unmarshal([]byte(raw), &entry); jsonErr != nil {
				// Non-NDJSON line: forward as a warning attributed to the worker.
				logger.Warn(raw, "worker", true)
			} else {
				// Trusted top-level attrs; user-supplied fields nested under
				// "fields" group so they cannot clobber requestId/fn.
				attrs := []any{
					"requestId", entry.RequestID,
					"fn", entry.Fn,
				}
				if len(entry.Fields) > 0 {
					attrs = append(attrs, slog.Group("fields", fieldsToAttrs(entry.Fields)...))
				}
				switch strings.ToLower(entry.Level) {
				case "debug":
					logger.Debug(entry.Msg, attrs...)
				case "warn", "warning":
					logger.Warn(entry.Msg, attrs...)
				case "error":
					logger.Error(entry.Msg, attrs...)
				default: // "info", "log", unknown
					logger.Info(entry.Msg, attrs...)
				}
			}
		}
		if err != nil {
			// io.EOF or pipe closed: worker exited, scanner is done.
			return
		}
	}
}

// scanWorkerStderr reads lines from the worker's stderr pipe and forwards
// each at Warn level with worker_stderr=true. This captures crash output
// and raw Node.js error messages without attributing them to any request.
// Lines longer than maxLogLineBytes are truncated; scanning continues.
func scanWorkerStderr(r io.Reader, logger *slog.Logger, wg *sync.WaitGroup) {
	defer wg.Done()
	rd := bufio.NewReaderSize(r, maxLogLineBytes)
	for {
		raw, err := readLineCapped(rd)
		if raw != "" {
			logger.Warn(raw, "worker_stderr", true)
		}
		if err != nil {
			return
		}
	}
}

func New(opts Options) (*Runtime, error) {
	if opts.EnvMap == nil {
		opts.EnvMap = map[string]string{}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.PoolSize <= 0 {
		opts.PoolSize = min(4, runtime.GOMAXPROCS(0))
		if opts.PoolSize < 1 {
			opts.PoolSize = 1
		}
	}
	if opts.MaxInFlight <= 0 {
		opts.MaxInFlight = opts.PoolSize * 64
	}

	// Fail-early validation: check that every ${INSTANCEZ_ENV_*} ref in every
	// function's env: map is present in opts.EnvMap. This must run BEFORE any
	// resource is allocated (shim file write, node spawn) so a missing secret
	// fails cleanly with no leaked goroutines or files.
	for name, fn := range opts.Functions {
		for _, v := range fn.Env {
			if ref, ok := asInstancezEnvRef(v); ok {
				if _, found := opts.EnvMap[ref]; !found {
					return nil, fmt.Errorf("funcs: function %q: ${%s} not in INSTANCEZ_ENV_ namespace", name, ref)
				}
			}
		}
	}

	// Write the shim into <opts.Dir>/functions/ — the directory that contains
	// node_modules (npm ci runs in <config-root>/functions per funcsetup.go;
	// the deploy bundle packs functions/ as a subtree). ESM resolves bare
	// specifiers (e.g. "@supabase/supabase-js") by walking node_modules UPWARD
	// from the *importing* module's location; the shim must sit inside
	// functions/ so it can find functions/node_modules. A shim placed at
	// opts.Dir (one level above) never descends into functions/node_modules
	// and throws ERR_MODULE_NOT_FOUND, causing ctx.supabase / ctx.serviceClient
	// to fail on every invocation.
	// The leading dot makes the filename match bundle.go's skip-check
	// (`.inz-worker-*`) so a shim left in the functions/ tree is never
	// packed into a deploy bundle.
	// Must be .mjs so Node treats it as ESM (top-level await + import).
	// The shim is SHARED across all workers (same embedded content); it is
	// written once here and removed once in Close.
	shimName := ".inz-worker-" + randHex(8) + ".mjs"
	var shimDir string
	if opts.Dir == "" {
		shimDir = os.TempDir()
	} else {
		shimDir = filepath.Join(opts.Dir, "functions")
		if err := os.MkdirAll(shimDir, 0o755); err != nil {
			return nil, fmt.Errorf("funcs: mkdir functions dir for shim: %w", err)
		}
	}
	shim := filepath.Join(shimDir, shimName)
	if err := os.WriteFile(shim, workerJS, 0o644); err != nil {
		return nil, fmt.Errorf("funcs: write shim: %w", err)
	}

	var spec []string
	names := map[string]bool{}
	for name, fn := range opts.Functions {
		spec = append(spec, name+"="+filepath.Join(opts.Dir, fn.File))
		names[name] = true
	}
	fnSpec := strings.Join(spec, ",")

	rt := &Runtime{
		opts:    opts,
		shim:    shim,
		fnNames: names,
		envmap:  opts.EnvMap,
		logger:  opts.Logger,
		slots:   make([]atomic.Pointer[worker], opts.PoolSize),
		sem:     make(chan struct{}, opts.MaxInFlight),
		done:    make(chan struct{}),
	}

	// Spawn the pool. On any failure, tear down everything already spawned.
	for i := 0; i < opts.PoolSize; i++ {
		w, err := rt.spawnWorker(fnSpec)
		if err != nil {
			rt.Close()
			return nil, err
		}
		rt.slots[i].Store(w)
	}
	return rt, nil
}

// spawnWorker writes/uses the shared shim, builds the node command with the
// SCRUBBED env, wires stdout/stderr → slog log capture (incrementing scanWG),
// starts the process, and health-checks it. This is the SINGLE place where the
// env scrub and log capture are applied, so it MUST be used for both initial
// pool creation AND restarts — that keeps the env-scrub regression guard and
// log capture in force on every (re)spawn.
func (r *Runtime) spawnWorker(fnSpec string) (*worker, error) {
	// Generate a unique socket path using random bytes. Do NOT create the file —
	// node's server.listen() fails with EADDRINUSE if a regular file already exists.
	sock := filepath.Join(os.TempDir(), "inz-fn-"+randHex(8)+".sock")

	cmd := exec.Command("node", r.shim, sock, fnSpec)
	// Pipes must be obtained BEFORE cmd.Start().
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("funcs: stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("funcs: stderr pipe: %w", err)
	}

	// Spawn with an explicitly constructed, minimal environment so the worker
	// cannot read parent secrets (AWS_*, INSTANCEZ_*, IRSA token files, etc.).
	// PATH is needed to find node itself; NODE_ENV and HOME are harmless
	// defaults. Node module resolution is filesystem-based (walks node_modules
	// upward from the importing file) and does NOT depend on env vars, so
	// @supabase/supabase-js and other vendored deps keep working.
	// Per-function env vars are added per-invoke via X-Inz-Context, not here.
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"NODE_ENV=production",
		"HOME=" + os.TempDir(),
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("funcs: start worker: %w", err)
	}

	// Launch scanner goroutines AFTER cmd.Start() so the pipes are live.
	r.scanWG.Add(2)
	go scanWorkerStdout(stdoutPipe, r.logger, &r.scanWG)
	go scanWorkerStderr(stderrPipe, r.logger, &r.scanWG)

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}

	w := &worker{cmd: cmd, sock: sock, client: client}
	w.healthy.Store(true)

	if err := waitHealthy(client, 5*time.Second); err != nil {
		// Tear down this half-spawned worker: kill the process (which closes the
		// pipe write ends so the scanners drain and exit), then reap it.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.Remove(sock)
		return nil, err
	}
	return w, nil
}

// waitHealthy polls the worker's /healthz over its unix-socket client until it
// responds or the deadline elapses.
func waitHealthy(client *http.Client, d time.Duration) error {
	deadline := time.Now().Add(d)
	var lastErr error
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest("GET", "http://unix/healthz", nil)
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("funcs: worker did not become healthy: %w", lastErr)
}

func (r *Runtime) Has(name string) bool { return r.fnNames[name] }

// AuthRequired reports whether the named function requires an authenticated
// caller. Returns false for unknown functions (safe: Has returns false for them
// and the handler 404s before reaching this check).
func (r *Runtime) AuthRequired(name string) bool {
	return r.opts.Functions[name].AuthRequired
}

// pickWorker returns the next healthy worker round-robin, or nil when none is
// healthy. It probes up to len(slots) entries starting from the round-robin
// cursor so a few unhealthy workers don't starve dispatch.
func (r *Runtime) pickWorker() *worker {
	n := len(r.slots)
	if n == 0 {
		return nil
	}
	start := atomic.AddUint64(&r.rr, 1)
	for i := 0; i < n; i++ {
		w := r.slots[(start+uint64(i))%uint64(n)].Load()
		if w != nil && w.healthy.Load() {
			return w
		}
	}
	return nil
}

// timeoutFor returns the configured per-request timeout for a function,
// defaulting to defaultTimeout when empty or unparseable.
func (r *Runtime) timeoutFor(name string) time.Duration {
	fn, ok := r.opts.Functions[name]
	if !ok || fn.Timeout == "" {
		return defaultTimeout
	}
	d, err := time.ParseDuration(fn.Timeout)
	if err != nil || d <= 0 {
		return defaultTimeout
	}
	return d
}

func (r *Runtime) Invoke(ctx context.Context, in domain.FunctionRequest) (*domain.FunctionResponse, error) {
	// Bounded in-flight gate: acquire a slot non-blockingly. If the gate is full
	// we return ErrSaturated (→503) rather than blocking forever.
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	default:
		return nil, ErrSaturated
	}

	// Pick a healthy worker. If none is healthy, that's a 502-class failure.
	w := r.pickWorker()
	if w == nil {
		return nil, ErrWorkerFailed
	}

	// Build the headers map for X-Inz-Context, defaulting Content-Type to
	// application/json when the caller did not set one. This keeps
	// TestInvokeHelloFunction passing (it sends a JSON body with no explicit CT)
	// while still allowing callers to override with e.g. text/plain.
	headers := in.Headers
	hasCT := false
	for k := range headers {
		if strings.EqualFold(k, "Content-Type") {
			hasCT = true
			break
		}
	}
	if !hasCT {
		m := make(map[string][]string, len(headers)+1)
		for k, v := range headers {
			m[k] = v
		}
		m["Content-Type"] = []string{"application/json"}
		headers = m
	}

	// Mint a short-lived service-role token for ctx.serviceClient. A nil
	// MintService (e.g. unit tests) yields no token; the shim then falls back
	// to the anon key for serviceClient, which means no escalation.
	var serviceTok string
	if r.opts.MintService != nil {
		var mintErr error
		if serviceTok, mintErr = r.opts.MintService(ctx); mintErr != nil {
			return nil, fmt.Errorf("funcs: mint service token: %w", mintErr)
		}
	}

	// Anon apikey (lazily minted + cached on first invoke, so the mint runs
	// after migrations have created auth.jwt_keys).
	anonTok, anonErr := r.anonKey(ctx)
	if anonErr != nil {
		return nil, fmt.Errorf("funcs: anon token: %w", anonErr)
	}

	// Resolve the function's env: map at invoke time.
	// Refs (${INSTANCEZ_ENV_*}) are resolved from EnvMap; literals pass through as-is.
	// Guaranteed: any ref present in the function config was validated at New time,
	// so opts.EnvMap[ref] is always populated here.
	resolved := make(map[string]string)
	if fn, ok := r.opts.Functions[in.Name]; ok {
		for k, v := range fn.Env {
			if ref, isRef := asInstancezEnvRef(v); isRef {
				resolved[k] = r.envmap[ref]
			} else {
				resolved[k] = v // literal
			}
		}
	}

	ctxJSON, err := json.Marshal(map[string]any{
		"method":    in.Method,
		"path":      in.Path,
		"query":     in.Query,
		"headers":   headers,
		"claims":    in.Claims,
		"env":       resolved,
		"requestId": in.RequestID,
		"dataPlane": map[string]any{
			"url":          r.opts.LoopbackURL,
			"anonKey":      anonTok,
			"callerToken":  in.CallerToken,
			"serviceToken": serviceTok,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("funcs: marshal context: %w", err)
	}

	// Enforce the per-request timeout INSIDE Invoke so the domain port stays
	// small (the handler just maps error types). The deadline applies to the
	// whole request including the response-body read.
	reqCtx, cancel := context.WithTimeout(ctx, r.timeoutFor(in.Name))
	defer cancel()

	req, _ := http.NewRequestWithContext(reqCtx, "POST", "http://unix/invoke", bytes.NewReader(in.Body))
	req.Header.Set("x-inz-fn", in.Name)
	req.Header.Set("x-inz-context", base64.StdEncoding.EncodeToString(ctxJSON))
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, r.classifyDoErr(ctx, reqCtx, w, err)
	}
	defer resp.Body.Close()
	body := new(bytes.Buffer)
	if _, err := body.ReadFrom(resp.Body); err != nil {
		// The deadline can fire mid-read; classify the same way as Do.
		return nil, r.classifyDoErr(ctx, reqCtx, w, err)
	}
	return &domain.FunctionResponse{Status: resp.StatusCode, Headers: resp.Header, Body: body.Bytes()}, nil
}

// classifyDoErr maps a client.Do / body-read error to the right typed error and
// triggers a worker restart on a genuine transport/connection failure.
//
// A timeout and a crash look identical at the call site but must be handled
// oppositely: a timeout means the worker is FINE (just slow) so we must NOT
// mark it unhealthy or restart it; a transport error means the process died so
// we mark it unhealthy and restart it.
func (r *Runtime) classifyDoErr(callerCtx, reqCtx context.Context, w *worker, err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		// Per-request deadline fired: worker is healthy, response discarded.
		return ErrTimeout
	case callerCtx.Err() != nil:
		// The caller's context was cancelled (not our timeout): propagate it.
		return callerCtx.Err()
	default:
		// Transport/connection error: the worker process likely died. Mark it
		// unhealthy exactly once (CAS) and trigger an async restart.
		if w.healthy.CompareAndSwap(true, false) {
			r.triggerRestart(w)
		}
		return fmt.Errorf("%w: %v", ErrWorkerFailed, err)
	}
}

// restartBackoffMin / restartBackoffMax bound the interruptible backoff used by
// the restart retry loop. The backoff starts at min and doubles up to max so a
// function that crashes on every boot (e.g. an import error) degrades to a slow
// poll rather than a hot spin, while a transient failure recovers quickly.
const (
	restartBackoffMin = 200 * time.Millisecond
	restartBackoffMax = 5 * time.Second
)

// triggerRestart re-spawns a replacement for the dead worker in a goroutine and
// swaps it into the dead worker's slot. The spawn is RETRIED with an
// interruptible backoff until it succeeds or the runtime is closed, so a slot
// is self-healing: a transient spawn failure (node fork limit, OOM, slow boot)
// does not permanently strand the slot with a dead worker.
//
// It guards against:
//   - spawn failures: retry with exponential backoff (min→max), so the slot
//     recovers once the transient condition clears.
//   - restart storms: the same backoff throttles a function that fails to boot.
//   - resurrection after Close: the Store happens under r.mu; if Close already
//     set closed, the freshly spawned worker is killed instead of installed.
//   - Close hangs: the backoff is a select on r.done, so Close (which closes
//     done under mu) wakes the loop immediately; the goroutine then bails and
//     restartWG.Wait() returns promptly.
//
// Exactly one restart runs per crash because the caller CAS-flips healthy.
func (r *Runtime) triggerRestart(dead *worker) {
	// Don't start a restart if we're already closing. restartWG.Add(1) happens
	// here under mu while !closed; Close sets closed under mu before Wait(), so
	// Add can never race Wait.
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.restartWG.Add(1)
	r.mu.Unlock()

	go func() {
		// defer Done() fires exactly once when the goroutine exits, regardless
		// of how many spawn attempts the retry loop made.
		defer r.restartWG.Done()

		// Reap the dead process and clean up its socket. The scanner goroutines
		// for the dead worker drain and exit when its pipes close on Kill/exit.
		_ = dead.cmd.Process.Kill()
		_ = dead.cmd.Wait()
		_ = os.Remove(dead.sock)

		// Find the slot currently holding the dead worker once, before the loop.
		// (We compare by pointer; if it's already been replaced, nothing to do.)
		idx := -1
		for i := range r.slots {
			if r.slots[i].Load() == dead {
				idx = i
				break
			}
		}
		if idx < 0 {
			return
		}

		fnSpec := r.fnSpec()
		backoff := restartBackoffMin
		for {
			// Bail promptly if we're closing (checked at the top of each
			// iteration so a pending loop never outlives Close beyond one
			// in-flight spawn attempt).
			r.mu.Lock()
			closed := r.closed
			r.mu.Unlock()
			if closed {
				return
			}

			nw, err := r.spawnWorker(fnSpec)
			if err != nil {
				r.logger.Warn("funcs: worker restart failed; will retry", "err", err, "backoff", backoff)
				// Interruptible backoff: wake immediately on Close.
				select {
				case <-r.done:
					return
				case <-time.After(backoff):
				}
				backoff *= 2
				if backoff > restartBackoffMax {
					backoff = restartBackoffMax
				}
				continue
			}

			// Install the new worker only if we're not closing. The check-and-
			// Store must be atomic w.r.t. Close setting closed, so both happen
			// under mu.
			r.mu.Lock()
			if r.closed {
				r.mu.Unlock()
				// We lost the race with Close: tear down the worker we just
				// spawned so we don't leak a node process / socket past Close.
				_ = nw.cmd.Process.Kill()
				_ = nw.cmd.Wait()
				_ = os.Remove(nw.sock)
				return
			}
			r.slots[idx].Store(nw)
			r.mu.Unlock()
			return
		}
	}()
}

// fnSpec rebuilds the "name=absPath,..." spec string passed to node.
func (r *Runtime) fnSpec() string {
	var spec []string
	for name, fn := range r.opts.Functions {
		spec = append(spec, name+"="+filepath.Join(r.opts.Dir, fn.File))
	}
	return strings.Join(spec, ",")
}

func (r *Runtime) Close() error {
	// Mark closed under mu so any restart goroutine that observes closed will
	// not Store a resurrected worker after this point.
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	// Close done in the SAME critical section guarded by the idempotency check
	// above — that check is the double-close guard. Closing done wakes any
	// pending restart backoff so restartWG.Wait() below cannot deadlock.
	close(r.done)
	r.mu.Unlock()

	// Wait for any in-flight restart goroutines to finish (they'll either have
	// installed a worker before we set closed, or will tear down and bail).
	r.restartWG.Wait()

	// Kill all workers first so the children's pipe write ends close, which
	// makes the scanner goroutines drain remaining bytes and exit on EOF.
	for i := range r.slots {
		if w := r.slots[i].Load(); w != nil {
			if w.cmd != nil && w.cmd.Process != nil {
				_ = w.cmd.Process.Kill()
			}
		}
	}
	// Wait for scanner goroutines to drain BEFORE cmd.Wait() so we don't miss
	// trailing log bytes and don't emit a spurious error from racing pipe reads.
	r.scanWG.Wait()
	// Reap children and remove their sockets.
	for i := range r.slots {
		if w := r.slots[i].Load(); w != nil {
			if w.cmd != nil {
				_ = w.cmd.Wait()
			}
			_ = os.Remove(w.sock)
		}
	}
	_ = os.Remove(r.shim)
	return nil
}
