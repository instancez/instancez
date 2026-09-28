package http

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseUserMetadata(t *testing.T) {
	big := `{"k":"` + strings.Repeat("a", maxUserMetadataBytes-8) + `"}`
	require.Len(t, big, maxUserMetadataBytes)
	cases := []struct {
		in, want string
		err      error
	}{
		{"", "{}", nil},
		{"null", "{}", nil},
		{" null ", "{}", nil},
		{`{}`, "{}", nil},
		{`{"owner":"ana","n":1,"nested":{"x":[true]}}`, `{"owner":"ana","n":1,"nested":{"x":[true]}}`, nil},
		{`{"emoji":"✓"}`, `{"emoji":"✓"}`, nil},
		{big, big, nil},
		{big + " ", "", errUserMetadataTooLarge},
		{`[1,2]`, "", errInvalidUserMetadata},
		{`"str"`, "", errInvalidUserMetadata},
		{`42`, "", errInvalidUserMetadata},
		{`{bad`, "", errInvalidUserMetadata},
		{`{"a":1} trailing`, "", errInvalidUserMetadata},
	}
	for _, tc := range cases {
		got, err := parseUserMetadata([]byte(tc.in))
		assert.ErrorIs(t, err, tc.err, tc.in)
		assert.Equal(t, tc.want, got, tc.in)
	}
}

// uploadCapture mounts upload routes and records the last storage.objects write.
func uploadCapture(t *testing.T) (*gin.Engine, *StorageV1Handler, *[]any, *string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var args []any
	var query string
	exec := func(_ context.Context, q string, a ...any) (int64, error) {
		if strings.Contains(q, "storage.objects") {
			query, args = q, a
		}
		return 1, nil
	}
	db := &stubDB{execFn: exec, beginFn: func(context.Context) (domain.Tx, error) { return &stubTx{execFn: exec}, nil }}
	h := newStorageHandler(db, &stubObjectStore{}, map[string]domain.Bucket{"b": {}})
	h.jwtKeys = stubKeys(t)
	r := gin.New()
	auth := func(c *gin.Context) { asAuthenticated(c) }
	r.POST("/storage/v1/object/:bucket/*path", auth, h.uploadObject)
	r.PUT("/storage/v1/object/:bucket/*path", auth, h.updateObject)
	r.PUT("/storage/v1/object/upload/sign/:bucket/*path", h.uploadToSignedURL)
	return r, h, &args, &query
}

func metaMultipart(t *testing.T, fields [][2]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, f := range fields {
		require.NoError(t, mw.WriteField(f[0], f[1]))
	}
	fw, err := mw.CreateFormFile("", "blob")
	require.NoError(t, err)
	_, _ = fw.Write([]byte("file bytes"))
	require.NoError(t, mw.Close())
	return &buf, mw.FormDataContentType()
}

func userMetaArg(t *testing.T, args []any) any {
	t.Helper()
	require.Len(t, args, 7)
	return args[6]
}

func TestUpload_UserMetadataTransports(t *testing.T) {
	meta := `{"owner":"ana","emoji":"✓"}`
	b64 := base64.StdEncoding.EncodeToString([]byte(meta))
	for name, tc := range map[string]struct {
		method, path string
		body         func() (*bytes.Buffer, string)
		header       string
		want         string
	}{
		"x-metadata header": {"POST", "/storage/v1/object/b/a.txt", func() (*bytes.Buffer, string) {
			return bytes.NewBufferString("hi"), "text/plain"
		}, b64, meta},
		"multipart metadata field": {"POST", "/storage/v1/object/b/a.txt", func() (*bytes.Buffer, string) {
			return metaMultipart(t, [][2]string{{"cacheControl", "3600"}, {"metadata", meta}})
		}, "", meta},
		"multipart userMetadata field": {"PUT", "/storage/v1/object/b/a.txt", func() (*bytes.Buffer, string) {
			return metaMultipart(t, [][2]string{{"userMetadata", meta}})
		}, "", meta},
		"metadata wins over userMetadata": {"POST", "/storage/v1/object/b/a.txt", func() (*bytes.Buffer, string) {
			return metaMultipart(t, [][2]string{{"userMetadata", `{"x":1}`}, {"metadata", meta}})
		}, "", meta},
		"none stores an empty object": {"POST", "/storage/v1/object/b/a.txt", func() (*bytes.Buffer, string) {
			return bytes.NewBufferString("hi"), "text/plain"
		}, "", "{}"},
	} {
		r, _, args, _ := uploadCapture(t)
		body, ct := tc.body()
		req := httptest.NewRequest(tc.method, tc.path, body)
		req.Header.Set("Content-Type", ct)
		if tc.header != "" {
			req.Header.Set("x-metadata", tc.header)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, "%s: %s", name, w.Body.String())
		assert.JSONEq(t, tc.want, userMetaArg(t, *args).(string), name)
	}
}

func TestUpload_SignedURLKeepsUserMetadata(t *testing.T) {
	r, h, args, _ := uploadCapture(t)
	tok := h.signUploadToken("b", "a.txt", "")
	body, ct := metaMultipart(t, [][2]string{{"metadata", `{"via":"signed"}`}})
	req := httptest.NewRequest("PUT", "/storage/v1/object/upload/sign/b/a.txt?token="+tok, body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.JSONEq(t, `{"via":"signed"}`, userMetaArg(t, *args).(string))
}

func TestUpload_BadUserMetadata(t *testing.T) {
	huge := `{"k":"` + strings.Repeat("a", maxUserMetadataBytes) + `"}`
	for name, tc := range map[string]struct {
		header string
		fields [][2]string
		status int
		slug   string
	}{
		"header not base64":            {header: "%%%", status: 400, slug: "invalid_metadata"},
		"header not json":              {header: base64.StdEncoding.EncodeToString([]byte("{bad")), status: 400, slug: "invalid_metadata"},
		"header json array":            {header: base64.StdEncoding.EncodeToString([]byte("[1]")), status: 400, slug: "invalid_metadata"},
		"header too large":             {header: base64.StdEncoding.EncodeToString([]byte(huge)), status: 413, slug: "payload_too_large"},
		"field not json":               {fields: [][2]string{{"metadata", "{bad"}}, status: 400, slug: "invalid_metadata"},
		"field json string":            {fields: [][2]string{{"metadata", `"x"`}}, status: 400, slug: "invalid_metadata"},
		"field too large":              {fields: [][2]string{{"metadata", huge}}, status: 413, slug: "payload_too_large"},
		"userMetadata field too large": {fields: [][2]string{{"userMetadata", huge}}, status: 413, slug: "payload_too_large"},
	} {
		r, _, args, _ := uploadCapture(t)
		var req *http.Request
		if tc.fields != nil {
			body, ct := metaMultipart(t, tc.fields)
			req = httptest.NewRequest("POST", "/storage/v1/object/b/a.txt", body)
			req.Header.Set("Content-Type", ct)
		} else {
			req = httptest.NewRequest("POST", "/storage/v1/object/b/a.txt", strings.NewReader("hi"))
			req.Header.Set("Content-Type", "text/plain")
			req.Header.Set("x-metadata", tc.header)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, tc.status, w.Code, name)
		assert.Contains(t, w.Body.String(), tc.slug, name)
		assert.Nil(t, *args, "%s: nothing is written", name)
	}
}

func TestWriteObjectRow_ReplacesUserMetadata(t *testing.T) {
	for name, mode := range map[string][2]bool{"insert": {false, false}, "upsert": {false, true}, "update": {true, false}} {
		var q string
		var args []any
		db := &stubDB{execFn: func(_ context.Context, query string, a ...any) (int64, error) { q, args = query, a; return 1, nil }}
		require.NoError(t, writeObjectRow(context.Background(), db, objectRow{bucket: "b", name: "n"}, mode[0], mode[1]))
		assert.Equal(t, "{}", args[6], "%s: absent metadata resets to {}, as Supabase does", name)
		switch name {
		case "upsert":
			assert.Contains(t, q, "user_metadata = EXCLUDED.user_metadata", name)
		case "update":
			assert.Contains(t, q, "user_metadata = $7::jsonb", name)
		default:
			assert.Contains(t, q, "user_metadata)", name)
		}
	}
}

func TestListItems_SupabaseShape(t *testing.T) {
	uploaded := time.Date(2026, 1, 2, 3, 4, 5, 6_000_000, time.UTC)
	rows := []map[string]any{{
		"id": "7b0c5c86-0b0e-4a4d-9d4e-2b1f0c9d7a11", "name": "dir/a.txt", "uploaded_at": uploaded,
		"metadata": map[string]any{"size": 3, "mimetype": "text/plain"},
	}}
	db := &stubDB{queryFn: func(_ context.Context, q string, _ ...any) ([]map[string]any, error) {
		assert.Contains(t, q, "SELECT o.id, ")
		return rows, nil
	}}
	h := newStorageHandler(db, &stubObjectStore{}, map[string]domain.Bucket{"b": {}})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/storage/v1/object/list/:bucket", h.listObjects)
	r.POST("/storage/v1/object/list-v2/:bucket", h.listObjectsV2)

	check := func(item map[string]any, route string) {
		assert.Equal(t, "7b0c5c86-0b0e-4a4d-9d4e-2b1f0c9d7a11", item["id"], route)
		for _, k := range []string{"created_at", "updated_at", "last_accessed_at"} {
			assert.Equal(t, "2026-01-02T03:04:05.006Z", item[k], "%s %s", route, k)
		}
		assert.Equal(t, map[string]any{"size": float64(3), "mimetype": "text/plain"}, item["metadata"], route)
	}
	var v1 []map[string]any
	w := serve(r, "POST", "/storage/v1/object/list/b", `{"prefix":"dir/"}`, nil)
	require.Equal(t, 200, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v1))
	require.Len(t, v1, 1)
	assert.Equal(t, "a.txt", v1[0]["name"])
	check(v1[0], "list")

	var v2 struct{ Objects []map[string]any }
	w = serve(r, "POST", "/storage/v1/object/list-v2/b", `{"prefix":"dir/"}`, nil)
	require.Equal(t, 200, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v2))
	require.Len(t, v2.Objects, 1)
	check(v2.Objects[0], "list-v2")
}
