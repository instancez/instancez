package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/instancez/instancez/internal/cloud"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderLogs(t *testing.T) {
	render := func(res *cloud.LogsResponse) (string, string) {
		var o, e bytes.Buffer
		renderLogs(&o, &e, res, 15)
		return o.String(), e.String()
	}

	out, errOut := render(&cloud.LogsResponse{Deployed: true, Events: []cloud.LogEvent{
		{TS: 1780315200000, Message: "hi\x1b[2J"},
		{TS: 0, Message: "epoch"},
	}})
	assert.Equal(t, "2026-06-01T12:00:00Z hi[2J\n1970-01-01T00:00:00Z epoch\n", out)
	assert.Empty(t, errOut)

	out, errOut = render(&cloud.LogsResponse{Deployed: true})
	assert.Equal(t, "no logs in the last 15 minutes\n", out)
	assert.Empty(t, errOut)

	out, errOut = render(&cloud.LogsResponse{Deployed: true, Truncated: true, Events: []cloud.LogEvent{{TS: 1780315200000, Message: "x"}}})
	assert.Equal(t, "2026-06-01T12:00:00Z x\n", out)
	assert.Equal(t, "output truncated: older lines omitted, use a smaller --minutes window\n", errOut)

	out, errOut = render(&cloud.LogsResponse{Deployed: false})
	assert.Empty(t, out)
	assert.Contains(t, errOut, "not deployed")
}

type logsEnv struct {
	cfg  string
	hits *atomic.Int32
	last *atomic.Value
}

func TestLogsTruncatedEndToEnd(t *testing.T) {
	env := logsSetup(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"events":[{"ts":1780315200000,"message":"x"}],"deployed":true,"truncated":true,"minutes":5}`))
	}, true)
	out, errOut, err := runLogsCmd(t, "--config", env.cfg)
	require.NoError(t, err)
	assert.Equal(t, "2026-06-01T12:00:00Z x\n", out)
	assert.Contains(t, errOut, "truncated")
}

func TestLogsErrorBodyControlCharsSanitized(t *testing.T) {
	env := logsSetup(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"error":"bad\u001b[2Jthing"}`))
	}, true)
	_, _, err := runLogsCmd(t, "--config", env.cfg)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "\x1b")
	assert.Contains(t, err.Error(), "bad[2Jthing")
}

func logsSetup(t *testing.T, handler http.HandlerFunc, saveCreds bool) logsEnv {
	t.Helper()
	env := logsEnv{hits: &atomic.Int32{}, last: &atomic.Value{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env.hits.Add(1)
		env.last.Store(r.URL.String())
		if r.URL.Path != "/instancez/projects/proj_1/logs" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer pat" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("INSTANCEZ_CLOUD_API", srv.URL)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("INSTANCEZ_CLOUD_PAT", "")
	if saveCreds {
		require.NoError(t, cloud.Save(cloud.Credentials{PAT: "pat"}))
	}
	env.cfg = filepath.Join(t.TempDir(), "instancez.yaml")
	require.NoError(t, os.WriteFile(env.cfg, []byte("project:\n  cloud:\n    project_id: proj_1\n"), 0o600))
	return env
}

func runLogsCmd(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var o, e bytes.Buffer
	root := NewRootCmd()
	root.SetArgs(append([]string{"cloud", "logs"}, args...))
	root.SetOut(&o)
	root.SetErr(&e)
	err = root.Execute()
	return o.String(), e.String(), err
}

func TestLogsEndToEnd(t *testing.T) {
	env := logsSetup(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"events":[{"ts":1780315200000,"message":"a\u001b[31m"},{"ts":1780315201000,"message":"b"}],"deployed":true,"truncated":false,"minutes":60}`))
	}, true)

	out, _, err := runLogsCmd(t, "--config", env.cfg, "--minutes", "60")
	require.NoError(t, err)
	assert.Equal(t, "2026-06-01T12:00:00Z a[31m\n2026-06-01T12:00:01Z b\n", out)
	assert.Equal(t, "/instancez/projects/proj_1/logs?minutes=60", env.last.Load())

	_, _, err = runLogsCmd(t, "--config", env.cfg)
	require.NoError(t, err)
	assert.Equal(t, "/instancez/projects/proj_1/logs?minutes=5", env.last.Load())
}

func TestLogsJSON(t *testing.T) {
	env := logsSetup(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"events":null,"deployed":true,"truncated":true}`))
	}, true)
	out, _, err := runLogsCmd(t, "--config", env.cfg, "--json")
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &got), out)
	assert.Equal(t, []any{}, got["events"])
	assert.EqualValues(t, 5, got["minutes"])
	assert.Equal(t, true, got["truncated"])
}

func TestLogsJSONNoPrompt(t *testing.T) {
	env := logsSetup(t, func(w http.ResponseWriter, r *http.Request) {}, false)
	var prompted bool
	prev := logsLogin
	logsLogin = ensureLoginOpts{
		isTTY:   func() bool { return true },
		confirm: func(string) bool { prompted = true; return false },
	}
	t.Cleanup(func() { logsLogin = prev })
	out, _, err := runLogsCmd(t, "--config", env.cfg, "--json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inz cloud login")
	assert.False(t, prompted)
	assert.Zero(t, env.hits.Load())
	assert.Empty(t, out)
}

func TestLogsBadMinutesNeverHitsServer(t *testing.T) {
	env := logsSetup(t, func(w http.ResponseWriter, r *http.Request) {}, true)
	for _, m := range []string{"0", "7", "1441"} {
		_, _, err := runLogsCmd(t, "--config", env.cfg, "--minutes", m)
		require.Error(t, err, m)
		assert.Contains(t, err.Error(), "5, 15, 30, 60, 180, 360, 720, 1440")
	}
	assert.Zero(t, env.hits.Load())
}

func TestLogsNoLinkedProject(t *testing.T) {
	logsSetup(t, func(w http.ResponseWriter, r *http.Request) {}, true)
	cfg := filepath.Join(t.TempDir(), "instancez.yaml")
	require.NoError(t, os.WriteFile(cfg, []byte("project: {}\n"), 0o600))
	_, _, err := runLogsCmd(t, "--config", cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "project_id")
}

func TestLogsErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"404", 404, `{"error":"not_found"}`, "project not found"},
		{"401", 401, `{"error":"unauthorized"}`, "inz cloud login"},
		{"502", 502, `{"error":"upstream"}`, "log source unavailable"},
		{"429", 429, `{"error":"slow"}`, "rate limited"},
		{"500", 500, `boom`, "get logs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := logsSetup(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}, true)
			_, _, err := runLogsCmd(t, "--config", env.cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.EqualValues(t, 1, env.hits.Load())
		})
	}
}

func TestLogsNonJSONSuccessBody(t *testing.T) {
	env := logsSetup(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>")) }, true)
	_, _, err := runLogsCmd(t, "--config", env.cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode response")
}
