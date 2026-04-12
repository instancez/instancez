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

	. "github.com/supabase-community/postgrest-go"
)

// unmarshalRows converts any response.Data that represents a row array into
// a []map[string]interface{}, handling both bare-object and array shapes.
func unmarshalRows(t *testing.T, data interface{}) []map[string]interface{} {
	t.Helper()
	b, err := json.Marshal(data)
	require.NoError(t, err)
	var arr []map[string]interface{}
	if err := json.Unmarshal(b, &arr); err == nil {
		return arr
	}
	var one map[string]interface{}
	if err := json.Unmarshal(b, &one); err == nil {
		return []map[string]interface{}{one}
	}
	t.Fatalf("unmarshalRows: cannot parse %v", data)
	return nil
}

// errorBody issues a raw HTTP request and decodes the response body as a
// PostgREST error envelope. It asserts the four canonical fields are all
// present (even if empty) so we lock in the shape, and returns the parsed
// map plus status so callers can check code/message/details specifics.
func errorBody(t *testing.T, req *http.Request) (int, map[string]interface{}) {
	t.Helper()
	req.Header.Set("Authorization", "Bearer "+testAdminKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &body), "body: %s", string(raw))
	// All four PostgREST error keys must always be present.
	for _, k := range []string{"code", "message", "details", "hint"} {
		_, ok := body[k]
		assert.True(t, ok, "missing %q key in error body: %s", k, string(raw))
	}
	return resp.StatusCode, body
}

func TestConf_Errors(t *testing.T) {
	if testClient == nil {
		t.Skip("no client")
	}

	t.Run("unknown column in filter", func(t *testing.T) {
		req, err := http.NewRequest("GET",
			testTS.URL+"/rest/v1/users?notacolumn=eq.1", nil)
		require.NoError(t, err)
		status, body := errorBody(t, req)
		assert.GreaterOrEqual(t, status, 400)
		assert.Less(t, status, 500)
		assert.NotEmpty(t, body["code"])
		assert.NotEmpty(t, body["message"])
		assert.Contains(t, strings.ToLower(body["message"].(string)), "notacolumn")
	})

	t.Run("unknown operator", func(t *testing.T) {
		req, err := http.NewRequest("GET",
			testTS.URL+"/rest/v1/users?age=wat.25", nil)
		require.NoError(t, err)
		status, body := errorBody(t, req)
		assert.Equal(t, 400, status)
		assert.Equal(t, "PGRST100", body["code"])
		assert.NotEmpty(t, body["message"])
	})

	t.Run("malformed JSON body on POST", func(t *testing.T) {
		req, err := http.NewRequest("POST",
			testTS.URL+"/rest/v1/users", strings.NewReader(`{"username": "x",`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		status, body := errorBody(t, req)
		assert.Equal(t, 400, status)
		assert.Equal(t, "PGRST100", body["code"])
		assert.NotEmpty(t, body["message"])
	})

	t.Run("unknown field in insert body", func(t *testing.T) {
		req, err := http.NewRequest("POST", testTS.URL+"/rest/v1/users",
			strings.NewReader(`{"username":"x","notafield":1}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		status, body := errorBody(t, req)
		assert.Equal(t, 400, status)
		assert.Equal(t, "PGRST100", body["code"])
		assert.Contains(t, strings.ToLower(body["message"].(string)), "notafield")
	})

	t.Run("single with >1 rows errors", func(t *testing.T) {
		// status=ONLINE matches multiple seed rows; Single() requires exactly 1.
		req, err := http.NewRequest("GET",
			testTS.URL+"/rest/v1/users?select=username&status=eq.ONLINE", nil)
		require.NoError(t, err)
		req.Header.Set("Accept", "application/vnd.pgrst.object+json")
		status, body := errorBody(t, req)
		assert.Equal(t, 406, status)
		assert.Equal(t, "PGRST116", body["code"])
		// details should carry the actual row count.
		assert.Contains(t, strings.ToLower(body["details"].(string)), "rows")
	})

	t.Run("unique violation -> 23505 body shape", func(t *testing.T) {
		// "supabot" is a seed row — inserting it again triggers pk conflict.
		req, err := http.NewRequest("POST", testTS.URL+"/rest/v1/users",
			strings.NewReader(`{"username":"supabot","status":"ONLINE"}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		status, body := errorBody(t, req)
		assert.Equal(t, 409, status)
		assert.Equal(t, "23505", body["code"])
		assert.NotEmpty(t, body["message"])
		// Postgres sets Detail on unique violations ("Key (username)=(supabot) already exists.")
		assert.NotEmpty(t, body["details"])
	})

	t.Run("foreign key violation -> 23503 body shape", func(t *testing.T) {
		// messages.username references users.username — this user does not exist.
		uname := fmt.Sprintf("nobody_%d", time.Now().UnixNano())
		payload := fmt.Sprintf(`{"message":"orphan","username":%q,"channel_id":1}`, uname)
		req, err := http.NewRequest("POST", testTS.URL+"/rest/v1/messages",
			strings.NewReader(payload))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		status, body := errorBody(t, req)
		assert.Equal(t, 422, status)
		assert.Equal(t, "23503", body["code"])
		assert.NotEmpty(t, body["message"])
		assert.NotEmpty(t, body["details"])
	})

	t.Run("not-null violation -> 23502 body shape", func(t *testing.T) {
		// messages.username is NOT NULL — omit it entirely.
		req, err := http.NewRequest("POST", testTS.URL+"/rest/v1/messages",
			strings.NewReader(`{"message":"no owner","channel_id":1}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		status, body := errorBody(t, req)
		assert.Equal(t, 422, status)
		assert.Equal(t, "23502", body["code"])
		assert.NotEmpty(t, body["message"])
	})
}

// Conformance tests inspired by
// https://github.com/supabase/supabase-js/tree/master/packages/core/postgrest-js
// and the postgrest-conformance-tests suite. Only cases that map onto
// ultrabase's existing feature set and the users/channels/messages seed are
// covered — RPC, views, ranges, tsvector, PostGIS, multi-schema, polymorphic
// funcs, EXPLAIN, abort and geojson are intentionally skipped.

func TestConf_Filters(t *testing.T) {
	if testClient == nil {
		t.Skip("no client")
	}
	ctx := context.Background()

	t.Run("like", func(t *testing.T) {
		resp, err := testClient.From("users").Select("username", nil).
			Like("username", "kiwi%").Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		require.Equal(t, 1, len(rows))
		assert.Equal(t, "kiwicopple", rows[0]["username"])
	})

	t.Run("not.eq", func(t *testing.T) {
		resp, err := testClient.From("users").Select("username", nil).
			Not("username", "eq", "supabot").Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		for _, r := range rows {
			assert.NotEqual(t, "supabot", r["username"])
		}
	})

	t.Run("not.like", func(t *testing.T) {
		resp, err := testClient.From("users").Select("username", nil).
			Not("username", "like", "kiwi%").Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		for _, r := range rows {
			assert.NotEqual(t, "kiwicopple", r["username"])
		}
	})

	t.Run("in", func(t *testing.T) {
		resp, err := testClient.From("users").Select("username", nil).
			In("username", []interface{}{"supabot", "kiwicopple"}).Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		require.Equal(t, 2, len(rows))
	})

	t.Run("not.in", func(t *testing.T) {
		// postgrest-go's Not() formats as "not.<op>.<value>"; wrap the
		// IN list manually since In() doesn't compose with Not().
		resp, err := testClient.From("users").Select("username", nil).
			Not("username", "in", "(supabot,kiwicopple)").Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		for _, r := range rows {
			u := r["username"]
			assert.NotEqual(t, "supabot", u)
			assert.NotEqual(t, "kiwicopple", u)
		}
	})

	t.Run("contained-by on array", func(t *testing.T) {
		resp, err := testClient.From("users").Select("username,tags", nil).
			ContainedBy("tags", []interface{}{"admin", "founder", "bot", "contrib", "speaker"}).
			Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		// All seed rows have tags drawn from this superset.
		assert.Equal(t, 5, len(rows))
	})

	t.Run("multiple filters stacked", func(t *testing.T) {
		resp, err := testClient.From("users").Select("username,status,age", nil).
			Eq("status", "ONLINE").
			Gte("age", 25).
			Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		assert.Greater(t, len(rows), 0)
		for _, r := range rows {
			assert.Equal(t, "ONLINE", r["status"])
			if v, ok := r["age"].(float64); ok {
				assert.GreaterOrEqual(t, v, 25.0)
			}
		}
	})

	t.Run("filter generic form", func(t *testing.T) {
		resp, err := testClient.From("users").Select("username", nil).
			Filter("username", "eq", "supabot").Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		require.Equal(t, 1, len(rows))
	})

	t.Run("or", func(t *testing.T) {
		resp, err := testClient.From("users").Select("username", nil).
			Or("username.eq.supabot,username.eq.dragarcia", nil).Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		assert.Equal(t, 2, len(rows))
	})

	t.Run("neq", func(t *testing.T) {
		resp, err := testClient.From("users").Select("username", nil).
			Neq("username", "supabot").Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		for _, r := range rows {
			assert.NotEqual(t, "supabot", r["username"])
		}
	})

	t.Run("gt/gte/lt/lte on age", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			fn   func(*FilterBuilder[[]map[string]interface{}]) *FilterBuilder[[]map[string]interface{}]
			min  float64
		}{
			{"gt 25", func(f *FilterBuilder[[]map[string]interface{}]) *FilterBuilder[[]map[string]interface{}] {
				return f.Gt("age", 25)
			}, 26},
			{"gte 25", func(f *FilterBuilder[[]map[string]interface{}]) *FilterBuilder[[]map[string]interface{}] {
				return f.Gte("age", 25)
			}, 25},
		} {
			t.Run(tc.name, func(t *testing.T) {
				fb := testClient.From("users").Select("username,age", nil)
				resp, err := tc.fn(fb).Execute(ctx)
				require.NoError(t, err)
				require.Nil(t, resp.Error)
				rows := unmarshalRows(t, resp.Data)
				assert.Greater(t, len(rows), 0)
				for _, r := range rows {
					if v, ok := r["age"].(float64); ok {
						assert.GreaterOrEqual(t, v, tc.min)
					}
				}
			})
		}

		resp, err := testClient.From("users").Select("username,age", nil).
			Lt("age", 28).Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		for _, r := range rows {
			if v, ok := r["age"].(float64); ok {
				assert.Less(t, v, 28.0)
			}
		}
	})

	t.Run("ilike", func(t *testing.T) {
		resp, err := testClient.From("users").Select("username", nil).
			Ilike("username", "%BOT%").Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		assert.Equal(t, 1, len(rows))
	})

	t.Run("is null", func(t *testing.T) {
		resp, err := testClient.From("users").Select("username,nickname", nil).
			Is("nickname", "null").Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		assert.Greater(t, len(rows), 0)
		for _, r := range rows {
			assert.Nil(t, r["nickname"])
		}
	})

	t.Run("contains on array (cs)", func(t *testing.T) {
		resp, err := testClient.From("users").Select("username,tags", nil).
			Contains("tags", []interface{}{"admin"}).Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		assert.Greater(t, len(rows), 0)
	})

	t.Run("overlaps on array (ov)", func(t *testing.T) {
		resp, err := testClient.From("users").Select("username,tags", nil).
			Overlaps("tags", []interface{}{"admin", "bot"}).Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		assert.GreaterOrEqual(t, len(rows), 2)
	})

	t.Run("match (multi-eq shorthand)", func(t *testing.T) {
		resp, err := testClient.From("users").Select("*", nil).
			Match(map[string]interface{}{
				"status":   "ONLINE",
				"username": "supabot",
			}).Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		assert.Equal(t, 1, len(rows))
	})
}

func TestConf_Transforms(t *testing.T) {
	if testClient == nil {
		t.Skip("no client")
	}
	ctx := context.Background()

	t.Run("order asc", func(t *testing.T) {
		resp, err := testClient.From("users").Select("username", nil).
			Order("username", &OrderOptions{Ascending: true}).Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		require.Greater(t, len(rows), 1)
		for i := 1; i < len(rows); i++ {
			assert.LessOrEqual(t,
				rows[i-1]["username"].(string),
				rows[i]["username"].(string),
			)
		}
	})

	t.Run("order desc", func(t *testing.T) {
		resp, err := testClient.From("users").Select("username", nil).
			Order("username", &OrderOptions{Ascending: false}).Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		require.Greater(t, len(rows), 1)
		for i := 1; i < len(rows); i++ {
			assert.GreaterOrEqual(t,
				rows[i-1]["username"].(string),
				rows[i]["username"].(string),
			)
		}
	})

	t.Run("order on multiple columns", func(t *testing.T) {
		resp, err := testClient.From("users").Select("username,status,age", nil).
			Order("status", &OrderOptions{Ascending: true}).
			Order("age", &OrderOptions{Ascending: false}).
			Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		require.Greater(t, len(rows), 1)
		// Within each status group, ages must be non-increasing.
		for i := 1; i < len(rows); i++ {
			if rows[i-1]["status"] == rows[i]["status"] {
				a := rows[i-1]["age"].(float64)
				b := rows[i]["age"].(float64)
				assert.GreaterOrEqual(t, a, b)
			}
		}
	})

	t.Run("order nullsfirst", func(t *testing.T) {
		nullsFirst := true
		resp, err := testClient.From("users").Select("username,nickname", nil).
			Order("nickname", &OrderOptions{Ascending: true, NullsFirst: &nullsFirst}).
			Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		require.Greater(t, len(rows), 0)
		// Nulls must come before any non-null nickname.
		sawNonNull := false
		for _, r := range rows {
			if r["nickname"] == nil {
				assert.False(t, sawNonNull, "null nickname after non-null")
			} else {
				sawNonNull = true
			}
		}
	})

	t.Run("limit", func(t *testing.T) {
		resp, err := testClient.From("users").Select("username", nil).
			Order("username", &OrderOptions{Ascending: true}).
			Limit(2, nil).Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		assert.Equal(t, 2, len(rows))
	})

	t.Run("range inclusive", func(t *testing.T) {
		resp, err := testClient.From("users").Select("username", nil).
			Order("username", &OrderOptions{Ascending: true}).
			Range(1, 3, nil).Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		// rows 1..3 inclusive → 3 entries.
		assert.Equal(t, 3, len(rows))
	})

	t.Run("single returns bare object", func(t *testing.T) {
		single := testClient.From("users").Select("username", nil).
			Eq("username", "supabot").Single()
		resp, err := single.Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error, "error: %v", resp.Error)
		// Single should populate Data as one object, not an array.
		one := unmarshalRows(t, resp.Data)
		require.Equal(t, 1, len(one))
		assert.Equal(t, "supabot", one[0]["username"])
	})

	t.Run("single with zero rows errors", func(t *testing.T) {
		resp, err := testClient.From("users").Select("username", nil).
			Eq("username", "does_not_exist").Single().Execute(ctx)
		// postgrest-go may surface the 404 as a top-level err or as
		// resp.Error depending on version; accept either.
		hasErr := err != nil || (resp != nil && resp.Error != nil)
		assert.True(t, hasErr, "expected error for empty Single")
	})

	t.Run("maybeSingle with zero rows no error", func(t *testing.T) {
		resp, err := testClient.From("users").Select("username", nil).
			Eq("username", "does_not_exist").MaybeSingle().Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error, "MaybeSingle must not error on empty set")
	})

	t.Run("csv export", func(t *testing.T) {
		// postgrest-go's CSV() helper swaps the builder's T to string; easier
		// to issue a raw HTTP request with Accept: text/csv.
		req, err := http.NewRequest("GET",
			testTS.URL+"/rest/v1/users?select=username,status&order=username.asc", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		req.Header.Set("Accept", "text/csv")
		httpResp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer httpResp.Body.Close()
		assert.Equal(t, 200, httpResp.StatusCode)
		assert.Contains(t, httpResp.Header.Get("Content-Type"), "text/csv")
		body, err := io.ReadAll(httpResp.Body)
		require.NoError(t, err)
		text := string(body)
		// Header row plus data rows.
		assert.Contains(t, text, "username")
		assert.Contains(t, text, "status")
		assert.Contains(t, text, "supabot")
		lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
		assert.GreaterOrEqual(t, len(lines), 2)
	})
}

func TestConf_Embedding(t *testing.T) {
	if testClient == nil {
		t.Skip("no client")
	}
	ctx := context.Background()

	t.Run("belongs-to (messages → users)", func(t *testing.T) {
		resp, err := testClient.From("messages").
			Select("id,message,users(username)", nil).
			Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error, "error: %v", resp.Error)
		rows := unmarshalRows(t, resp.Data)
		require.Greater(t, len(rows), 0)
		for _, r := range rows {
			u, ok := r["users"].(map[string]interface{})
			require.True(t, ok, "users embed not an object: %T", r["users"])
			_, hasUsername := u["username"]
			assert.True(t, hasUsername)
		}
	})

	t.Run("belongs-to with parent filter", func(t *testing.T) {
		resp, err := testClient.From("messages").
			Select("id,users(username,status)", nil).
			Eq("channel_id", 1).
			Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error, "error: %v", resp.Error)
		rows := unmarshalRows(t, resp.Data)
		require.Greater(t, len(rows), 0)
		for _, r := range rows {
			u, ok := r["users"].(map[string]interface{})
			require.True(t, ok)
			assert.NotEmpty(t, u["username"])
		}
	})

	t.Run("has-many with parent limit", func(t *testing.T) {
		resp, err := testClient.From("users").
			Select("username, messages(id)", nil).
			Order("username", &OrderOptions{Ascending: true}).
			Limit(2, nil).
			Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error, "error: %v", resp.Error)
		rows := unmarshalRows(t, resp.Data)
		require.Equal(t, 2, len(rows))
		for _, r := range rows {
			_, ok := r["messages"].([]interface{})
			assert.True(t, ok, "messages embed not an array: %T", r["messages"])
		}
	})

	t.Run("has-many with count=exact", func(t *testing.T) {
		resp, err := testClient.From("users").
			Select("username, messages(id)", &SelectOptions{Count: "exact"}).
			Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error, "error: %v", resp.Error)
		require.NotNil(t, resp.Count)
		assert.Greater(t, *resp.Count, int64(0))
	})
}

func TestConf_NestedEmbedding(t *testing.T) {
	if testClient == nil {
		t.Skip("no client")
	}

	t.Run("has-many with nested belongs-to (channels → messages → users)", func(t *testing.T) {
		// channels → messages(message, users(username))
		rows := fetchRows(t, "/rest/v1/channels?select=slug,messages(message,users(username))&order=id.asc")
		require.Greater(t, len(rows), 0)
		for _, r := range rows {
			msgs, ok := r["messages"].([]interface{})
			require.True(t, ok, "messages should be array, got %T", r["messages"])
			for _, m := range msgs {
				msg := m.(map[string]interface{})
				assert.NotEmpty(t, msg["message"])
				u, ok := msg["users"].(map[string]interface{})
				require.True(t, ok, "nested users should be object, got %T", msg["users"])
				assert.NotEmpty(t, u["username"])
			}
		}
	})

	t.Run("belongs-to with nested has-many (messages → users → messages)", func(t *testing.T) {
		// messages → users(username, messages(message))
		rows := fetchRows(t, "/rest/v1/messages?select=id,users(username,messages(message))&order=id.asc")
		require.Greater(t, len(rows), 0)
		for _, r := range rows {
			u, ok := r["users"].(map[string]interface{})
			require.True(t, ok, "users should be object, got %T", r["users"])
			assert.NotEmpty(t, u["username"])
			innerMsgs, ok := u["messages"].([]interface{})
			require.True(t, ok, "nested messages should be array, got %T", u["messages"])
			require.Greater(t, len(innerMsgs), 0, "user should have at least one message")
		}
	})

	t.Run("spread belongs-to (messages → ...users)", func(t *testing.T) {
		// messages → ...users(username) — username should be inlined into the parent row.
		rows := fetchRows(t, "/rest/v1/messages?select=id,...users(username)&order=id.asc")
		require.Greater(t, len(rows), 0)
		for _, r := range rows {
			assert.NotNil(t, r["id"])
			_, hasUsername := r["username"]
			assert.True(t, hasUsername, "spread should inline username into parent row: %v", r)
			_, hasUsers := r["users"]
			assert.False(t, hasUsers, "spread should not have nested users key: %v", r)
		}
	})

	t.Run("spread on has-many is rejected", func(t *testing.T) {
		req, err := http.NewRequest("GET", testTS.URL+"/rest/v1/users?select=username,...messages(*)", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 400, resp.StatusCode)
	})
}

func TestConf_Mutations(t *testing.T) {
	if testClient == nil {
		t.Skip("no client")
	}
	ctx := context.Background()

	t.Run("insert then select=*", func(t *testing.T) {
		uname := fmt.Sprintf("mut_ins_%d", nowNano())
		resp, err := testClient.From("users").Insert(
			map[string]interface{}{"username": uname, "status": "ONLINE"}, nil,
		).Select("*").Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error, "error: %v", resp.Error)
		rows := unmarshalRows(t, resp.Data)
		require.Equal(t, 1, len(rows))
		assert.Equal(t, uname, rows[0]["username"])

		_, _ = testClient.From("users").Delete(nil).Eq("username", uname).Execute(ctx)
	})

	t.Run("update then select=*", func(t *testing.T) {
		uname := fmt.Sprintf("mut_upd_%d", nowNano())
		_, err := testClient.From("users").Insert(
			map[string]interface{}{"username": uname, "status": "ONLINE"}, nil,
		).Execute(ctx)
		require.NoError(t, err)

		resp, err := testClient.From("users").
			Update(map[string]interface{}{"status": "OFFLINE"}, nil).
			Eq("username", uname).
			Select("*").
			Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		require.Equal(t, 1, len(rows))
		assert.Equal(t, "OFFLINE", rows[0]["status"])

		_, _ = testClient.From("users").Delete(nil).Eq("username", uname).Execute(ctx)
	})

	t.Run("delete then select=*", func(t *testing.T) {
		uname := fmt.Sprintf("mut_del_%d", nowNano())
		_, err := testClient.From("users").Insert(
			map[string]interface{}{"username": uname, "status": "ONLINE"}, nil,
		).Execute(ctx)
		require.NoError(t, err)

		resp, err := testClient.From("users").Delete(nil).
			Eq("username", uname).
			Select("*").
			Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		rows := unmarshalRows(t, resp.Data)
		require.Equal(t, 1, len(rows))
		assert.Equal(t, uname, rows[0]["username"])
	})

	t.Run("return=minimal yields empty body", func(t *testing.T) {
		uname := fmt.Sprintf("mut_min_%d", nowNano())
		body := fmt.Sprintf(`{"username":%q,"status":"ONLINE"}`, uname)

		req, err := http.NewRequest("POST", testTS.URL+"/rest/v1/users", strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Prefer", "return=minimal")
		httpResp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer httpResp.Body.Close()
		assert.Equal(t, 201, httpResp.StatusCode)
		buf, err := io.ReadAll(httpResp.Body)
		require.NoError(t, err)
		assert.Empty(t, strings.TrimSpace(string(buf)))

		_, _ = testClient.From("users").Delete(nil).Eq("username", uname).Execute(ctx)
	})

	t.Run("GET with count=exact populates Count", func(t *testing.T) {
		resp, err := testClient.From("users").
			Select("username", &SelectOptions{Count: "exact"}).
			Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		require.NotNil(t, resp.Count)
		assert.Greater(t, *resp.Count, int64(0))
	})

	t.Run("HEAD with count=exact populates Count", func(t *testing.T) {
		resp, err := testClient.From("users").
			Select("*", &SelectOptions{Head: true, Count: "exact"}).Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error)
		require.NotNil(t, resp.Count)
		assert.Greater(t, *resp.Count, int64(0))
	})

	t.Run("bulk insert", func(t *testing.T) {
		prefix := fmt.Sprintf("bulk_%d_", time.Now().UnixNano())
		values := []map[string]interface{}{
			{"username": prefix + "a", "status": "ONLINE"},
			{"username": prefix + "b", "status": "OFFLINE"},
			{"username": prefix + "c", "status": "ONLINE"},
		}

		resp, err := testClient.From("users").Insert(values, nil).Select("*").Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error, "error: %v", resp.Error)
		rows := unmarshalRows(t, resp.Data)
		require.Equal(t, 3, len(rows), "expected 3 inserted rows")

		_, _ = testClient.From("users").Delete(nil).
			In("username", []interface{}{prefix + "a", prefix + "b", prefix + "c"}).Execute(ctx)
	})

	t.Run("bulk update and delete by filter", func(t *testing.T) {
		marker := fmt.Sprintf("mark_%d", time.Now().UnixNano())
		values := []map[string]interface{}{
			{"username": marker + "_1", "status": "ONLINE", "nickname": marker},
			{"username": marker + "_2", "status": "ONLINE", "nickname": marker},
			{"username": marker + "_3", "status": "ONLINE", "nickname": marker},
		}
		_, err := testClient.From("users").Insert(values, nil).Execute(ctx)
		require.NoError(t, err)

		upd, err := testClient.From("users").
			Update(map[string]interface{}{"status": "OFFLINE"}, nil).
			Eq("nickname", marker).
			Select("*").
			Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, upd.Error)
		updRows := unmarshalRows(t, upd.Data)
		assert.Equal(t, 3, len(updRows))
		for _, r := range updRows {
			assert.Equal(t, "OFFLINE", r["status"])
		}

		del, err := testClient.From("users").Delete(&DeleteOptions{Count: "exact"}).
			Eq("nickname", marker).Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, del.Error)

		check, err := testClient.From("users").Select("username", nil).
			Eq("nickname", marker).Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, check.Error)
		assert.Equal(t, 0, len(unmarshalRows(t, check.Data)))
	})

	t.Run("upsert inserts then updates", func(t *testing.T) {
		uname := fmt.Sprintf("upsert_%d", time.Now().UnixNano())

		up1, err := testClient.From("users").Upsert(
			map[string]interface{}{"username": uname, "status": "ONLINE"},
			&UpsertOptions{OnConflict: "username"},
		).Select("*").Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, up1.Error, "error: %v", up1.Error)
		rows := unmarshalRows(t, up1.Data)
		require.Equal(t, 1, len(rows))
		assert.Equal(t, "ONLINE", rows[0]["status"])

		up2, err := testClient.From("users").Upsert(
			map[string]interface{}{"username": uname, "status": "OFFLINE"},
			&UpsertOptions{OnConflict: "username"},
		).Select("*").Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, up2.Error, "error: %v", up2.Error)
		rows2 := unmarshalRows(t, up2.Data)
		require.Equal(t, 1, len(rows2))
		assert.Equal(t, "OFFLINE", rows2[0]["status"])

		_, _ = testClient.From("users").Delete(nil).Eq("username", uname).Execute(ctx)
	})

	t.Run("tx=rollback dry-run insert", func(t *testing.T) {
		uname := fmt.Sprintf("rollback_%d", time.Now().UnixNano())
		body := fmt.Sprintf(`{"username":%q,"status":"ONLINE"}`, uname)

		req, err := http.NewRequest("POST", testTS.URL+"/rest/v1/users", strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Add("Prefer", "return=representation")
		req.Header.Add("Prefer", "tx=rollback")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 201, resp.StatusCode)

		check, err := testClient.From("users").Select("username", nil).
			Eq("username", uname).Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, check.Error)
		assert.Equal(t, 0, len(unmarshalRows(t, check.Data)))
	})
}

// TestConf_RangeOperators exercises the PostgREST range operators (sl, sr,
// nxl, nxr, adj) against the channels.active_during int4range column. The
// seed has two rows: '[1,10)' and '[20,30)'. postgrest-go has no dedicated
// helper for these ops, so we drive them via Filter().
func TestConf_RangeOperators(t *testing.T) {
	if testClient == nil {
		t.Skip("no client")
	}
	ctx := context.Background()

	// '[1,10)' is strictly left of '[20,30)'.
	t.Run("sl (strictly left of)", func(t *testing.T) {
		resp, err := testClient.From("channels").Select("slug", nil).
			Filter("active_during", "sl", "(15,40)").Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error, "error: %v", resp.Error)
		rows := unmarshalRows(t, resp.Data)
		require.Equal(t, 1, len(rows))
		assert.Equal(t, "public", rows[0]["slug"])
	})

	t.Run("sr (strictly right of)", func(t *testing.T) {
		resp, err := testClient.From("channels").Select("slug", nil).
			Filter("active_during", "sr", "(0,15)").Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error, "error: %v", resp.Error)
		rows := unmarshalRows(t, resp.Data)
		require.Equal(t, 1, len(rows))
		assert.Equal(t, "random", rows[0]["slug"])
	})

	t.Run("nxr (does not extend to the right of)", func(t *testing.T) {
		// '[1,10)' does not extend past 15; '[20,30)' does.
		resp, err := testClient.From("channels").Select("slug", nil).
			Filter("active_during", "nxr", "(0,15)").Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error, "error: %v", resp.Error)
		rows := unmarshalRows(t, resp.Data)
		require.Equal(t, 1, len(rows))
		assert.Equal(t, "public", rows[0]["slug"])
	})

	t.Run("nxl (does not extend to the left of)", func(t *testing.T) {
		// '[20,30)' does not extend left of 15; '[1,10)' does.
		resp, err := testClient.From("channels").Select("slug", nil).
			Filter("active_during", "nxl", "(15,40)").Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error, "error: %v", resp.Error)
		rows := unmarshalRows(t, resp.Data)
		require.Equal(t, 1, len(rows))
		assert.Equal(t, "random", rows[0]["slug"])
	})

	t.Run("adj (adjacent)", func(t *testing.T) {
		// '[10,20)' is adjacent to both '[1,10)' and '[20,30)'.
		resp, err := testClient.From("channels").Select("slug", nil).
			Filter("active_during", "adj", "[10,20)").
			Order("slug", &OrderOptions{Ascending: true}).
			Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, resp.Error, "error: %v", resp.Error)
		rows := unmarshalRows(t, resp.Data)
		require.Equal(t, 2, len(rows))
		assert.Equal(t, "public", rows[0]["slug"])
		assert.Equal(t, "random", rows[1]["slug"])
	})
}

// fetchRows issues a raw GET with admin auth and decodes the JSON array
// response. Used for filters postgrest-go does not expose (like(all) /
// like(any) / ilike(*)), where we need to craft the URL ourselves.
func fetchRows(t *testing.T, path string) []map[string]interface{} {
	t.Helper()
	req, err := http.NewRequest("GET", testTS.URL+path, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+testAdminKey)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode, "body: %s", string(raw))
	var rows []map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &rows), "body: %s", string(raw))
	return rows
}

// TestConf_LikeAllAny exercises like(all), like(any), ilike(all), ilike(any).
// postgrest-go's Filter() helper rejects these ops (its whitelist only knows
// the base like/ilike), so we drive the server with raw HTTP. We URL-encode
// the "%" characters as "%25" so Go's query parser does not mistake them for
// percent-escapes.
func TestConf_LikeAllAny(t *testing.T) {
	if testClient == nil {
		t.Skip("no client")
	}

	// enc encodes a raw pattern's '%' wildcards for safe URL transport.
	enc := func(s string) string { return strings.ReplaceAll(s, "%", "%25") }

	t.Run("like(any) matches either pattern", func(t *testing.T) {
		// "supabot" matches %bot, "kiwicopple" matches %copple.
		path := "/rest/v1/users?select=username&order=username.asc" +
			"&username=like(any)." + enc("{%bot,%copple}")
		rows := fetchRows(t, path)
		got := make([]string, 0, len(rows))
		for _, r := range rows {
			got = append(got, r["username"].(string))
		}
		assert.Equal(t, []string{"kiwicopple", "supabot"}, got)
	})

	t.Run("like(all) requires every pattern to match", func(t *testing.T) {
		// Only "kiwicopple" starts with k AND ends with e.
		path := "/rest/v1/users?select=username&username=like(all)." + enc("{k%,%e}")
		rows := fetchRows(t, path)
		require.Equal(t, 1, len(rows))
		assert.Equal(t, "kiwicopple", rows[0]["username"])
	})

	t.Run("ilike(any) is case-insensitive", func(t *testing.T) {
		path := "/rest/v1/users?select=username&order=username.asc" +
			"&username=ilike(any)." + enc("{%BOT,%COPPLE}")
		rows := fetchRows(t, path)
		require.Equal(t, 2, len(rows))
	})

	t.Run("ilike(all) is case-insensitive", func(t *testing.T) {
		path := "/rest/v1/users?select=username&username=ilike(all)." + enc("{K%,%E}")
		rows := fetchRows(t, path)
		require.Equal(t, 1, len(rows))
		assert.Equal(t, "kiwicopple", rows[0]["username"])
	})

	t.Run("not.like(any) negates", func(t *testing.T) {
		// Drive through postgrest-go's Not() which does not validate the
		// inner operator — handy for exercising the client-side negation
		// wrapping on top of our server-side parser.
		resp, err := testClient.From("users").Select("username", nil).
			Not("username", "like(any)", "{%bot,%copple}").
			Execute(context.Background())
		require.NoError(t, err)
		require.Nil(t, resp.Error, "error: %v", resp.Error)
		rows := unmarshalRows(t, resp.Data)
		for _, r := range rows {
			u := r["username"].(string)
			assert.NotEqual(t, "supabot", u)
			assert.NotEqual(t, "kiwicopple", u)
		}
	})
}

// TestConf_MaxAffected exercises the `Prefer: max-affected=N` guard on
// PATCH/DELETE. Exceeding the limit must return PGRST124 with the query
// rolled back. postgrest-go has no helper for this, so we drive raw HTTP.
func TestConf_MaxAffected(t *testing.T) {
	if testClient == nil {
		t.Skip("no client")
	}
	ctx := context.Background()

	// Shared setup: insert 3 marker rows so we can target them as a group.
	marker := fmt.Sprintf("maxaff_%d", time.Now().UnixNano())
	values := []map[string]interface{}{
		{"username": marker + "_1", "status": "ONLINE", "nickname": marker},
		{"username": marker + "_2", "status": "ONLINE", "nickname": marker},
		{"username": marker + "_3", "status": "ONLINE", "nickname": marker},
	}
	_, err := testClient.From("users").Insert(values, nil).Execute(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = testClient.From("users").Delete(nil).
			Eq("nickname", marker).Execute(ctx)
	})

	t.Run("PATCH over limit -> PGRST124", func(t *testing.T) {
		body := `{"status":"OFFLINE"}`
		req, err := http.NewRequest("PATCH",
			testTS.URL+"/rest/v1/users?nickname=eq."+marker, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Prefer", "max-affected=2")
		status, respBody := errorBody(t, req)
		assert.Equal(t, 400, status)
		assert.Equal(t, "PGRST124", respBody["code"])

		// Guard aborted the tx -> rows must still be ONLINE.
		check, err := testClient.From("users").Select("username,status", nil).
			Eq("nickname", marker).Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, check.Error)
		rows := unmarshalRows(t, check.Data)
		require.Equal(t, 3, len(rows))
		for _, r := range rows {
			assert.Equal(t, "ONLINE", r["status"])
		}
	})

	t.Run("PATCH within limit succeeds", func(t *testing.T) {
		body := `{"status":"OFFLINE"}`
		req, err := http.NewRequest("PATCH",
			testTS.URL+"/rest/v1/users?nickname=eq."+marker, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Prefer", "max-affected=5")
		httpResp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer httpResp.Body.Close()
		assert.Equal(t, 204, httpResp.StatusCode)

		check, err := testClient.From("users").Select("username,status", nil).
			Eq("nickname", marker).Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, check.Error)
		rows := unmarshalRows(t, check.Data)
		for _, r := range rows {
			assert.Equal(t, "OFFLINE", r["status"])
		}
	})

	t.Run("DELETE over limit -> PGRST124, rows preserved", func(t *testing.T) {
		// Insert a fresh trio so the delete has targets even after the PATCH test.
		delMarker := fmt.Sprintf("maxaff_del_%d", time.Now().UnixNano())
		delValues := []map[string]interface{}{
			{"username": delMarker + "_1", "nickname": delMarker},
			{"username": delMarker + "_2", "nickname": delMarker},
			{"username": delMarker + "_3", "nickname": delMarker},
		}
		_, err := testClient.From("users").Insert(delValues, nil).Execute(ctx)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, _ = testClient.From("users").Delete(nil).
				Eq("nickname", delMarker).Execute(ctx)
		})

		req, err := http.NewRequest("DELETE",
			testTS.URL+"/rest/v1/users?nickname=eq."+delMarker, nil)
		require.NoError(t, err)
		req.Header.Set("Prefer", "max-affected=2")
		status, respBody := errorBody(t, req)
		assert.Equal(t, 400, status)
		assert.Equal(t, "PGRST124", respBody["code"])

		check, err := testClient.From("users").Select("username", nil).
			Eq("nickname", delMarker).Execute(ctx)
		require.NoError(t, err)
		require.Nil(t, check.Error)
		assert.Equal(t, 3, len(unmarshalRows(t, check.Data)), "guard must roll back")
	})
}

func nowNano() int64 {
	return time.Now().UnixNano()
}
