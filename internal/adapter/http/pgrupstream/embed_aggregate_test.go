//go:build integration

package pgrupstream

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

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

	t.Run("count=exact counts groups", func(t *testing.T) {
		req, err := http.NewRequest("GET", testTS.URL+"/rest/v1/users?select=count(),messages(id)", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		req.Header.Set("apikey", testAdminKey)
		req.Header.Set("Prefer", "count=exact")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		require.Equal(t, 200, resp.StatusCode, string(body))
		assert.Equal(t, "0-1/2", resp.Header.Get("Content-Range"))
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
	status, raw, parsed := rpcPOST(t, "users_by_status", `{"target":"NOBODY"}`, map[string]string{
		"Accept": "application/vnd.pgrst.object+json",
		"Prefer": "count=exact",
	})
	require.Equal(t, 406, status, string(raw))
	m, ok := parsed.(map[string]any)
	require.True(t, ok, string(raw))
	assert.Equal(t, "PGRST116", m["code"])
	assert.Contains(t, m["details"], "0 rows")
	assert.False(t, strings.Contains(string(raw), "__inz"), "helper columns leaked: %s", raw)

	status, raw, parsed = rpcPOST(t, "users_by_status", `{"target":"ONLINE"}`, map[string]string{
		"Accept": "application/vnd.pgrst.object+json",
		"Prefer": "count=exact",
	})
	require.Equal(t, 406, status, string(raw))
	assert.Equal(t, "PGRST116", parsed.(map[string]any)["code"])

	status, raw, _ = rpcPOST(t, "users_by_status?username=eq.supabot", `{"target":"ONLINE"}`, map[string]string{
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
