//go:build integration

package pgrupstream

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
		{"/rest/v1/messages?select=id,users!inner(username)&users.status=eq.OFFLINE", "*/0", 0},
		{"/rest/v1/users?select=username&limit=2", "0-1/5", 2},
	}
	for _, c := range cases {
		status, hdr, raw := call(t, "GET", testTS.URL+c.path, "", exact, false)
		require.Equal(t, 200, status, "%s: %s", c.path, raw)
		require.Len(t, rowsOf(t, raw), c.wantRows, c.path)
		require.Equal(t, c.wantRange, hdr.Get("Content-Range"), c.path)
	}
	// Non-inner to-one filter nulls the embed and keeps every parent row.
	status, hdr, raw := call(t, "GET", testTS.URL+"/rest/v1/messages?select=id,users(username)&users.username=eq.kiwicopple", "", exact, false)
	require.Equal(t, 200, status, "%s", raw)
	rows := rowsOf(t, raw)
	require.Len(t, rows, 2)
	require.Equal(t, "0-1/2", hdr.Get("Content-Range"))
	for _, r := range rows {
		require.Contains(t, r, "users")
		require.Nil(t, r["users"], "unmatched to-one embed must be null: %v", r)
	}
	_, _, raw = call(t, "GET", testTS.URL+"/rest/v1/messages?select=id,users(username)&users.username=eq.supabot", "", exact, false)
	for _, r := range rowsOf(t, raw) {
		require.Equal(t, map[string]any{"username": "supabot"}, r["users"])
	}
	status, hdr, raw = call(t, "GET", testTS.URL+"/rest/v1/users?select=username,messages(id)&messages.id=eq.-1&username=eq.supabot", "", exact, false)
	require.Equal(t, 200, status, "%s", raw)
	rows = rowsOf(t, raw)
	require.Len(t, rows, 1)
	require.Equal(t, []any{}, rows[0]["messages"])
	require.Equal(t, "0-0/1", hdr.Get("Content-Range"))

	_, hdr, _ = call(t, "GET", testTS.URL+"/rest/v1/users?select=username,messages!inner(id)", "", map[string]string{"Prefer": "count=planned"}, false)
	require.Regexp(t, `^0-0/\d+$`, hdr.Get("Content-Range"))
}

func TestHardening_RPCCountRunsInReadOnlyTx(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}
	status, hdr, raw := call(t, "GET", testTS.URL+"/rest/v1/rpc/users_if_read_only?select=username", "", map[string]string{"Prefer": "count=exact"}, false)
	require.Equal(t, 200, status, "%s", raw)
	require.Len(t, rowsOf(t, raw), 5)
	require.Equal(t, "0-4/5", hdr.Get("Content-Range"))
}

func TestHardening_RPCCountRunsFunctionOnce(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}
	calls := func() int64 {
		row, err := testDB.QueryRow(context.Background(), "SELECT n FROM rpc_calls")
		require.NoError(t, err)
		return row["n"].(int64)
	}
	cases := []struct {
		query, prefer, wantRange string
		wantRows                 int
	}{
		{"", "count=exact", "0-4/5", 5},
		{"?username=neq.supabot&order=username.desc&limit=2", "count=exact", "0-1/4", 2},
		{"?offset=10", "count=exact", "*/5", 0},
		{"?limit=0", "count=exact", "*/5", 0},
		{"?username=eq.nobody", "count=exact", "*/0", 0},
		{"?select=username,messages(id)&username=eq.supabot", "count=exact", "0-0/1", 1},
		{"?select=count()", "count=exact", "0-0/5", 1},
		{"?select=status,count()", "count=exact", "0-1/5", 2},
		{"?select=status,count()&having=count.gt.100", "count=exact", "*/5", 0},
		{"?username=eq.nobody", "", "*/*", 0},
		{"?select=status,count()", "count=planned", `^0-1/\d+$`, 2},
		{"", "count=planned", `^0-4/\d+$`, 5},
		{"", "count=estimated", `^0-4/\d+$`, 5},
		{"?username=neq.supabot", "count=planned", `^0-3/\d+$`, 4},
		{"", "", "0-4/*", 5},
	}
	for _, c := range cases {
		before := calls()
		hdrs := map[string]string{}
		if c.prefer != "" {
			hdrs["Prefer"] = c.prefer
		}
		status, hdr, raw := call(t, "POST", testTS.URL+"/rest/v1/rpc/counted_users"+c.query, `{}`, hdrs, false)
		require.Equal(t, 200, status, "%s: %s", c.query, raw)
		rows := rowsOf(t, raw)
		require.Len(t, rows, c.wantRows, c.query)
		require.NotContains(t, string(raw), "__inz_")
		if strings.HasPrefix(c.wantRange, "^") {
			require.Regexp(t, c.wantRange, hdr.Get("Content-Range"), c.query)
		} else {
			require.Equal(t, c.wantRange, hdr.Get("Content-Range"), c.query)
		}
		require.Equal(t, before+1, calls(), "%s %s: function must run exactly once", c.query, c.prefer)
	}

	_, _, raw := call(t, "POST", testTS.URL+"/rest/v1/rpc/counted_users?order=username.desc&select=username", `{}`, map[string]string{"Prefer": "count=exact"}, false)
	got := rowsOf(t, raw)
	require.Len(t, got, 5)
	require.Equal(t, "supabot", got[0]["username"])
	require.Equal(t, "acupofjose", got[4]["username"])

	status, _, raw := call(t, "POST", testTS.URL+"/rest/v1/rpc/counted_users?username=eq.supabot", `{}`,
		map[string]string{"Prefer": "count=exact", "Accept": "application/vnd.pgrst.object+json"}, false)
	require.Equal(t, 200, status, "%s", raw)
	var obj map[string]any
	require.NoError(t, json.Unmarshal(raw, &obj), "%s", raw)
	require.Equal(t, "supabot", obj["username"])
	require.NotContains(t, string(raw), "__inz_")
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

	// supabase-js .explain() default header; the JSON body is the bare EXPLAIN document.
	status, hdr, raw := call(t, "GET", testTS.URL+"/rest/v1/users", "", map[string]string{
		"Accept": `application/vnd.pgrst.plan+json; for="application/json"; options=;`}, false)
	require.Equal(t, 200, status, "%s", raw)
	require.Contains(t, hdr.Get("Content-Type"), "application/vnd.pgrst.plan+json")
	var doc []map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc), "%s", raw)
	require.Contains(t, doc[0], "Plan")
	require.NotContains(t, doc[0], "Execution Time")

	status, hdr, raw = call(t, "GET", testTS.URL+"/rest/v1/users", "", map[string]string{
		"Accept": `application/vnd.pgrst.plan+text; for="application/json"; options=analyze|buffers;`}, false)
	require.Equal(t, 200, status, "%s", raw)
	require.Contains(t, hdr.Get("Content-Type"), "application/vnd.pgrst.plan+text")
	require.Contains(t, string(raw), "Execution Time")
	require.Contains(t, string(raw), "Buffers")

	// A comma list is negotiated like PostgREST: the first entry after q and specificity ordering wins.
	for accept, wantCT := range map[string]string{
		"application/vnd.pgrst.plan+json, application/json":                         "application/vnd.pgrst.plan+json",
		"application/json, application/vnd.pgrst.plan+json":                         "application/json",
		`application/json, application/vnd.pgrst.plan+text; for="application/json"`: "application/vnd.pgrst.plan+text",
	} {
		status, hdr, raw = call(t, "GET", testTS.URL+"/rest/v1/users", "", map[string]string{"Accept": accept}, false)
		require.Equal(t, 200, status, "%s: %s", accept, raw)
		require.Contains(t, hdr.Get("Content-Type"), wantCT, accept)
	}

	status, _, raw = call(t, "GET", testTS.URL+"/rest/v1/users", "", map[string]string{
		"Accept": `application/vnd.pgrst.plan+text; for="application/json"; options=wal;`}, false)
	require.Equal(t, 400, status, "%s", raw)
	require.Contains(t, string(raw), "22023")

	status, hdr, raw = call(t, "GET", testTS.URL+"/rest/v1/users", "", map[string]string{"Accept": `application/vnd.pgrst.plan; for="text/xml", application/json`}, false)
	require.Equal(t, 200, status, "%s", raw)
	require.Contains(t, hdr.Get("Content-Type"), "application/json")
	require.NotContains(t, string(raw), "Plan")
	status, _, _ = call(t, "GET", testTS.URL+"/rest/v1/users", "", map[string]string{"Accept": `application/vnd.pgrst.plan; for="text/xml"`}, false)
	require.Equal(t, 406, status)
	status, _, _ = call(t, "GET", testTS.URL+"/rest/v1/users", "", map[string]string{"Accept": `application/vnd.pgrst.plan; for="text/xml", application/vnd.pgrst.plan+json`}, true)
	require.Equal(t, 406, status, "anon never gets a plan")
	status, _, _ = call(t, "GET", testTS.URL+"/rest/v1/users", "", map[string]string{"Accept": "Application/Vnd.Pgrst.Plan"}, true)
	require.Equal(t, 406, status)
}

func TestHardening_PlanRefusedOnWritesAndRPC(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}
	channels := func() []map[string]any {
		status, _, raw := call(t, "GET", testTS.URL+"/rest/v1/channels?select=id,slug&order=id", "", nil, false)
		require.Equal(t, 200, status, "%s", raw)
		return rowsOf(t, raw)
	}
	before := channels()
	require.NotEmpty(t, before)
	for _, accept := range []string{`application/vnd.pgrst.plan+text; for="application/json"; options=analyze;`, "Application/Vnd.Pgrst.Plan+Json"} {
		for _, anon := range []bool{false, true} {
			plan := map[string]string{"Accept": accept, "Prefer": "return=representation,resolution=merge-duplicates"}
			for _, r := range []struct{ method, path, body string }{
				{"POST", "/rest/v1/channels", `{"slug":"planned"}`},
				{"PUT", "/rest/v1/channels?id=eq.1", `{"id":1,"slug":"planned"}`},
				{"PATCH", "/rest/v1/channels?id=eq.1", `{"slug":"planned"}`},
				{"DELETE", "/rest/v1/channels?id=eq.1", ""},
				{"GET", "/rest/v1/rpc/greet", ""},
				{"HEAD", "/rest/v1/rpc/greet", ""},
			} {
				status, _, raw := call(t, r.method, testTS.URL+r.path, r.body, plan, anon)
				require.Equal(t, 406, status, "%s %s anon=%v: %s", r.method, r.path, anon, raw)
				if r.method != "HEAD" {
					require.Contains(t, string(raw), "PGRST107")
				}
			}
			// A volatile RPC that ran would take 2s.
			start := time.Now()
			status, _, raw := call(t, "POST", testTS.URL+"/rest/v1/rpc/sleep_for", `{"secs":2}`, plan, anon)
			require.Equal(t, 406, status, "%s", raw)
			require.Less(t, time.Since(start), time.Second)
		}
	}
	// A malformed token or an Accept list that only mentions a plan must still be refused before auth or SQL.
	for _, h := range []map[string]string{
		{"Accept": "application/vnd.pgrst.plan+json", "Authorization": "Bearer not.a.jwt"},
		{"Accept": "application/vnd.pgrst.plan+json, application/json", "Prefer": "return=representation"},
		{"Accept": "application/json, Application/Vnd.Pgrst.Plan", "Prefer": "return=representation"},
	} {
		anon := h["Authorization"] != "" // the secret apikey would skip token checks
		for _, r := range []struct{ method, path, body string }{
			{"POST", "/rest/v1/channels", `{"slug":"planned"}`},
			{"PUT", "/rest/v1/channels?id=eq.1", `{"id":1,"slug":"planned"}`},
			{"PATCH", "/rest/v1/channels?id=eq.1", `{"slug":"planned"}`},
			{"DELETE", "/rest/v1/channels?id=eq.1", ""},
			{"POST", "/rest/v1/rpc/greet", `{}`},
		} {
			status, _, raw := call(t, r.method, testTS.URL+r.path, r.body, h, anon)
			require.Equal(t, 406, status, "%s %s %v: %s", r.method, r.path, h, raw)
			require.Contains(t, string(raw), "PGRST107")
		}
	}
	require.Equal(t, before, channels())
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
