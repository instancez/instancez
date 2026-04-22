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

	// FTS operators against a non-text column must be rejected at parse
	// time with PGRST100 — not passed through to Postgres (where they would
	// yield a raw SQLSTATE like 42883). users.age is int, so fts.1 fails.
	t.Run("fts on int column rejected with PGRST100", func(t *testing.T) {
		req, err := http.NewRequest("GET",
			testTS.URL+"/rest/v1/users?age=fts.1", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		status, body := errorBody(t, req)
		assert.Equal(t, 400, status)
		assert.Equal(t, "PGRST100", body["code"])
		assert.Contains(t, strings.ToLower(body["message"].(string)), "fts")
	})

	// Text columns remain valid FTS targets. users.status is text, so a
	// well-formed plainto_tsquery call must succeed (200, empty or not).
	t.Run("plfts on text column accepted", func(t *testing.T) {
		req, err := http.NewRequest("GET",
			testTS.URL+"/rest/v1/users?status=plfts.ONLINE", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode)
	})

	t.Run("Accept-Profile public accepted", func(t *testing.T) {
		req, err := http.NewRequest("GET", testTS.URL+"/rest/v1/users?select=username&limit=1", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		req.Header.Set("Accept-Profile", "public")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode)
	})

	t.Run("Accept-Profile other schema → 406 PGRST106", func(t *testing.T) {
		req, err := http.NewRequest("GET", testTS.URL+"/rest/v1/users?select=username", nil)
		require.NoError(t, err)
		req.Header.Set("Accept-Profile", "auth")
		status, body := errorBody(t, req)
		assert.Equal(t, 406, status)
		assert.Equal(t, "PGRST106", body["code"])
	})

	t.Run("Content-Profile other schema rejected on POST", func(t *testing.T) {
		req, err := http.NewRequest("POST",
			testTS.URL+"/rest/v1/users", strings.NewReader(`{"username":"skipme"}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Profile", "private")
		status, body := errorBody(t, req)
		assert.Equal(t, 406, status)
		assert.Equal(t, "PGRST106", body["code"])
	})

	t.Run("single with 0 rows errors with PGRST116 and '0 rows' in details", func(t *testing.T) {
		// supabase-js .maybeSingle() converts PGRST116 errors whose details
		// mention "0 rows" into data: null. .single() surfaces the error.
		// Either way, the server must emit PGRST116 on 0 rows for the
		// singular Accept header — not a generic not_found.
		req, err := http.NewRequest("GET",
			testTS.URL+"/rest/v1/users?select=username&username=eq.__nope_nobody__", nil)
		require.NoError(t, err)
		req.Header.Set("Accept", "application/vnd.pgrst.object+json")
		status, body := errorBody(t, req)
		assert.Equal(t, 406, status)
		assert.Equal(t, "PGRST116", body["code"])
		assert.Contains(t, body["details"].(string), "0 rows")
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

	t.Run("has-many !inner filters parents without children", func(t *testing.T) {
		// Seed has 5 users; only supabot has messages. Without !inner we get
		// all 5; with !inner we get just supabot.
		all := fetchRows(t, "/rest/v1/users?select=username,messages(id)&order=username.asc")
		require.Equal(t, 5, len(all), "baseline: all 5 users returned")

		innerRows := fetchRows(t, "/rest/v1/users?select=username,messages!inner(id)&order=username.asc")
		require.Equal(t, 1, len(innerRows), "!inner: only users with ≥1 message")
		assert.Equal(t, "supabot", innerRows[0]["username"])
		msgs, ok := innerRows[0]["messages"].([]interface{})
		require.True(t, ok)
		assert.GreaterOrEqual(t, len(msgs), 1)
	})

	t.Run("has-many !inner with embed filter", func(t *testing.T) {
		// Filter embedded messages to channel_id=1 and require ≥1 match.
		// supabot has one message in channel 1, so the parent still passes.
		rows := fetchRows(t, "/rest/v1/users?select=username,messages!inner(id)&messages.channel_id=eq.1")
		require.Equal(t, 1, len(rows))
		assert.Equal(t, "supabot", rows[0]["username"])

		// No messages in channel 999 → zero parents returned.
		empty := fetchRows(t, "/rest/v1/users?select=username,messages!inner(id)&messages.channel_id=eq.999")
		assert.Equal(t, 0, len(empty))
	})

	t.Run("has-many embed with limit and offset", func(t *testing.T) {
		// supabot has 2 messages. posts.limit=1&posts.offset=1 should
		// return only the second one.
		rows := fetchRows(t, "/rest/v1/users?select=username,messages(id,message)&messages.limit=1&messages.offset=1&messages.order=id.asc&username=eq.supabot")
		require.Equal(t, 1, len(rows))
		msgs, ok := rows[0]["messages"].([]interface{})
		require.True(t, ok)
		require.Equal(t, 1, len(msgs))
		m := msgs[0].(map[string]interface{})
		assert.Equal(t, "Second message", m["message"])
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

	// Bulk insert with `Prefer: missing=default`: rows that omit a column
	// fall back to the server-side default (here, users.status DEFAULT
	// 'ONLINE'). The server must echo Preference-Applied so clients can
	// confirm the behavior. Rows that omit no columns must also succeed.
	t.Run("Prefer missing=default echoes header and uses column defaults", func(t *testing.T) {
		u1 := fmt.Sprintf("mdflt_a_%d", time.Now().UnixNano())
		u2 := fmt.Sprintf("mdflt_b_%d", time.Now().UnixNano())
		defer func() {
			_, _ = testClient.From("users").Delete(nil).Eq("username", u1).Execute(ctx)
			_, _ = testClient.From("users").Delete(nil).Eq("username", u2).Execute(ctx)
		}()

		body := fmt.Sprintf(`[{"username":%q,"status":"OFFLINE"},{"username":%q}]`, u1, u2)
		req, err := http.NewRequest("POST", testTS.URL+"/rest/v1/users", strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Add("Prefer", "return=representation")
		req.Header.Add("Prefer", "missing=default")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 201, resp.StatusCode)
		assert.Equal(t, "missing=default", resp.Header.Get("Preference-Applied"))

		var rows []map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&rows))
		require.Equal(t, 2, len(rows))
		byName := map[string]map[string]any{}
		for _, r := range rows {
			byName[r["username"].(string)] = r
		}
		assert.Equal(t, "OFFLINE", byName[u1]["status"])
		assert.Equal(t, "ONLINE", byName[u2]["status"], "omitted column should take the schema DEFAULT")
	})

	// Without the missing=default preference the server still substitutes
	// DEFAULT (ultrabase's permissive default), but must NOT emit the
	// Preference-Applied echo.
	t.Run("no missing=default preference → no Preference-Applied echo", func(t *testing.T) {
		uname := fmt.Sprintf("mdflt_none_%d", time.Now().UnixNano())
		defer func() {
			_, _ = testClient.From("users").Delete(nil).Eq("username", uname).Execute(ctx)
		}()

		body := fmt.Sprintf(`{"username":%q}`, uname)
		req, err := http.NewRequest("POST", testTS.URL+"/rest/v1/users", strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Add("Prefer", "return=representation")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 201, resp.StatusCode)
		assert.Empty(t, resp.Header.Get("Preference-Applied"))
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

// rpcPOST issues a POST /rest/v1/rpc/:name with the given JSON body and
// returns status, raw body, and parsed JSON (or nil if the body isn't
// an object). Admin auth is attached so RLS never rejects the call —
// these tests target dispatch semantics, not authorization.
func rpcPOST(t *testing.T, name, body string, headers map[string]string) (int, []byte, any) {
	t.Helper()
	req, err := http.NewRequest("POST", testTS.URL+"/rest/v1/rpc/"+name, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testAdminKey)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var parsed any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &parsed)
	}
	return resp.StatusCode, raw, parsed
}

// TestConf_RPC exercises the /rest/v1/rpc/:name dispatcher against a
// live Postgres to lock in the wire-level behavior that supabase-js
// relies on: scalar unwrap, setof expansion, void → 204, PGRST202 on
// missing functions, PGRST102 on GET against volatile, single-object
// response coercion, and body-driven named arguments.
func TestConf_RPC(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}

	t.Run("scalar returns bare value", func(t *testing.T) {
		status, _, parsed := rpcPOST(t, "add_numbers", `{"a":2,"b":3}`, nil)
		assert.Equal(t, 200, status)
		// Scalar RPCs should return the naked number, not a wrapping object.
		switch v := parsed.(type) {
		case float64:
			assert.Equal(t, float64(5), v)
		default:
			t.Errorf("expected bare number, got %T %v", parsed, parsed)
		}
	})

	t.Run("scalar uses arg default when omitted", func(t *testing.T) {
		// greet() has a default of 'world'; an empty body must fall
		// through to the Postgres default rather than erroring.
		status, _, parsed := rpcPOST(t, "greet", `{}`, nil)
		assert.Equal(t, 200, status)
		assert.Equal(t, "hello world", parsed)
	})

	t.Run("setof returns array of rows", func(t *testing.T) {
		status, _, parsed := rpcPOST(t, "users_by_status", `{"target":"ONLINE"}`, nil)
		assert.Equal(t, 200, status)
		arr, ok := parsed.([]any)
		require.True(t, ok, "expected array, got %T", parsed)
		assert.GreaterOrEqual(t, len(arr), 1)
	})

	t.Run("setof with singular Accept → object", func(t *testing.T) {
		status, _, parsed := rpcPOST(t, "users_by_status", `{"target":"ONLINE"}`,
			map[string]string{"Accept": "application/vnd.pgrst.object+json"})
		// Multiple rows match ONLINE → 406 PGRST116, same as table-level.
		assert.Equal(t, 406, status)
		m, ok := parsed.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "PGRST116", m["code"])
	})

	t.Run("void returns 204 with empty body", func(t *testing.T) {
		status, raw, _ := rpcPOST(t, "touch_nothing", `{}`, nil)
		assert.Equal(t, 204, status)
		assert.Empty(t, raw)
	})

	t.Run("stable function attempting write is blocked", func(t *testing.T) {
		// sneaky_insert is declared STABLE but tries to INSERT. The RPC
		// handler pins non-volatile transactions to read-only, so
		// Postgres rejects the write with SQLSTATE 25006 (read_only_sql_
		// transaction). Error is surfaced through handleDBError.
		status, _, parsed := rpcPOST(t, "sneaky_insert", `{}`, nil)
		assert.GreaterOrEqual(t, status, 400)
		m, ok := parsed.(map[string]any)
		require.True(t, ok, "expected error body, got %T", parsed)
		// Code should be the pg SQLSTATE or a PGRST wrapper. Either way,
		// the message must mention read-only or the seeded row must not
		// have been committed.
		msg, _ := m["message"].(string)
		code, _ := m["code"].(string)
		hasReadOnly := strings.Contains(strings.ToLower(msg), "read-only") ||
			strings.Contains(strings.ToLower(msg), "read only") ||
			code == "25006"
		assert.True(t, hasReadOnly, "error body should indicate read-only: %v", m)

		// Belt-and-suspenders: no 'sneaky' channel should have been committed.
		rows := fetchRows(t, "/rest/v1/channels?select=slug&slug=eq.sneaky")
		assert.Equal(t, 0, len(rows), "sneaky insert must not commit")
	})

	t.Run("volatile function can still write", func(t *testing.T) {
		// touch_nothing is VOLATILE, so the read-only guard is off. It
		// doesn't actually write but verifies the code path is unaffected.
		status, _, _ := rpcPOST(t, "touch_nothing", `{}`, nil)
		assert.Equal(t, 204, status)
	})

	t.Run("unknown function → PGRST202", func(t *testing.T) {
		status, _, parsed := rpcPOST(t, "does_not_exist", `{}`, nil)
		assert.Equal(t, 404, status)
		m, ok := parsed.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "PGRST202", m["code"])
	})

	t.Run("unknown arg key is rejected", func(t *testing.T) {
		// 'wrong' is not declared on add_numbers; PostgREST would
		// return a PGRST202-style error. We return a 400 bad_request
		// with a descriptive message; locking that shape in here lets
		// supabase-js surface it as error.message.
		status, _, parsed := rpcPOST(t, "add_numbers", `{"a":1,"b":2,"wrong":9}`, nil)
		assert.Equal(t, 400, status)
		m, ok := parsed.(map[string]any)
		require.True(t, ok)
		assert.NotEmpty(t, m["message"])
	})

	t.Run("GET on volatile function → 405 PGRST102", func(t *testing.T) {
		req, err := http.NewRequest("GET", testTS.URL+"/rest/v1/rpc/touch_nothing", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		assert.Equal(t, 405, resp.StatusCode)
		assert.Equal(t, "PGRST102", m["code"])
	})

	t.Run("GET on stable function works", func(t *testing.T) {
		// greet() is STABLE and takes a text arg, so query-string values
		// flow through without requiring an int cast. PostgREST also
		// allows GET on STABLE/IMMUTABLE functions — the contract we're
		// locking in here.
		req, err := http.NewRequest("GET",
			testTS.URL+"/rest/v1/rpc/greet?name=alice", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		assert.Equal(t, 200, resp.StatusCode, "body: %s", string(raw))
		var v any
		require.NoError(t, json.Unmarshal(raw, &v))
		assert.Equal(t, "hello alice", v)
	})

	t.Run("setof with .eq filter chaining", func(t *testing.T) {
		// supabase-js .rpc('users_by_status', {target:'ONLINE'}).eq('username','supabot')
		// puts the arg in the body and the filter in the query string. The
		// dispatcher must route each to the right place and apply the filter
		// against the function result via a subquery wrap.
		req, err := http.NewRequest("POST",
			testTS.URL+"/rest/v1/rpc/users_by_status?username=eq.supabot",
			strings.NewReader(`{"target":"ONLINE"}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		assert.Equal(t, 200, resp.StatusCode, "body: %s", string(raw))
		var arr []map[string]any
		require.NoError(t, json.Unmarshal(raw, &arr))
		require.Len(t, arr, 1, "eq filter should narrow to exactly one row")
		assert.Equal(t, "supabot", arr[0]["username"])
	})

	t.Run("setof with order + limit", func(t *testing.T) {
		// Order by username desc; limit 2. users_by_status(ONLINE) returns
		// {supabot, awailas, dragarcia}; desc → {supabot, dragarcia, awailas}
		// and limit 2 cuts it to the first two.
		req, err := http.NewRequest("POST",
			testTS.URL+"/rest/v1/rpc/users_by_status?order=username.desc&limit=2",
			strings.NewReader(`{"target":"ONLINE"}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		assert.Equal(t, 200, resp.StatusCode, "body: %s", string(raw))
		var arr []map[string]any
		require.NoError(t, json.Unmarshal(raw, &arr))
		require.Len(t, arr, 2)
		assert.Equal(t, "supabot", arr[0]["username"])
		assert.Equal(t, "dragarcia", arr[1]["username"])
	})

	t.Run("setof emits Content-Range without count", func(t *testing.T) {
		// PostgREST always emits Content-Range on row-returning responses;
		// without Prefer: count=* the total is "*".
		req, err := http.NewRequest("POST",
			testTS.URL+"/rest/v1/rpc/users_by_status",
			strings.NewReader(`{"target":"ONLINE"}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		cr := resp.Header.Get("Content-Range")
		require.NotEmpty(t, cr, "Content-Range must be set on setof responses")
		assert.Contains(t, cr, "/*", "total should be * without count prefer")
	})

	t.Run("setof with count=exact returns total", func(t *testing.T) {
		// Three ONLINE users; limit 1 trims the payload but the count must
		// still report the full filtered total (3).
		req, err := http.NewRequest("POST",
			testTS.URL+"/rest/v1/rpc/users_by_status?limit=1",
			strings.NewReader(`{"target":"ONLINE"}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		req.Header.Set("Prefer", "count=exact")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		cr := resp.Header.Get("Content-Range")
		// Expect "0-0/3" (page of 1, total 3)
		assert.Equal(t, "0-0/3", cr)
	})

	t.Run("setof with count=exact and WHERE", func(t *testing.T) {
		// Exercises the count-query WHERE replay: parseRPCChain renders
		// the filter tree once for the data query starting at $2, and
		// executeRPCCount re-renders the same tree starting at its own
		// $2. If the two paths ever drift on placeholder numbering, the
		// count query crashes or returns the wrong total. Filter trims
		// three ONLINE users down to one, so the total must be 1 even
		// though limit=10 would fit all matches.
		req, err := http.NewRequest("POST",
			testTS.URL+"/rest/v1/rpc/users_by_status?username=eq.supabot&limit=10",
			strings.NewReader(`{"target":"ONLINE"}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		req.Header.Set("Prefer", "count=exact")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, "0-0/1", resp.Header.Get("Content-Range"))
	})

	t.Run("HEAD on stable function returns Content-Range, no body", func(t *testing.T) {
		req, err := http.NewRequest("HEAD",
			testTS.URL+"/rest/v1/rpc/users_by_status?target=ONLINE", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		req.Header.Set("Prefer", "count=exact")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode)
		assert.NotEmpty(t, resp.Header.Get("Content-Range"))
		body, _ := io.ReadAll(resp.Body)
		assert.Empty(t, body, "HEAD should not carry a body")
	})

	t.Run("HEAD on volatile function → 405 PGRST102", func(t *testing.T) {
		req, err := http.NewRequest("HEAD",
			testTS.URL+"/rest/v1/rpc/touch_nothing", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 405, resp.StatusCode)
	})

	t.Run("setof with select projection narrows columns", func(t *testing.T) {
		// supabase-js .rpc('users_by_status',{target:'ONLINE'}).select('username')
		// sends select=username in the query string. The response must
		// contain only the projected column, not the full row tuple.
		req, err := http.NewRequest("POST",
			testTS.URL+"/rest/v1/rpc/users_by_status?select=username&order=username.asc",
			strings.NewReader(`{"target":"ONLINE"}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		assert.Equal(t, 200, resp.StatusCode, "body: %s", string(raw))
		var arr []map[string]any
		require.NoError(t, json.Unmarshal(raw, &arr))
		require.GreaterOrEqual(t, len(arr), 1)
		// Every row should have exactly one key: "username".
		for _, row := range arr {
			assert.Len(t, row, 1, "projection should leave one column, got %v", row)
			_, ok := row["username"]
			assert.True(t, ok, "row should contain username, got %v", row)
		}
	})

	t.Run("setof with select alias projects to aliased key", func(t *testing.T) {
		// select=handle:username renames the column in the response. This
		// is the shape supabase-js produces when callers use .select() on
		// an RPC result with a custom alias.
		req, err := http.NewRequest("POST",
			testTS.URL+"/rest/v1/rpc/users_by_status?select=handle:username&limit=1",
			strings.NewReader(`{"target":"ONLINE"}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		assert.Equal(t, 200, resp.StatusCode, "body: %s", string(raw))
		var arr []map[string]any
		require.NoError(t, json.Unmarshal(raw, &arr))
		require.Len(t, arr, 1)
		_, hasAlias := arr[0]["handle"]
		assert.True(t, hasAlias, "expected aliased 'handle' key, got %v", arr[0])
	})

	t.Run("setof select=unknown column is rejected", func(t *testing.T) {
		req, err := http.NewRequest("POST",
			testTS.URL+"/rest/v1/rpc/users_by_status?select=bogus_col",
			strings.NewReader(`{"target":"ONLINE"}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 400, resp.StatusCode)
	})
}

// TestConf_JSONBArrayIndex locks in the chain-form JSONB path accessor
// (e.g. `data->items->0->>name`) end-to-end. Earlier splitJSONBColumn
// only split on the first operator, so array-index chains failed at
// parse time. The refactor makes splitJSONBPath walk the whole chain
// and render integer keys unquoted so Postgres treats them as array
// subscripts.
func TestConf_JSONBArrayIndex(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}
	ctx := context.Background()

	// Seed: set channels.data to a jsonb document containing an array of
	// objects, then query through the public HTTP API. We clean up at the
	// end so other tests that assume channels.data is NULL keep working.
	_, err := testDB.Exec(ctx,
		`UPDATE channels SET data = '{"items":[{"name":"alpha","qty":2},{"name":"beta","qty":5}]}'::jsonb WHERE slug = 'public'`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = testDB.Exec(context.Background(), `UPDATE channels SET data = NULL WHERE slug = 'public'`)
	})

	t.Run("filter on nested array element", func(t *testing.T) {
		req, err := http.NewRequest("GET",
			testTS.URL+`/rest/v1/channels?select=slug&data->items->0->>name=eq.alpha`, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		require.Equal(t, 200, resp.StatusCode, "body: %s", string(raw))
		var arr []map[string]any
		require.NoError(t, json.Unmarshal(raw, &arr))
		require.Len(t, arr, 1, "expected exactly one row where items[0].name = alpha")
		assert.Equal(t, "public", arr[0]["slug"])
	})

	t.Run("project nested array element", func(t *testing.T) {
		req, err := http.NewRequest("GET",
			testTS.URL+`/rest/v1/channels?select=slug,first_item_name:data->items->0->>name&slug=eq.public`, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		require.Equal(t, 200, resp.StatusCode, "body: %s", string(raw))
		var arr []map[string]any
		require.NoError(t, json.Unmarshal(raw, &arr))
		require.Len(t, arr, 1)
		assert.Equal(t, "alpha", arr[0]["first_item_name"])
	})

	t.Run("filter with no match returns zero rows", func(t *testing.T) {
		req, err := http.NewRequest("GET",
			testTS.URL+`/rest/v1/channels?select=slug&data->items->1->>name=eq.nope`, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		require.Equal(t, 200, resp.StatusCode, "body: %s", string(raw))
		var arr []map[string]any
		require.NoError(t, json.Unmarshal(raw, &arr))
		assert.Len(t, arr, 0)
	})
}

// TestConf_Aggregates locks in the PostgREST aggregate surface end-to-end:
// column-form (col.count(), col.sum(), etc.), bare count(), alias override,
// and the GROUP BY auto-emission when a plain column is mixed with an
// aggregate. Seed state: five users, three ONLINE / two OFFLINE, ages
// 1,30,28,27,25 → totals are deterministic per row group.
func TestConf_Aggregates(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}

	t.Run("bare count returns row total", func(t *testing.T) {
		req, err := http.NewRequest("GET", testTS.URL+`/rest/v1/users?select=count()`, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		require.Equal(t, 200, resp.StatusCode, "body: %s", string(raw))
		var arr []map[string]any
		require.NoError(t, json.Unmarshal(raw, &arr))
		require.Len(t, arr, 1)
		assert.EqualValues(t, 5, arr[0]["count"])
	})

	t.Run("column count with explicit alias", func(t *testing.T) {
		req, err := http.NewRequest("GET",
			testTS.URL+`/rest/v1/users?select=total:username.count()`, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		require.Equal(t, 200, resp.StatusCode, "body: %s", string(raw))
		var arr []map[string]any
		require.NoError(t, json.Unmarshal(raw, &arr))
		require.Len(t, arr, 1)
		assert.EqualValues(t, 5, arr[0]["total"])
	})

	t.Run("sum avg min max on ages", func(t *testing.T) {
		req, err := http.NewRequest("GET",
			testTS.URL+`/rest/v1/users?select=age.sum(),age.avg(),age.min(),age.max()`, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		require.Equal(t, 200, resp.StatusCode, "body: %s", string(raw))
		var arr []map[string]any
		require.NoError(t, json.Unmarshal(raw, &arr))
		require.Len(t, arr, 1)
		assert.EqualValues(t, 111, arr[0]["sum"]) // 1+30+28+27+25
		assert.EqualValues(t, 1, arr[0]["min"])
		assert.EqualValues(t, 30, arr[0]["max"])
		// avg comes back as string because Postgres returns NUMERIC for int
		// avg; clients decode it as a string-encoded number.
		switch v := arr[0]["avg"].(type) {
		case string:
			assert.Contains(t, v, "22")
		case float64:
			assert.InDelta(t, 22.2, v, 0.01)
		default:
			t.Errorf("unexpected avg type %T: %v", v, v)
		}
	})

	t.Run("group by status with count and avg age", func(t *testing.T) {
		req, err := http.NewRequest("GET",
			testTS.URL+`/rest/v1/users?select=status,username.count(),age.sum()&order=status`, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		require.Equal(t, 200, resp.StatusCode, "body: %s", string(raw))
		var arr []map[string]any
		require.NoError(t, json.Unmarshal(raw, &arr))
		require.Len(t, arr, 2, "expected one group per status")

		byStatus := map[string]map[string]any{}
		for _, row := range arr {
			byStatus[row["status"].(string)] = row
		}

		online, ok := byStatus["ONLINE"]
		require.True(t, ok)
		assert.EqualValues(t, 3, online["count"])
		assert.EqualValues(t, 54, online["sum"]) // 1+28+25

		offline, ok := byStatus["OFFLINE"]
		require.True(t, ok)
		assert.EqualValues(t, 2, offline["count"])
		assert.EqualValues(t, 57, offline["sum"]) // 30+27
	})

	t.Run("unknown column in aggregate is rejected", func(t *testing.T) {
		req, err := http.NewRequest("GET",
			testTS.URL+`/rest/v1/users?select=bogus.sum()`, nil)
		require.NoError(t, err)
		status, body := errorBody(t, req)
		assert.Equal(t, 400, status)
		assert.NotEmpty(t, body["message"])
	})
}

// TestConf_RequestID verifies end-to-end that:
//
//  1. Every response carries an X-Request-Id (either echoed or generated)
//  2. The same ID is published into the transaction as
//     current_setting('request.request_id') so RLS policies and RPC bodies
//     can observe it
//
// The fixture `current_request_id()` (declared in schemaSQL) reads the GUC
// and returns it, which lets us compare against the header on the same
// response.
func TestConf_RequestID(t *testing.T) {
	if testClient == nil {
		t.Skip("no client")
	}

	t.Run("client header is echoed and reaches SQL", func(t *testing.T) {
		const want = "conf-abcd-1234"
		req, err := http.NewRequest("POST",
			testTS.URL+"/rest/v1/rpc/current_request_id",
			strings.NewReader("{}"))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Request-Id", want)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, want, resp.Header.Get("X-Request-Id"))

		body, _ := io.ReadAll(resp.Body)
		var got string
		require.NoError(t, json.Unmarshal(body, &got))
		assert.Equal(t, want, got, "RPC should see the same request id via GUC")
	})

	t.Run("missing header → server generates, still visible in SQL", func(t *testing.T) {
		req, err := http.NewRequest("POST",
			testTS.URL+"/rest/v1/rpc/current_request_id",
			strings.NewReader("{}"))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, 200, resp.StatusCode)
		hdr := resp.Header.Get("X-Request-Id")
		require.NotEmpty(t, hdr, "server must generate an id when the client omits one")

		body, _ := io.ReadAll(resp.Body)
		var got string
		require.NoError(t, json.Unmarshal(body, &got))
		assert.Equal(t, hdr, got, "generated id should also reach SQL")
	})

	t.Run("request id also reaches CRUD transactions", func(t *testing.T) {
		// Not just RPC: ordinary CRUD requests open a tx via db.Begin too,
		// so the GUC should be set there as well. We piggyback on the
		// users table SELECT and only check the response header — the
		// SQL-side check is covered by the RPC cases above.
		const want = "conf-crud-9"
		req, err := http.NewRequest("GET",
			testTS.URL+"/rest/v1/users?select=username&limit=1", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		req.Header.Set("X-Request-Id", want)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode)
		assert.Equal(t, want, resp.Header.Get("X-Request-Id"))
	})
}

// TestConf_Having validates the HAVING clause integration with a live
// database. HAVING filters are applied after GROUP BY, so they operate on
// aggregate values and grouped columns.
func TestConf_Having(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}

	// Seed state: 3 ONLINE users, 2 OFFLINE users.
	// having=count.gt.2 should only return the ONLINE group.
	t.Run("having filters aggregate results", func(t *testing.T) {
		rows := fetchRows(t,
			`/rest/v1/users?select=status,username.count()&order=status&having=count.gt.2`)
		require.Len(t, rows, 1, "only ONLINE group has count > 2")
		assert.Equal(t, "ONLINE", rows[0]["status"])
		assert.EqualValues(t, 3, rows[0]["count"])
	})

	t.Run("having on grouped column", func(t *testing.T) {
		rows := fetchRows(t,
			`/rest/v1/users?select=status,username.count()&order=status&having=status.eq.OFFLINE`)
		require.Len(t, rows, 1)
		assert.Equal(t, "OFFLINE", rows[0]["status"])
	})

	t.Run("having with no matching groups returns empty", func(t *testing.T) {
		rows := fetchRows(t,
			`/rest/v1/users?select=status,username.count()&having=count.gt.100`)
		assert.Len(t, rows, 0)
	})
}

// TestConf_RPCEmbeds validates that embeds work on RPC results when the
// function returns SETOF of a known table with FK relationships.
func TestConf_RPCEmbeds(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}

	// users_by_status returns SETOF users. The messages table has a FK
	// (username → users.username), so messages is a has-many embed on users.
	// We can also do belongs-to from messages to users, but the RPC returns
	// users, so we test has-many (messages) embed.
	t.Run("has-many embed on RPC setof result", func(t *testing.T) {
		// supabot is ONLINE and has 2 messages in the seed data.
		req, err := http.NewRequest("POST",
			testTS.URL+"/rest/v1/rpc/users_by_status?select=username,messages(message)",
			strings.NewReader(`{"target":"ONLINE"}`))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		require.Equal(t, 200, resp.StatusCode, "body: %s", string(raw))

		var rows []map[string]any
		require.NoError(t, json.Unmarshal(raw, &rows))
		require.GreaterOrEqual(t, len(rows), 1)

		// Find supabot's row.
		var supabot map[string]any
		for _, r := range rows {
			if r["username"] == "supabot" {
				supabot = r
				break
			}
		}
		require.NotNil(t, supabot, "supabot should be in ONLINE results")

		// The messages embed should be a JSON array.
		msgs, ok := supabot["messages"]
		require.True(t, ok, "expected messages embed in response")
		msgArr, ok := msgs.([]any)
		require.True(t, ok, "messages should be an array, got %T", msgs)
		assert.Len(t, msgArr, 2, "supabot has 2 seed messages")
	})

	t.Run("embed on unknown RPC return type rejected", func(t *testing.T) {
		// add_numbers returns int (scalar), not setof — embeds don't apply.
		// But more importantly, if we had a function returning setof of an
		// unknown type, embeds should be rejected. We test via query param.
		req, err := http.NewRequest("GET",
			testTS.URL+"/rest/v1/rpc/users_by_status?select=username,bogus_table(*)&target=ONLINE",
			nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		// Should get a 400 because bogus_table is not a valid embed on users.
		assert.Equal(t, 400, resp.StatusCode)
	})
}

// TestConf_ErrorHints verifies that common database errors include useful
// hint text in the response when Postgres doesn't provide one.
func TestConf_ErrorHints(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}

	t.Run("duplicate key includes constraint hint", func(t *testing.T) {
		// Insert a user that already exists.
		req, err := http.NewRequest("POST", testTS.URL+"/rest/v1/users",
			strings.NewReader(`{"username":"supabot","status":"ONLINE","age":99}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		status, body := errorBody(t, req)
		assert.Equal(t, 409, status)
		assert.Equal(t, "23505", body["code"])
		// Should have a non-empty hint about the constraint.
		hint, _ := body["hint"].(string)
		assert.NotEmpty(t, hint, "duplicate key should include a hint")
		assert.Contains(t, strings.ToLower(hint), "already exists")
	})

	t.Run("not-null violation includes column hint", func(t *testing.T) {
		// messages.username is NOT NULL required.
		req, err := http.NewRequest("POST", testTS.URL+"/rest/v1/messages",
			strings.NewReader(`{"message":"test","channel_id":1}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		status, body := errorBody(t, req)
		assert.Equal(t, 422, status)
		assert.Equal(t, "23502", body["code"])
	})

	t.Run("foreign key violation includes hint", func(t *testing.T) {
		req, err := http.NewRequest("POST", testTS.URL+"/rest/v1/messages",
			strings.NewReader(`{"message":"test","username":"nonexistent_user","channel_id":1}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		status, body := errorBody(t, req)
		assert.Equal(t, 422, status)
		assert.Equal(t, "23503", body["code"])
		hint, _ := body["hint"].(string)
		assert.NotEmpty(t, hint, "FK violation should include a hint")
	})
}

// TestConf_CSVTypeCoercion validates that CSV imports correctly coerce
// string values to typed Go values based on the table schema.
func TestConf_CSVTypeCoercion(t *testing.T) {
	if testTS == nil {
		t.Skip("no upstream")
	}

	// Insert a user via CSV and verify the integer age is correctly handled.
	t.Run("csv insert with integer coercion", func(t *testing.T) {
		csvBody := "username,status,age\ncsvuser1,ONLINE,42\n"
		req, err := http.NewRequest("POST", testTS.URL+"/rest/v1/users",
			strings.NewReader(csvBody))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		req.Header.Set("Content-Type", "text/csv")
		req.Header.Set("Prefer", "return=representation")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		require.Equal(t, 201, resp.StatusCode, "body: %s", string(raw))

		var rows []map[string]any
		require.NoError(t, json.Unmarshal(raw, &rows))
		require.Len(t, rows, 1)
		assert.Equal(t, "csvuser1", rows[0]["username"])
		assert.EqualValues(t, 42, rows[0]["age"])

		// Cleanup
		t.Cleanup(func() {
			dreq, _ := http.NewRequest("DELETE",
				testTS.URL+"/rest/v1/users?username=eq.csvuser1", nil)
			dreq.Header.Set("Authorization", "Bearer "+testAdminKey)
			http.DefaultClient.Do(dreq)
		})
	})

	t.Run("csv insert with empty integer becomes null", func(t *testing.T) {
		csvBody := "username,status,age\ncsvuser2,ONLINE,\n"
		req, err := http.NewRequest("POST", testTS.URL+"/rest/v1/users",
			strings.NewReader(csvBody))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+testAdminKey)
		req.Header.Set("Content-Type", "text/csv")
		req.Header.Set("Prefer", "return=representation")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		require.Equal(t, 201, resp.StatusCode, "body: %s", string(raw))

		var rows []map[string]any
		require.NoError(t, json.Unmarshal(raw, &rows))
		require.Len(t, rows, 1)
		assert.Nil(t, rows[0]["age"], "empty int CSV cell should be NULL")

		t.Cleanup(func() {
			dreq, _ := http.NewRequest("DELETE",
				testTS.URL+"/rest/v1/users?username=eq.csvuser2", nil)
			dreq.Header.Set("Authorization", "Bearer "+testAdminKey)
			http.DefaultClient.Do(dreq)
		})
	})
}

func nowNano() int64 {
	return time.Now().UnixNano()
}
