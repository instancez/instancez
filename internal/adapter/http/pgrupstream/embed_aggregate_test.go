//go:build integration

package pgrupstream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConf_AggregateWithEmbeds(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}

	t.Run("to-one embed is a group key", func(t *testing.T) {
		rows := fetchRows(t, "/rest/v1/messages?select=count(),users(username)")
		require.Len(t, rows, 1)
		assert.EqualValues(t, 2, rows[0]["count"])
		assert.Equal(t, map[string]any{"username": "supabot"}, rows[0]["users"])
	})

	t.Run("to-one embed with plain column and column aggregate", func(t *testing.T) {
		rows := fetchRows(t, "/rest/v1/messages?select=channel_id,id.count(),users(username,status)&order=channel_id.asc")
		require.Len(t, rows, 2)
		for i, r := range rows {
			assert.EqualValues(t, i+1, r["channel_id"])
			assert.EqualValues(t, 1, r["count"])
			assert.Equal(t, map[string]any{"username": "supabot", "status": "ONLINE"}, r["users"])
		}
	})

	t.Run("unmatched embed filter groups under null", func(t *testing.T) {
		rows := fetchRows(t, "/rest/v1/messages?select=count(),users(username)&users.status=eq.OFFLINE")
		require.Len(t, rows, 1)
		assert.EqualValues(t, 2, rows[0]["count"])
		assert.Nil(t, rows[0]["users"])
	})

	t.Run("to-many embed is a group key", func(t *testing.T) {
		rows := fetchRows(t, "/rest/v1/users?select=count(),messages(message)&order=count.asc")
		require.Len(t, rows, 2)
		assert.EqualValues(t, 1, rows[0]["count"])
		assert.Len(t, rows[0]["messages"], 2)
		assert.EqualValues(t, 4, rows[1]["count"])
		assert.Equal(t, []any{}, rows[1]["messages"])
	})

	t.Run("to-many embed with filter arg and outer filter", func(t *testing.T) {
		rows := fetchRows(t, "/rest/v1/users?select=status,count(),messages(message)&messages.channel_id=eq.2&status=eq.ONLINE&order=count.asc")
		require.Len(t, rows, 2)
		assert.EqualValues(t, 1, rows[0]["count"])
		assert.Equal(t, []any{map[string]any{"message": "Second message"}}, rows[0]["messages"])
		assert.EqualValues(t, 2, rows[1]["count"])
	})

	t.Run("spread embed columns are group keys", func(t *testing.T) {
		rows := fetchRows(t, "/rest/v1/messages?select=count(),...users(status)")
		require.Len(t, rows, 1)
		assert.Equal(t, map[string]any{"count": float64(2), "status": "ONLINE"}, rows[0])
	})

	t.Run("count=exact counts ungrouped rows", func(t *testing.T) {
		req, err := http.NewRequest("GET", testTS.URL+"/rest/v1/users?select=count(),messages(id)", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		req.Header.Set("apikey", testAdminKey)
		req.Header.Set("Prefer", "count=exact")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		require.Equal(t, 206, resp.StatusCode, string(body))
		assert.Equal(t, "0-1/5", resp.Header.Get("Content-Range"), "count ignores GROUP BY, as in PostgREST")
	})
}

func TestConf_ToOneEmbedFiltersKeepParents(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}

	t.Run("embed-scoped or with no match nulls the embed", func(t *testing.T) {
		rows := fetchRows(t, "/rest/v1/messages?select=id,users(username)&users.or=(status.eq.OFFLINE,age.gt.100)&order=id.asc")
		require.Len(t, rows, 2)
		for _, r := range rows {
			assert.Nil(t, r["users"])
		}
	})

	t.Run("embed-scoped or with a match keeps the embed", func(t *testing.T) {
		rows := fetchRows(t, "/rest/v1/messages?select=id,users(username)&users.or=(status.eq.OFFLINE,age.eq.1)&order=id.asc")
		require.Len(t, rows, 2)
		for _, r := range rows {
			assert.Equal(t, map[string]any{"username": "supabot"}, r["users"])
		}
	})

	t.Run("spread with unmatched filter keeps parents with null columns", func(t *testing.T) {
		rows := fetchRows(t, "/rest/v1/messages?select=id,...users(username,status)&users.status=eq.OFFLINE&order=id.asc")
		require.Len(t, rows, 2)
		for _, r := range rows {
			require.Contains(t, r, "username")
			assert.Nil(t, r["username"])
			assert.Nil(t, r["status"])
		}
	})

	t.Run("spread with matched filter fills columns", func(t *testing.T) {
		rows := fetchRows(t, "/rest/v1/messages?select=id,...users(username)&users.status=eq.ONLINE&order=id.asc")
		require.Len(t, rows, 2)
		for _, r := range rows {
			assert.Equal(t, "supabot", r["username"])
		}
	})
}

func TestConf_RPCCountSingleObjectZeroRows(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}
	status, raw, parsed, _ := rpcPOST(t, "users_by_status", `{"target":"NOBODY"}`, map[string]string{
		"Accept": "application/vnd.pgrst.object+json",
		"Prefer": "count=exact",
	})
	require.Equal(t, 406, status, string(raw))
	m, ok := parsed.(map[string]any)
	require.True(t, ok, string(raw))
	assert.Equal(t, "PGRST116", m["code"])
	assert.Contains(t, m["details"], "0 rows")
	assert.False(t, strings.Contains(string(raw), "__inz"), "helper columns leaked: %s", raw)

	status, raw, parsed, _ = rpcPOST(t, "users_by_status", `{"target":"ONLINE"}`, map[string]string{
		"Accept": "application/vnd.pgrst.object+json",
		"Prefer": "count=exact",
	})
	require.Equal(t, 406, status, string(raw))
	assert.Equal(t, "PGRST116", parsed.(map[string]any)["code"])

	status, raw, _, _ = rpcPOST(t, "users_by_status?username=eq.supabot", `{"target":"ONLINE"}`, map[string]string{
		"Accept": "application/vnd.pgrst.object+json",
		"Prefer": "count=exact",
	})
	require.Equal(t, 200, status, string(raw))
	var obj map[string]any
	require.NoError(t, json.Unmarshal(raw, &obj), string(raw))
	assert.Equal(t, "supabot", obj["username"])
	for k := range obj {
		assert.False(t, strings.HasPrefix(k, "__inz"), "helper column %q leaked", k)
	}
}

func TestConf_RPCAggregateGrouping(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}
	exact := map[string]string{"Prefer": "count=exact"}
	status, raw, parsed, hdr := rpcPOST(t, "users_by_status?select=status,count(),messages(id)&order=status", `{"target":"ONLINE"}`, exact)
	require.Equal(t, 206, status, string(raw))
	rows := parsed.([]any)
	require.Len(t, rows, 2, string(raw))
	byMsgs := map[string]float64{}
	for _, r := range rows {
		m := r.(map[string]any)
		assert.Equal(t, "ONLINE", m["status"])
		b, _ := json.Marshal(m["messages"])
		byMsgs[string(b)] = m["count"].(float64)
	}
	assert.Equal(t, map[string]float64{`[{"id":1},{"id":2}]`: 1, `[]`: 2}, byMsgs, "grouped by the whole embed")
	assert.Equal(t, "0-1/3", hdr.Get("Content-Range"), "count stays ungrouped")
	for _, q := range []string{"select=count(),messages(id)", "select=messages(id),total:username.count()"} {
		status, raw, _, _ := rpcPOST(t, "users_by_status?"+q, `{"target":"ONLINE"}`, nil)
		require.Equal(t, 200, status, q+": "+string(raw))
		assert.NotContains(t, string(raw), "__inz", q)
	}
	for q, want := range map[string]any{"select=count()": float64(3), "select=total:username.count()": float64(3)} {
		status, raw, parsed, _ := rpcPOST(t, "users_by_status?"+q, `{"target":"ONLINE"}`, nil)
		require.Equal(t, 200, status, q+": "+string(raw))
		rows := parsed.([]any)
		require.Len(t, rows, 1, q)
		for _, v := range rows[0].(map[string]any) {
			assert.Equal(t, want, v, q)
		}
	}
}

func TestConf_RPCAggregateGroupBy(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}
	rpc := func(q, body string, headers map[string]string) (int, []byte, http.Header) {
		t.Helper()
		status, raw, _, hdr := rpcPOST(t, "users_by_status?"+q, body, headers)
		return status, raw, hdr
	}
	rows := func(raw []byte) []map[string]any {
		t.Helper()
		var out []map[string]any
		require.NoError(t, json.Unmarshal(raw, &out), string(raw))
		return out
	}
	exact := map[string]string{"Prefer": "count=exact"}

	status, raw, _ := rpc("select=status,count()", `{"target":"OFFLINE"}`, nil)
	require.Equal(t, 200, status, string(raw))
	assert.Equal(t, []map[string]any{{"status": "OFFLINE", "count": float64(2)}}, rows(raw))

	status, raw, hdr := rpc("select=nick:nickname,total:username.count()&order=nickname.asc", `{"target":"OFFLINE"}`, exact)
	require.Equal(t, 200, status, string(raw))
	assert.Equal(t, []map[string]any{{"nick": "jose", "total": float64(1)}, {"nick": "kiwi", "total": float64(1)}}, rows(raw))
	assert.Equal(t, "0-1/2", hdr.Get("Content-Range"), "count=exact counts the two OFFLINE rows")

	status, raw, hdr = rpc("select=nickname,count()&limit=1&order=nickname.desc", `{"target":"OFFLINE"}`, exact)
	require.Equal(t, 206, status, string(raw))
	assert.Equal(t, []map[string]any{{"nickname": "kiwi", "count": float64(1)}}, rows(raw))
	assert.Equal(t, "0-0/2", hdr.Get("Content-Range"))

	status, raw, hdr = rpc("select=status,count()&having=count.gt.2", `{"target":"ONLINE"}`, exact)
	require.Equal(t, 206, status, string(raw))
	assert.Equal(t, []map[string]any{{"status": "ONLINE", "count": float64(3)}}, rows(raw))
	assert.Equal(t, "0-0/3", hdr.Get("Content-Range"), "count ignores HAVING")

	status, raw, hdr = rpc("select=status,count()&having=count.gt.5", `{"target":"ONLINE"}`, exact)
	require.Equal(t, 206, status, string(raw))
	assert.Empty(t, rows(raw))
	assert.Equal(t, "*/3", hdr.Get("Content-Range"), "empty page, ungrouped total")

	status, raw, hdr = rpc("select=count()", `{"target":"ONLINE"}`, exact)
	require.Equal(t, 206, status, string(raw))
	assert.Equal(t, []map[string]any{{"count": float64(3)}}, rows(raw))
	assert.Equal(t, "0-0/3", hdr.Get("Content-Range"), "a bare aggregate counts the source rows")

	status, raw, hdr = rpc("select=status,count()", `{"target":"NOBODY"}`, exact)
	require.Equal(t, 200, status, string(raw))
	assert.Empty(t, rows(raw))
	assert.Equal(t, "*/0", hdr.Get("Content-Range"))

	_, _, hdr = rpc("", `{"target":"NOBODY"}`, nil)
	assert.Equal(t, "*/*", hdr.Get("Content-Range"))

	status, raw, _ = rpc("select=status,count()&username=eq.supabot", `{"target":"ONLINE"}`, nil)
	require.Equal(t, 200, status, string(raw))
	assert.Equal(t, []map[string]any{{"status": "ONLINE", "count": float64(1)}}, rows(raw))
}

func TestConf_StarWithAggregateRejected(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}
	for _, path := range []string{"/rest/v1/users?select=*,count()", "/rest/v1/users?select=count(),*", "/rest/v1/messages?select=*,count(),users(username)"} {
		req, err := http.NewRequest("GET", testTS.URL+path, nil)
		require.NoError(t, err)
		status, body := errorBody(t, req)
		assert.Equal(t, 400, status, path)
		assert.Contains(t, body["message"], "*", path)
	}
	for _, q := range []string{"select=*,count()", "select=count(),*"} {
		status, raw, _, _ := rpcPOST(t, "users_by_status?"+q, `{"target":"ONLINE"}`, nil)
		assert.Equal(t, 400, status, q+": "+string(raw))
	}
}

func TestConf_GroupingErrorIs400(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}
	req, err := http.NewRequest("GET", testTS.URL+"/rest/v1/users?select=status,count()&order=age.asc", nil)
	require.NoError(t, err)
	status, body := errorBody(t, req)
	assert.Equal(t, 400, status)
	assert.Equal(t, "42803", body["code"])

	status, raw, parsed, _ := rpcPOST(t, "users_by_status?select=status,count()&order=age.asc", `{"target":"ONLINE"}`, nil)
	assert.Equal(t, 400, status, string(raw))
	assert.Equal(t, "42803", parsed.(map[string]any)["code"])
}

func TestConf_TableCountAndRangeParity(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}
	for _, c := range []struct {
		path, prefer, want string
		status             int
	}{
		{"/rest/v1/users?select=status,count()", "count=exact", "0-1/5", 206},
		{"/rest/v1/users?select=status,count()&having=count.gt.100", "count=exact", "*/5", 206},
		{"/rest/v1/users?select=count()&status=eq.ONLINE", "count=exact", "0-0/3", 206},
		{"/rest/v1/users?select=status,count()", "count=planned", "", 0},
		{"/rest/v1/users?username=eq.nobody", "count=exact", "*/0", 200},
		{"/rest/v1/users?username=eq.nobody", "", "*/*", 200},
		{"/rest/v1/users?offset=10", "count=exact", "*/5", 416},
		{"/rest/v1/users?limit=0", "count=exact", "*/5", 206},
		{"/rest/v1/users?order=username&limit=2&offset=1", "count=exact", "1-2/5", 206},
	} {
		req, err := http.NewRequest("GET", testTS.URL+c.path, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		req.Header.Set("apikey", testAdminKey)
		if c.prefer != "" {
			req.Header.Set("Prefer", c.prefer)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if c.status == 0 {
			require.Contains(t, []int{200, 206}, resp.StatusCode, c.path+": "+string(body))
		} else {
			require.Equal(t, c.status, resp.StatusCode, c.path+": "+string(body))
		}
		if c.want == "" {
			assert.Regexp(t, `^0-1/\d+$`, resp.Header.Get("Content-Range"), c.path)
			continue
		}
		assert.Equal(t, c.want, resp.Header.Get("Content-Range"), c.path+" "+c.prefer)
	}
}

func TestConf_AggregateInnerEmbedCountsFilteredUngroupedRows(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}
	ctx := context.Background()

	// 3 messages, 2 pass the !inner filter, grouped into 1 row.
	marker := fmt.Sprintf("innagg_%d", time.Now().UnixNano())
	_, err := testClient.From("users").Insert([]map[string]interface{}{
		{"username": marker, "status": "OFFLINE"},
	}, nil).Execute(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = testClient.From("users").Delete(nil).Eq("username", marker).Execute(context.Background())
	})
	_, err = testClient.From("messages").Insert([]map[string]interface{}{
		{"message": "offline probe", "username": marker, "channel_id": 1},
	}, nil).Execute(ctx)
	require.NoError(t, err)

	req, err := http.NewRequest("GET", testTS.URL+
		"/rest/v1/messages?select=count(),users!inner(status)&users.status=eq.ONLINE", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+testAdminKey)
	req.Header.Set("apikey", testAdminKey)
	req.Header.Set("Prefer", "count=exact")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, 206, resp.StatusCode, string(body))

	var rows []map[string]any
	require.NoError(t, json.Unmarshal(body, &rows), string(body))
	require.Len(t, rows, 1, string(body))
	assert.EqualValues(t, 2, rows[0]["count"], "one group, 2 rows in it")
	assert.Equal(t, map[string]any{"status": "ONLINE"}, rows[0]["users"])
	assert.Equal(t, "0-0/2", resp.Header.Get("Content-Range"),
		"count is the 2 filtered rows, not the 1 group and not the 3 unfiltered rows")
}

func TestConf_RPCInnerEmbedCountMatchesRows(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}
	exact := map[string]string{"Prefer": "count=exact"}
	total := func(hdr http.Header) string {
		t.Helper()
		cr := hdr.Get("Content-Range")
		i := strings.Index(cr, "/")
		require.GreaterOrEqual(t, i, 0, "Content-Range %q has no total", cr)
		return cr[i+1:]
	}
	row, err := testDB.QueryRow(context.Background(), "SELECT count(*)::int AS n FROM messages")
	require.NoError(t, err)
	all := int(row["n"].(int32))
	require.Greater(t, all, 1, "needs the ghost row plus at least one real row")

	status, raw, parsed, hdr := rpcPOST(t, "messages_with_ghost?select=id,users!inner(username)", `{}`, exact)
	require.Contains(t, []int{200, 206}, status, string(raw))
	rows := parsed.([]any)
	require.Len(t, rows, all-1, "inner embed drops exactly the ghost row: %s", raw)
	for _, r := range rows {
		require.NotNil(t, r.(map[string]any)["users"], "inner embed drops the ghost row: %s", raw)
	}
	assert.Equal(t, fmt.Sprintf("0-%d/%d", all-2, all-1), hdr.Get("Content-Range"), "count drops what the page drops")

	status, raw, parsed, hdr = rpcPOST(t, "messages_with_ghost?select=n:id.count(),users!inner(status)", `{}`, exact)
	require.Contains(t, []int{200, 206}, status, string(raw))
	sum := 0.0
	for _, r := range parsed.([]any) {
		sum += r.(map[string]any)["n"].(float64)
	}
	assert.Equal(t, float64(all-1), sum, "groups drop the ghost row")
	assert.Equal(t, fmt.Sprint(all-1), total(hdr), "ungrouped count = sum of group counts")

	status, raw, parsed, _ = rpcPOST(t, "messages_with_ghost?select=n:id.count(),users(status)", `{}`, nil)
	require.Equal(t, 200, status, string(raw))
	var sawNull bool
	for _, r := range parsed.([]any) {
		sawNull = sawNull || r.(map[string]any)["users"] == nil
	}
	assert.True(t, sawNull, "left embed keeps the ghost row in a null group: %s", raw)
}

func TestConf_RPCHasManyInnerAndAliases(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}
	exact := map[string]string{"Prefer": "count=exact"}
	status, raw, parsed, hdr := rpcPOST(t, "users_by_status?select=username,messages!inner(id)", `{"target":"ONLINE"}`, exact)
	require.Equal(t, 200, status, string(raw))
	rows := parsed.([]any)
	require.Len(t, rows, 1, "only supabot has messages: %s", raw)
	assert.Equal(t, "supabot", rows[0].(map[string]any)["username"])
	assert.Equal(t, "0-0/1", hdr.Get("Content-Range"), "count drops parents with no children")

	status, raw, parsed, _ = rpcPOST(t, "users_by_status?select=n:username.count(),messages!inner(id)", `{"target":"ONLINE"}`, nil)
	require.Equal(t, 200, status, string(raw))
	require.Len(t, parsed.([]any), 1, string(raw))
	assert.Equal(t, float64(1), parsed.([]any)[0].(map[string]any)["n"], "aggregate skips dropped parents")

	status, raw, parsed, _ = rpcPOST(t, "users_by_status?select=username,m:messages(id)&username=eq.supabot", `{"target":"ONLINE"}`, nil)
	require.Equal(t, 200, status, string(raw))
	row := parsed.([]any)[0].(map[string]any)
	assert.Len(t, row["m"], 2, string(raw))
	assert.NotContains(t, row, "messages")

	status, raw, parsed, _ = rpcPOST(t, "messages_with_ghost?select=id,author:users(username),who:users(status)&order=id", `{}`, nil)
	require.Equal(t, 200, status, string(raw))
	for i, r := range parsed.([]any) {
		m := r.(map[string]any)
		assert.NotContains(t, m, "users")
		if i == 0 {
			assert.Nil(t, m["author"], "ghost row has no author: %s", raw)
			continue
		}
		assert.Equal(t, map[string]any{"username": "supabot"}, m["author"], string(raw))
		assert.Equal(t, map[string]any{"status": "ONLINE"}, m["who"], string(raw))
	}
}
