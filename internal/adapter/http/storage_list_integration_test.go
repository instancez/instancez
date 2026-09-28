//go:build integration

package http

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listRouter runs the list handlers against a real storage.objects table.
func listRouter(t *testing.T) *gin.Engine {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbboot.StartRawContainer(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	_, err = pool.Exec(ctx, `CREATE SCHEMA storage;
CREATE TABLE storage.objects (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), bucket_id text NOT NULL, name text NOT NULL,
  size bigint NOT NULL DEFAULT 0, mime text NOT NULL DEFAULT '', uploaded_at timestamptz NOT NULL, metadata jsonb, UNIQUE (bucket_id, name))`)
	require.NoError(t, err)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, name := range []string{
		"top.txt", "a-b.txt", "a/x.txt", "a/y/z.txt", "B.txt", "b/c.txt",
		"dir/file.txt", "dir/sub/deep.txt", "dir/sub/deeper/d.txt", "dir/Sub2/q.txt", "dir/zz.txt", "we_ird%/p.txt", "wexird/p.txt", "ünï/c.txt",
	} {
		_, err = pool.Exec(ctx, `INSERT INTO storage.objects (bucket_id, name, uploaded_at, metadata) VALUES ('b', $1, $2, '{"size":1}')`, name, base.Add(time.Duration(i)*time.Minute))
		require.NoError(t, err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO storage.objects (bucket_id, name, uploaded_at) VALUES ('other', 'top.txt', now())`)
	require.NoError(t, err)

	db := &stubDB{queryFn: func(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
		rows, err := pool.Query(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, pgx.RowToMap)
	}}
	h := newStorageHandler(db, &stubObjectStore{}, map[string]domain.Bucket{"b": {}})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/storage/v1/object/list/:bucket", h.listObjects)
	r.POST("/storage/v1/object/list-v2/:bucket", h.listObjectsV2)
	return r
}

func v1Names(t *testing.T, r *gin.Engine, body string) ([]string, []map[string]any) {
	t.Helper()
	w := serve(r, "POST", "/storage/v1/object/list/b", body, nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	var items []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &items))
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it["name"].(string)
	}
	return names, items
}

type v2Resp struct {
	HasNext       bool             `json:"hasNext"`
	NextCursor    string           `json:"nextCursor"`
	NextCursorKey string           `json:"nextCursorKey"`
	Folders       []map[string]any `json:"folders"`
	Objects       []map[string]any `json:"objects"`
}

func v2List(t *testing.T, r *gin.Engine, body string) v2Resp {
	t.Helper()
	w := serve(r, "POST", "/storage/v1/object/list-v2/b", body, nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	var resp v2Resp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

func names(items []map[string]any) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it["name"].(string)
	}
	return out
}

func TestStorageList_V1FoldsLikeStorageSearch(t *testing.T) {
	r := listRouter(t)

	got, items := v1Names(t, r, `{"prefix":""}`)
	assert.Equal(t, []string{"a-b.txt", "a", "B.txt", "b", "dir", "top.txt", "we_ird%", "wexird", "ünï"}, got,
		"case-insensitive C order; a folder sorts as its key plus a slash")
	assert.Nil(t, items[1]["id"])
	assert.Nil(t, items[1]["metadata"])
	assert.Nil(t, items[1]["created_at"])
	assert.NotNil(t, items[0]["id"])
	assert.Equal(t, "2026-01-01T00:01:00.000Z", items[0]["created_at"])

	got, _ = v1Names(t, r, `{"prefix":"dir"}`)
	assert.Equal(t, []string{"file.txt", "sub", "Sub2", "zz.txt"}, got, "prefix gains a slash; nested keys fold once")

	got, _ = v1Names(t, r, `{"prefix":"dir/","limit":2,"offset":1}`)
	assert.Equal(t, []string{"sub", "Sub2"}, got, "offset counts folders and files alike")

	got, _ = v1Names(t, r, `{"prefix":"DIR/","search":"S"}`)
	assert.Equal(t, []string{"sub", "Sub2"}, got, "search is a case-insensitive prefix under the folder")

	got, _ = v1Names(t, r, `{"prefix":"","search":"we_"}`)
	assert.Equal(t, []string{"we_ird%"}, got, "LIKE wildcards in search are literal")

	got, _ = v1Names(t, r, `{"prefix":"dir/","sortBy":{"column":"name","order":"desc"}}`)
	assert.Equal(t, []string{"zz.txt", "Sub2", "sub", "file.txt"}, got)

	got, _ = v1Names(t, r, `{"prefix":"dir/","sortBy":{"column":"created_at","order":"desc"}}`)
	assert.Equal(t, []string{"Sub2", "sub", "zz.txt", "file.txt"}, got, "folders first, then files by time")

	got, _ = v1Names(t, r, `{"prefix":"ünï/"}`)
	assert.Equal(t, []string{"c.txt"}, got, "multi-byte prefixes cut by characters")

	got, _ = v1Names(t, r, `{"prefix":"nothing/"}`)
	assert.Empty(t, got)
}

func TestStorageList_V2MatchesListObjectsWithDelimiter(t *testing.T) {
	r := listRouter(t)

	resp := v2List(t, r, `{"prefix":"dir/","with_delimiter":true}`)
	assert.Equal(t, []string{"dir/Sub2/", "dir/sub/"}, names(resp.Folders), "full keys, byte order, one row per folder")
	assert.Equal(t, []string{"dir/file.txt", "dir/zz.txt"}, names(resp.Objects))
	assert.Equal(t, "file.txt", resp.Objects[0]["key"])
	assert.Nil(t, resp.Folders[0]["id"])
	assert.Equal(t, "b", resp.Folders[0]["bucket_id"])
	assert.False(t, resp.HasNext)

	var pages [][]string
	cursor := ""
	for range 10 {
		body := `{"prefix":"dir/","with_delimiter":true,"limit":1`
		if cursor != "" {
			body += `,"cursor":"` + cursor + `"`
		}
		resp = v2List(t, r, body+`}`)
		pages = append(pages, append(names(resp.Folders), names(resp.Objects)...))
		if !resp.HasNext {
			break
		}
		cursor = resp.NextCursor
	}
	assert.Equal(t, [][]string{{"dir/Sub2/"}, {"dir/file.txt"}, {"dir/sub/"}, {"dir/zz.txt"}}, pages, "a folder cursor skips its whole subtree")

	resp = v2List(t, r, `{"prefix":"dir/"}`)
	assert.Empty(t, resp.Folders)
	assert.Equal(t, []string{"dir/Sub2/q.txt", "dir/file.txt", "dir/sub/deep.txt", "dir/sub/deeper/d.txt", "dir/zz.txt"}, names(resp.Objects), "no delimiter lists every key")

	resp = v2List(t, r, `{"prefix":"dir/","with_delimiter":true,"sortBy":{"column":"created_at","order":"desc"},"limit":2}`)
	assert.Equal(t, []string{"dir/zz.txt"}, names(resp.Objects))
	assert.Equal(t, []string{"dir/Sub2/"}, names(resp.Folders))
	resp = v2List(t, r, `{"prefix":"dir/","with_delimiter":true,"sortBy":{"column":"created_at","order":"desc"},"cursor":"`+resp.NextCursor+`"}`)
	assert.Equal(t, []string{"dir/sub/"}, names(resp.Folders), "folders sort by their earliest object")
	assert.Equal(t, []string{"dir/file.txt"}, names(resp.Objects))

	resp = v2List(t, r, `{"prefix":"we_","with_delimiter":true}`)
	assert.Equal(t, []string{"we_ird%/"}, names(resp.Folders), "prefix wildcards are literal")
}
