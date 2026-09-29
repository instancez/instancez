//go:build integration

package http

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type queryer interface {
	Query(ctx context.Context, q string, args ...any) ([]map[string]any, error)
}

// listDB migrates a real database with the storage tables, so the tests run the production DDL.
func listDB(t *testing.T, buckets map[string]domain.Bucket) (domain.OwnerDB, domain.RequestDB) {
	t.Helper()
	owner, req := dbboot.StartContainer(t)
	require.NoError(t, app.NewMigrator(owner).Apply(context.Background(), &domain.Config{Version: 1, Auth: &domain.Auth{}, Storage: buckets}))
	return owner, req
}

var plainBuckets = map[string]domain.Bucket{"b": {}, "big": {}}

func routerFor(db domain.Database) *gin.Engine {
	h := newStorageHandler(db, &stubObjectStore{}, map[string]domain.Bucket{"b": {}, "big": {}})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/storage/v1/object/list/:bucket", h.listObjects)
	r.POST("/storage/v1/object/list-v2/:bucket", h.listObjectsV2)
	return r
}

func seedObjects(t *testing.T, db domain.Database, bucket string, base time.Time, names ...string) {
	t.Helper()
	for i, name := range names {
		_, err := db.Exec(context.Background(), `INSERT INTO storage.objects (bucket_id, name, uploaded_at, metadata) VALUES ($1, $2, $3, '{"size":1}')`, bucket, name, base.Add(time.Duration(i)*time.Minute))
		require.NoError(t, err)
	}
}

// listRouter runs the list handlers against a real storage.objects table.
func listRouter(t *testing.T) *gin.Engine {
	t.Helper()
	owner, _ := listDB(t, plainBuckets)
	seedObjects(t, owner, "b", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		"top.txt", "a-b.txt", "a/x.txt", "a/y/z.txt", "B.txt", "b/c.txt",
		"dir/file.txt", "dir/sub/deep.txt", "dir/sub/deeper/d.txt", "dir/Sub2/q.txt", "dir/zz.txt", "we_ird%/p.txt", "wexird/p.txt", "ünï/c.txt")
	seedObjects(t, owner, "other", time.Now(), "top.txt")
	return routerFor(owner)
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

// scannedRows sums the rows every scan node read, across loops.
func scannedRows(node map[string]any) float64 {
	var n float64
	if strings.HasSuffix(asString(node["Node Type"]), "Scan") && node["Node Type"] != "CTE Scan" && node["Node Type"] != "WorkTable Scan" {
		n += node["Actual Rows"].(float64) * node["Actual Loops"].(float64)
		if f, ok := node["Rows Removed by Filter"].(float64); ok {
			n += f * node["Actual Loops"].(float64)
		}
	}
	for _, c := range asSlice(node["Plans"]) {
		n += scannedRows(c.(map[string]any))
	}
	return n
}

func asSlice(v any) []any { s, _ := v.([]any); return s }

// explainPlan returns the root plan node of EXPLAIN (ANALYZE, FORMAT JSON).
func explainPlan(t *testing.T, db queryer, ctx context.Context, name, sql string, args []any) map[string]any {
	t.Helper()
	rows, err := db.Query(ctx, "EXPLAIN (ANALYZE, FORMAT JSON) "+sql, args...)
	require.NoError(t, err, name)
	plan := rows[0]["QUERY PLAN"]
	if b, ok := plan.(string); ok {
		var parsed []any
		require.NoError(t, json.Unmarshal([]byte(b), &parsed))
		plan = parsed
	}
	return plan.([]any)[0].(map[string]any)["Plan"].(map[string]any)
}

func seedBig(t *testing.T, db domain.Database) {
	t.Helper()
	_, err := db.Exec(context.Background(), `INSERT INTO storage.objects (bucket_id, name, uploaded_at)
SELECT 'big', format('p/f%s/x%s.txt', lpad(f::text, 3, '0'), x), now() FROM generate_series(1, 400) f, generate_series(1, 100) x;
INSERT INTO storage.objects (bucket_id, name, uploaded_at)
SELECT 'big', format('q/%s.txt', i), now() FROM generate_series(1, 10000) i;
INSERT INTO storage.objects (bucket_id, name, uploaded_at) SELECT 'big', format('p/top%s.txt', i), now() FROM generate_series(1, 5) i;
ANALYZE storage.objects`)
	require.NoError(t, err)
}

func boundedShapes() map[string]listQuery {
	return map[string]listQuery{
		"v1 page":             {bucket: "big", match: "p/", fold: true, foldFrom: 2, caseFold: true, limit: 10, offset: 50},
		"v1 desc":             {bucket: "big", match: "p/", fold: true, foldFrom: 2, caseFold: true, desc: true, limit: 10},
		"v2 folder cursor":    {bucket: "big", match: "p/", fold: true, foldFrom: 2, limit: 11, after: &listCursor{Name: "p/f100/"}},
		"v2 desc file cursor": {bucket: "big", match: "p/", fold: true, foldFrom: 2, desc: true, limit: 11, after: &listCursor{Name: "p/top3.txt"}},
		"v2 no delimiter":     {bucket: "big", match: "p/f2", limit: 11},
		"v1 empty prefix":     {bucket: "big", match: "", fold: true, caseFold: true, limit: 100},
	}
}

func assertBounded(t *testing.T, db queryer, ctx context.Context, shapes map[string]listQuery) {
	t.Helper()
	for name, q := range shapes {
		q.lowerCol = true
		sql, args := q.sql()
		root := explainPlan(t, db, ctx, name, sql, args)
		raw, _ := json.Marshal(root)
		assert.NotContains(t, string(raw), "Seq Scan", name)
		assert.Contains(t, string(raw), "_c_idx", "%s uses a COLLATE \"C\" index", name)
		t.Logf("%s: %v rows read", name, scannedRows(root))
		assert.Less(t, scannedRows(root), float64(200), "%s reads only the rows it emits, not the 50k under the prefix", name)
	}
}

func TestStorageList_SkipScanIsBoundedOn50kObjects(t *testing.T) {
	owner, _ := listDB(t, plainBuckets)
	seedBig(t, owner)
	assertBounded(t, owner, context.Background(), boundedShapes())

	w := serve(routerFor(owner), "POST", "/storage/v1/object/list/big", `{"prefix":"p","limit":3,"offset":398}`, nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	var items []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &items))
	assert.Equal(t, []string{"f399", "f400", "top1.txt"}, names(items))
}

// The RLS policy runs before any non-leakproof qual, so the walk must key on leakproof comparisons only.
func TestStorageList_SkipScanIsBoundedUnderRLS(t *testing.T) {
	buckets := map[string]domain.Bucket{"b": {}, "big": {RLS: []domain.RLSPolicy{{Operations: []string{"select"}, Using: "octet_length(name) > 0"}}}}
	owner, req := listDB(t, buckets)
	seedBig(t, owner)

	ctx, err := req.WithRLS(context.Background(), domain.Session{Role: "authenticated", IsAuthenticated: true})
	require.NoError(t, err)
	tx, err := req.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	assertBounded(t, tx, ctx, boundedShapes())
}

func TestStorageList_V1KeepsNamesThatDifferOnlyByCase(t *testing.T) {
	owner, _ := listDB(t, plainBuckets)
	seedObjects(t, owner, "b", time.Now(), "p/a.txt", "p/A.txt", "p/z/1.txt", "p/Z/2.txt", "p/b.txt")
	r := routerFor(owner)

	got, _ := v1Names(t, r, `{"prefix":"p/"}`)
	assert.Equal(t, []string{"A.txt", "a.txt", "b.txt", "z"}, got, "asc keeps both cases, and case-variant folders fold once")

	got, _ = v1Names(t, r, `{"prefix":"p/","sortBy":{"column":"name","order":"desc"}}`)
	assert.Equal(t, []string{"Z", "b.txt", "a.txt", "A.txt"}, got, "desc keeps both cases")

	got, _ = v1Names(t, r, `{"prefix":"p/","limit":1,"offset":1}`)
	assert.Equal(t, []string{"a.txt"}, got, "offset counts each case variant")
}

func TestStorageList_V1WorksBeforeTheLowerColumnExists(t *testing.T) {
	owner, _ := listDB(t, plainBuckets)
	seedObjects(t, owner, "b", time.Now(), "p/a.txt", "p/A.txt", "p/b/1.txt")
	_, err := owner.Exec(context.Background(), `ALTER TABLE storage.objects DROP COLUMN name_lower`)
	require.NoError(t, err)

	got, _ := v1Names(t, routerFor(owner), `{"prefix":"p/"}`)
	assert.Equal(t, []string{"A.txt", "a.txt", "b"}, got)
}

func TestStorageList_V2TimeSortWithoutDelimiter(t *testing.T) {
	owner, _ := listDB(t, plainBuckets)
	seedObjects(t, owner, "b", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "d/a.txt", "d/z.txt", "d/sub/m.txt", "d/b.txt")
	r := routerFor(owner)

	resp := v2List(t, r, `{"prefix":"d/","sortBy":{"column":"created_at","order":"desc"},"limit":2}`)
	assert.Equal(t, []string{"d/b.txt", "d/sub/m.txt"}, names(resp.Objects), "newest first, no folders")
	assert.Empty(t, resp.Folders)
	require.True(t, resp.HasNext)
	resp = v2List(t, r, `{"prefix":"d/","sortBy":{"column":"created_at","order":"desc"},"cursor":"`+resp.NextCursor+`"}`)
	assert.Equal(t, []string{"d/z.txt", "d/a.txt"}, names(resp.Objects))

	resp = v2List(t, r, `{"prefix":"d/","sortBy":{"column":"updated_at"}}`)
	assert.Equal(t, []string{"d/a.txt", "d/z.txt", "d/sub/m.txt", "d/b.txt"}, names(resp.Objects), "oldest first by default")
}

func TestStorageList_V2ClampsForeignCursorToPrefix(t *testing.T) {
	owner, _ := listDB(t, plainBuckets)
	seedObjects(t, owner, "b", time.Now(), "a/1.txt", "p/1.txt", "p/2.txt", "z/1.txt")
	r := routerFor(owner)

	below := encodeListCursor(listCursor{Name: "a/1.txt"})
	resp := v2List(t, r, `{"prefix":"p/","cursor":"`+below+`"}`)
	assert.Equal(t, []string{"p/1.txt", "p/2.txt"}, names(resp.Objects), "a cursor below the prefix starts at the prefix")

	above := encodeListCursor(listCursor{Name: "z/1.txt"})
	resp = v2List(t, r, `{"prefix":"p/","sortBy":{"order":"desc"},"cursor":"`+above+`"}`)
	assert.Equal(t, []string{"p/2.txt", "p/1.txt"}, names(resp.Objects), "desc: a cursor above the prefix starts at its top")

	resp = v2List(t, r, `{"prefix":"p/","cursor":"`+above+`"}`)
	assert.Empty(t, resp.Objects, "asc: a cursor above the prefix is the end")
	resp = v2List(t, r, `{"prefix":"p/","sortBy":{"order":"desc"},"cursor":"`+below+`"}`)
	assert.Empty(t, resp.Objects, "desc: a cursor below the prefix is the end")
}

// The fallback must return exactly what the name_lower path returns.
func TestStorageList_FallbackMatchesTheColumnPath(t *testing.T) {
	owner, _ := listDB(t, plainBuckets)
	seedObjects(t, owner, "b", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		"top.txt", "a-b.txt", "a/x.txt", "a/y/z.txt", "B.txt", "b/c.txt", "p/a.txt", "p/A.txt", "p/z/1.txt", "p/Z/2.txt", "p/b.txt",
		"dir/file.txt", "dir/sub/deep.txt", "dir/Sub2/q.txt", "dir/zz.txt", "we_ird%/p.txt", "ünï/c.txt", "ÜNÏ/d.txt",
		"kelvin/a.txt", "Kelvin/b.txt", "Dir/Other.txt", "p/", "quote'/it.txt", "İnbox/a.txt", "inbox/b.txt", "Inbox/c.txt")
	r := routerFor(owner)
	bodies := []string{
		`{}`, `{"prefix":"p"}`, `{"prefix":"p/"}`, `{"prefix":"P/"}`, `{"prefix":"dir/"}`, `{"prefix":"DIR/"}`, `{"prefix":"dir/","search":"S"}`,
		`{"prefix":"a"}`, `{"prefix":"we_"}`, `{"prefix":"ünï/"}`, `{"prefix":"ÜNÏ/"}`, `{"prefix":"kelvin/"}`, `{"prefix":"k"}`, `{"search":"quote'"}`,
		`{"prefix":"nope/"}`, `{"prefix":"inbox/"}`, `{"prefix":"İnbox/"}`, `{"prefix":"i"}`, `{"prefix":"p/","limit":2}`, `{"prefix":"p/","limit":2,"offset":2}`, `{"prefix":"p/","limit":1,"offset":99}`,
		`{"prefix":"p/","sortBy":{"column":"name","order":"desc"}}`, `{"prefix":"","sortBy":{"column":"name","order":"desc"},"limit":3,"offset":1}`,
		`{"prefix":"dir/","sortBy":{"column":"name","order":"desc"}}`,
	}
	want := make([][]map[string]any, len(bodies))
	for i, b := range bodies {
		_, want[i] = v1Names(t, r, b)
	}

	_, err := owner.Exec(context.Background(), `ALTER TABLE storage.objects DROP COLUMN name_lower`)
	require.NoError(t, err)
	for i, b := range bodies {
		_, got := v1Names(t, r, b)
		assert.Equal(t, want[i], got, b)
	}
}

func TestStorageList_FallbackReadsOnlyThePrefix(t *testing.T) {
	owner, _ := listDB(t, plainBuckets)
	seedBig(t, owner)
	_, err := owner.Exec(context.Background(), `ALTER TABLE storage.objects DROP COLUMN name_lower`)
	require.NoError(t, err)

	for _, match := range []string{"p/f399/", "P/F399/", "p/f399/x0"} {
		q := listQuery{bucket: "big", match: match, fold: true, foldFrom: len(match), caseFold: true, limit: 10}
		sql, args := q.sql()
		root := explainPlan(t, owner, context.Background(), match, sql, args)
		raw, _ := json.Marshal(root)
		assert.False(t, strings.Contains(string(raw), "Seq Scan"), "%s seq scans", match)
		assert.True(t, strings.Contains(string(raw), "objects_bucket_name_c_idx"), "%s skips the name index", match)
		assert.Less(t, scannedRows(root), float64(500), "%s reads its prefix, not the 50k rows in the bucket", match)
	}
}

// A long letter prefix must seek on its leading letters, not scan the bucket.
func TestStorageList_FallbackSeeksALongLetterPrefix(t *testing.T) {
	owner, _ := listDB(t, plainBuckets)
	seedBig(t, owner)
	_, err := owner.Exec(context.Background(), `INSERT INTO storage.objects (bucket_id, name) SELECT 'big', format('users/abcdef/f%s.txt', i) FROM generate_series(1, 20) i;
INSERT INTO storage.objects (bucket_id, name) SELECT 'big', format('users/abczzz/f%s.txt', i) FROM generate_series(1, 20) i;
INSERT INTO storage.objects (bucket_id, name) SELECT 'big', format('userz/f%s.txt', i) FROM generate_series(1, 5000) i;
ANALYZE storage.objects;
ALTER TABLE storage.objects DROP COLUMN name_lower`)
	require.NoError(t, err)

	for _, match := range []string{"users/abcdef/", "USERS/ABCDEF/"} {
		q := listQuery{bucket: "big", match: match, fold: true, foldFrom: len(match), caseFold: true, limit: 10}
		sql, args := q.sql()
		root := explainPlan(t, owner, context.Background(), match, sql, args)
		raw, _ := json.Marshal(root)
		assert.NotContains(t, string(raw), "Seq Scan", match)
		assert.Contains(t, string(raw), "objects_bucket_name_c_idx", match)
		assert.Less(t, scannedRows(root), float64(500), "%s reads users/a*, not the 5k userz/ rows a 4-rune seek would read", match)
	}
}
