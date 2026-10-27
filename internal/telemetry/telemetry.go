package telemetry

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/posthog/posthog-go"
)

var (
	APIKey = "phc_pTBwgBY5a4f2TsTMBhsqonSmTAC8duxSjFscRUfpKdPK"
	Host   = prodHost
)

const (
	prodHost      = "https://us.i.posthog.com"
	flushCap      = 2 * time.Second
	sourceBuild   = "source build"
	notice        = "inz collects anonymous usage data to improve the CLI.\nDisable: inz telemetry disable  (or INSTANCEZ_TELEMETRY_DISABLED=1)\nDetails: https://docs.instancez.ai/telemetry/\n"
	disabledByCmd = "disabled by inz telemetry disable"
)

var agentEnvVars = []string{"CLAUDECODE", "CURSOR_AGENT", "GEMINI_CLI", "CODEX_SANDBOX"}

type Options struct {
	Version, Commit string
	FlagDisabled    bool
	Getenv          func(string) string
	Home            string
	Stderr          io.Writer
	NoPersist       bool
}

type Client struct {
	o      Options
	st     state
	reason string
	mu     sync.Mutex
	ph     posthog.Client
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Init loads or creates the state file and never returns nil.
func Init(o Options) *Client {
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
	if o.Home == "" {
		o.Home, _ = os.UserHomeDir()
	}
	st, fresh, stErr := loadState(o.Home)
	c := &Client{o: o, st: st}
	switch {
	case o.Version == "dev":
		c.reason = sourceBuild
	case truthy(o.Getenv("INSTANCEZ_TELEMETRY_DISABLED")):
		c.reason = "INSTANCEZ_TELEMETRY_DISABLED"
	case truthy(o.Getenv("DO_NOT_TRACK")):
		c.reason = "DO_NOT_TRACK"
	case o.FlagDisabled:
		c.reason = "--no-telemetry"
	case stErr != nil:
		c.reason = "unreadable ~/.instancez/telemetry.json; inz telemetry enable resets it"
	case c.st.Disabled:
		c.reason = disabledByCmd
	}
	if fresh && c.reason == "" && !o.NoPersist && o.Home != "" {
		_ = saveState(o.Home, c.st)
	}
	return c
}

func (c *Client) Enabled() (bool, string) { return c.reason == "", c.reason }

func (c *Client) AnonymousID() string { return c.st.AnonymousID }

// MaybeShowNotice prints the notice once while telemetry is enabled.
func (c *Client) MaybeShowNotice() {
	if c.reason != "" || c.st.NoticeShown {
		return
	}
	_, _ = io.WriteString(c.o.Stderr, notice)
	c.st.NoticeShown = true
	if c.o.Home != "" {
		_ = saveState(c.o.Home, c.st)
	}
}

func (c *Client) debug() bool { return truthy(c.o.Getenv("INSTANCEZ_TELEMETRY_DEBUG")) }

// Capture queues an event. Debug mode prints it instead of sending.
func (c *Client) Capture(event string, props map[string]any) {
	debug := c.debug()
	if c.reason != "" && (!debug || c.reason != sourceBuild) {
		return
	}
	p := c.withBase(props)
	if debug {
		b, _ := json.Marshal(map[string]any{"event": event, "distinct_id": c.st.AnonymousID, "properties": c.wireProps(event, p)})
		_, _ = fmt.Fprintf(c.o.Stderr, "%s\n", b)
		return
	}
	if testing.Testing() && Host == prodHost {
		return // tests must stub Host
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ph == nil {
		ph, err := posthog.NewWithConfig(APIKey, posthog.Config{
			Endpoint:        Host,
			Logger:          posthog.StdLogger(log.New(io.Discard, "", 0), false),
			IsServer:        posthog.Ptr(false),
			ShutdownTimeout: flushCap,
		})
		if err != nil {
			return
		}
		c.ph = ph
	}
	_ = c.ph.Enqueue(posthog.Capture{DistinctId: c.st.AnonymousID, Event: event, Properties: p})
}

// Close flushes queued events, waiting at most 2 seconds.
func (c *Client) Close() {
	c.mu.Lock()
	ph := c.ph
	c.ph = nil
	c.mu.Unlock()
	if ph == nil {
		return
	}
	_ = ph.Close()
}

// withBase adds the properties every event carries.
func (c *Client) withBase(props map[string]any) map[string]any {
	agent := false
	for _, k := range agentEnvVars {
		agent = agent || c.o.Getenv(k) != ""
	}
	p := map[string]any{
		"version":  c.o.Version,
		"commit":   c.o.Commit,
		"os":       runtime.GOOS,
		"arch":     runtime.GOARCH,
		"is_ci":    c.o.Getenv("CI") != "",
		"is_agent": agent,
	}
	for k, v := range props {
		p[k] = v
	}
	p["$process_person_profile"] = false
	p["$geoip_disable"] = true
	return p
}

// wireProps adds what posthog-go appends on send, so debug output matches the wire.
func (c *Client) wireProps(event string, p map[string]any) map[string]any {
	if m, ok := (posthog.Capture{DistinctId: c.st.AnonymousID, Event: event, Properties: p}).APIfy().(posthog.CaptureInApi); ok {
		return map[string]any(m.Properties)
	}
	return p
}
