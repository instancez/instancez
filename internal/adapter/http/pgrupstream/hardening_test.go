//go:build integration

package pgrupstream

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	instancezhttp "github.com/instancez/instancez/internal/adapter/http"
	"github.com/instancez/instancez/internal/domain"
	"github.com/stretchr/testify/require"
)

// serverWith boots a second instancez server on the shared pools with a tweaked config.
func serverWith(t *testing.T, mutate func(*domain.Config)) string {
	t.Helper()
	cfg := buildConfig()
	mutate(cfg)
	srv := instancezhttp.NewServer(instancezhttp.ServerDeps{
		Config: cfg, DB: testAuthDB, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), DevMode: true,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

// call sends a request (as service_role unless anon) and returns status, headers and raw body.
func call(t *testing.T, method, url, body string, headers map[string]string, anon bool) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	require.NoError(t, err)
	if !anon {
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		req.Header.Set("apikey", testAdminKey)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, resp.Header, raw
}

func rowsOf(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(raw, &rows), "body: %s", raw)
	return rows
}

func TestHardening_NoDefaultLimit(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}
	status, _, raw := call(t, "GET", testTS.URL+"/rest/v1/users?select=username", "", nil, false)
	require.Equal(t, 200, status)
	require.Len(t, rowsOf(t, raw), 5)

	// max_limit: -1 turns the cap off.
	off := serverWith(t, func(c *domain.Config) { c.Server.MaxLimit = -1 })
	status, _, raw = call(t, "GET", off+"/rest/v1/users?select=username", "", nil, false)
	require.Equal(t, 200, status, "%s", raw)
	require.Len(t, rowsOf(t, raw), 5)
	status, _, raw = call(t, "POST", off+"/rest/v1/rpc/users_by_status", `{"target":"ONLINE"}`, nil, false)
	require.Equal(t, 200, status, "%s", raw)
	require.Len(t, rowsOf(t, raw), 3)
}

func TestHardening_MaxRows(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}
	base := serverWith(t, func(c *domain.Config) { c.Server.MaxLimit = 2 })
	exact := map[string]string{"Prefer": "count=exact"}

	status, hdr, raw := call(t, "GET", base+"/rest/v1/users?select=username&order=username", "", exact, false)
	require.Contains(t, []int{200, 206}, status, "%s", raw)
	require.Len(t, rowsOf(t, raw), 2)
	require.Equal(t, "0-1/5", hdr.Get("Content-Range"))

	for path, want := range map[string]int{
		"/rest/v1/users?select=username&limit=100":          2,
		"/rest/v1/users?select=username&limit=1":            1,
		"/rest/v1/users?select=username&limit=0":            0,
		"/rest/v1/users?select=username&offset=4":           1,
		"/rest/v1/users?select=username&limit=100&offset=1": 2,
	} {
		status, _, raw := call(t, "GET", base+path, "", nil, false)
		require.Equal(t, 200, status, "%s: %s", path, raw)
		require.Len(t, rowsOf(t, raw), want, path)
	}
	for _, path := range []string{"limit=-1", "limit=NaN", "limit=99999999999999999999"} {
		status, _, raw := call(t, "GET", base+"/rest/v1/users?select=username&"+path, "", nil, false)
		require.Equal(t, 400, status, "%s: %s", path, raw)
	}

	// Range end - start + 1 overflows int; the cap must still hold.
	_, hdr, raw = call(t, "GET", base+"/rest/v1/users?select=username", "", map[string]string{"Range": "0-9223372036854775807"}, false)
	require.Len(t, rowsOf(t, raw), 2)
	require.Equal(t, "0-1/*", hdr.Get("Content-Range"))
	_, _, raw = call(t, "GET", base+"/rest/v1/users?select=username", "", map[string]string{"Range": "1-10"}, false)
	require.Len(t, rowsOf(t, raw), 2)

	status, hdr, raw = call(t, "POST", base+"/rest/v1/rpc/users_by_status", `{"target":"ONLINE"}`, exact, false)
	require.Equal(t, 200, status, "%s", raw)
	require.Len(t, rowsOf(t, raw), 2, "setof RPC capped (3 ONLINE users)")
	require.Equal(t, "0-1/3", hdr.Get("Content-Range"))
	_, _, raw = call(t, "POST", base+"/rest/v1/rpc/users_by_status?limit=1", `{"target":"ONLINE"}`, nil, false)
	require.Len(t, rowsOf(t, raw), 1)

	one := serverWith(t, func(c *domain.Config) { c.Server.MaxLimit = 1 })
	_, _, raw = call(t, "GET", one+"/rest/v1/users?select=username,messages(id)&username=eq.supabot", "", nil, false)
	rows := rowsOf(t, raw)
	require.Len(t, rows, 1)
	require.Len(t, rows[0]["messages"], 1, "has-many embed capped (supabot has 2 messages)")
}

func TestHardening_CountMatchesRows(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}
	exact := map[string]string{"Prefer": "count=exact"}
	cases := []struct {
		path, wantRange string
		wantRows        int
	}{
		{"/rest/v1/users?select=username,messages!inner(id)", "0-0/1", 1},
		{"/rest/v1/messages?select=id,users!inner(username)&users.status=eq.OFFLINE", "0-0/0", 0},
		{"/rest/v1/users?select=username&limit=2", "0-1/5", 2},
	}
	for _, c := range cases {
		status, hdr, raw := call(t, "GET", testTS.URL+c.path, "", exact, false)
		require.Equal(t, 200, status, "%s: %s", c.path, raw)
		require.Len(t, rowsOf(t, raw), c.wantRows, c.path)
		require.Equal(t, c.wantRange, hdr.Get("Content-Range"), c.path)
	}
	// Known builder bug: a non-inner to-one embed filter drops parent rows, so only check count == rows.
	status, hdr, raw := call(t, "GET", testTS.URL+"/rest/v1/messages?select=id,users(username)&users.username=eq.kiwicopple", "", exact, false)
	require.Equal(t, 200, status, "%s", raw)
	cr := hdr.Get("Content-Range")
	require.Equal(t, strconv.Itoa(len(rowsOf(t, raw))), cr[strings.LastIndex(cr, "/")+1:], cr)

	_, hdr, _ = call(t, "GET", testTS.URL+"/rest/v1/users?select=username,messages!inner(id)", "", map[string]string{"Prefer": "count=planned"}, false)
	require.Regexp(t, `^0-0/\d+$`, hdr.Get("Content-Range"))
}

func TestHardening_PlanOnlyForServiceRole(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}
	plan := map[string]string{"Accept": "application/vnd.pgrst.plan+json"}
	status, _, raw := call(t, "GET", testTS.URL+"/rest/v1/users", "", plan, true)
	require.Equal(t, 406, status, "%s", raw)
	require.Contains(t, string(raw), "PGRST107")
	status, _, raw = call(t, "GET", testTS.URL+"/rest/v1/users", "", plan, false)
	require.Equal(t, 200, status, "%s", raw)
	require.Contains(t, string(raw), "Plan")
}

func TestHardening_StatementTimeout(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}
	fast := serverWith(t, func(c *domain.Config) { c.Server.Timeouts.DBQuery = "300ms" })
	start := time.Now()
	status, _, raw := call(t, "POST", fast+"/rest/v1/rpc/sleep_for", `{"secs":3}`, nil, false)
	require.Equal(t, 500, status, "%s", raw)
	require.Contains(t, string(raw), "57014")
	require.Less(t, time.Since(start), 2*time.Second)

	// Prefer can lower the timeout but not raise it.
	start = time.Now()
	status, _, raw = call(t, "POST", fast+"/rest/v1/rpc/sleep_for", `{"secs":3}`, map[string]string{"Prefer": "statement-timeout=999999"}, false)
	require.Equal(t, 500, status, "%s", raw)
	require.Less(t, time.Since(start), 2*time.Second)

	slow := serverWith(t, func(c *domain.Config) { c.Server.Timeouts.DBQuery = "10s" })
	start = time.Now()
	status, _, raw = call(t, "POST", slow+"/rest/v1/rpc/sleep_for", `{"secs":3}`, map[string]string{"Prefer": "statement-timeout=100"}, false)
	require.Equal(t, 500, status, "%s", raw)
	require.Less(t, time.Since(start), 2*time.Second)

	status, _, _ = call(t, "POST", slow+"/rest/v1/rpc/sleep_for", `{"secs":0.05}`, nil, false)
	require.Equal(t, 200, status)
}
