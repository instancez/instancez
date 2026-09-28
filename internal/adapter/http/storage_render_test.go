package http

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/instancez/instancez/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func renderHandler(t *testing.T, row map[string]any, buckets map[string]domain.Bucket) (*StorageV1Handler, *gin.Engine) {
	t.Helper()
	return renderHandlerDB(t, &stubDB{queryRowFn: func(context.Context, string, ...any) (map[string]any, error) { return row, nil }}, buckets)
}

func renderHandlerDB(t *testing.T, db *stubDB, buckets map[string]domain.Bucket) (*StorageV1Handler, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("INSTANCEZ_PUBLISHABLE_KEY", anonTestPK)
	store := &stubObjectStore{downloadFn: func(context.Context, string) (io.ReadCloser, string, error) {
		return io.NopCloser(bytes.NewReader(realPNG(t, 8, 8))), "image/png", nil
	}}
	h := newStorageHandler(db, store, buckets)
	h.jwtKeys = stubKeys(t)
	r := gin.New()
	h.Mount(r.Group(""))
	return h, r
}

func pngSize(t *testing.T, b []byte) (int, int) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	require.NoError(t, err)
	return img.Bounds().Dx(), img.Bounds().Dy()
}

var pubRow = map[string]any{"id": "1", "name": "a.png", "size": int64(123), "mime": "image/png", "uploaded_at": time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), "metadata": map[string]any{}}

func TestHeadPublicObject(t *testing.T) {
	_, r := renderHandler(t, pubRow, map[string]domain.Bucket{"pub": {Public: true}, "priv": {}})
	w := serve(r, "HEAD", "/storage/v1/object/public/pub/a.png", "", nil)
	require.Equal(t, 200, w.Code)
	assert.Equal(t, "123", w.Header().Get("Content-Length"))
	assert.Equal(t, "image/png", w.Header().Get("Content-Type"))
	assert.Equal(t, "Fri, 02 Jan 2026 03:04:05 GMT", w.Header().Get("Last-Modified"))
	assert.Equal(t, "public, max-age=3600", w.Header().Get("Cache-Control"))
	assert.Empty(t, w.Body.String())
	assert.Equal(t, 404, serve(r, "HEAD", "/storage/v1/object/public/priv/a.png", "", nil).Code, "private bucket answers like missing")
	assert.Equal(t, 404, serve(r, "HEAD", "/storage/v1/object/public/nope/a.png", "", nil).Code)
	assert.Equal(t, 400, serve(r, "HEAD", "/storage/v1/object/public/pub/%2e%2e/x", "", nil).Code)
	assert.Equal(t, 400, serve(r, "HEAD", "/storage/v1/object/public/pub", "", nil).Code)
}

func TestHeadPublicObject_Missing(t *testing.T) {
	_, r := renderHandler(t, nil, map[string]domain.Bucket{"pub": {Public: true}})
	assert.Equal(t, 404, serve(r, "HEAD", "/storage/v1/object/public/pub/gone.png", "", nil).Code)
}

func TestObjectInfoPublic(t *testing.T) {
	_, r := renderHandler(t, pubRow, map[string]domain.Bucket{"pub": {Public: true}, "priv": {}})
	w := serve(r, "GET", "/storage/v1/object/info/public/pub/a.png", "", nil)
	require.Equal(t, 200, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "a.png", body["name"])
	assert.Equal(t, "image/png", body["content_type"])
	assert.Equal(t, 404, serve(r, "GET", "/storage/v1/object/info/public/priv/a.png", "", nil).Code)
}

func TestRenderPublicAndAuthenticated(t *testing.T) {
	_, r := renderHandler(t, pubRow, map[string]domain.Bucket{"pub": {Public: true}, "open": {}})
	for _, m := range []string{"GET", "HEAD"} {
		w := serve(r, m, "/storage/v1/render/image/public/pub/a.png?width=3&height=5&resize=fill", "", nil)
		require.Equal(t, 200, w.Code, m)
		if m == "GET" {
			wd, ht := pngSize(t, w.Body.Bytes())
			assert.Equal(t, [2]int{3, 5}, [2]int{wd, ht})
		}
	}
	assert.Equal(t, 400, serve(r, "GET", "/storage/v1/render/image/public/pub/a.png?width=-1", "", nil).Code)
	anon := map[string]string{"apikey": anonTestPK, "Authorization": "Bearer " + anonTestPK}
	assert.Equal(t, 404, serve(r, "GET", "/storage/v1/render/image/authenticated/open/a.png?width=3", "", anon).Code, "anon + no rls: gated like downloads")
	svc := map[string]string{"apikey": "test-secret-key"}
	t.Setenv("INSTANCEZ_SECRET_KEY", "test-secret-key")
	w := serve(r, "GET", "/storage/v1/render/image/authenticated/open/a.png?width=2&height=2&resize=fill", "", svc)
	require.Equal(t, 200, w.Code, w.Body.String())
	wd, ht := pngSize(t, w.Body.Bytes())
	assert.Equal(t, [2]int{2, 2}, [2]int{wd, ht})
	assert.Equal(t, 404, serve(r, "GET", "/storage/v1/render/image/bogus/pub/a.png", "", nil).Code)
}

func TestRenderToken_SignedTransformRoundTrip(t *testing.T) {
	h, r := renderHandler(t, pubRow, map[string]domain.Bucket{"docs": {}})
	ctx := context.Background()
	tok := h.signRenderToken(ctx, "docs", "a.png", time.Hour, "height=5&resize=fill&width=3")
	require.Equal(t, 3, len(strings.Split(tok, ".")))
	w := serve(r, "GET", "/storage/v1/render/image/sign/docs/a.png?token="+tok+"&width=100", "", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	wd, ht := pngSize(t, w.Body.Bytes())
	assert.Equal(t, [2]int{3, 5}, [2]int{wd, ht}, "the token's transform wins over query params")

	plain := h.signDownloadToken(ctx, "docs", "a.png", time.Hour)
	w = serve(r, "GET", "/storage/v1/render/image/sign/docs/a.png?token="+plain, "", nil)
	require.Equal(t, 200, w.Code)
	wd, _ = pngSize(t, w.Body.Bytes())
	assert.Equal(t, 8, wd, "plain token serves the original")
}

func TestRenderToken_Confusion(t *testing.T) {
	h, r := renderHandler(t, pubRow, map[string]domain.Bucket{"docs": {}})
	ctx := context.Background()
	tok := h.signRenderToken(ctx, "docs", "a.png", time.Hour, "width=3")
	parts := strings.Split(tok, ".")
	forged := parts[0] + "." + parts[1] + "." + "d2lkdGg9MjUwMA" // base64url("width=2500")
	expired := h.signRenderToken(ctx, "docs", "a.png", -2*time.Second, "width=3")
	for name, target := range map[string]string{
		"transform token at /object/sign": "/storage/v1/object/sign/docs/a.png?token=" + tok,
		"tampered transform":              "/storage/v1/render/image/sign/docs/a.png?token=" + forged,
		"other path":                      "/storage/v1/render/image/sign/docs/b.png?token=" + tok,
		"expired":                         "/storage/v1/render/image/sign/docs/a.png?token=" + expired,
		"empty transform segment":         "/storage/v1/render/image/sign/docs/a.png?token=" + parts[0] + "." + parts[1] + ".",
		"four parts":                      "/storage/v1/render/image/sign/docs/a.png?token=" + tok + ".x",
		"no token":                        "/storage/v1/render/image/sign/docs/a.png",
	} {
		assert.Equal(t, 400, serve(r, "GET", target, "", nil).Code, name)
	}
	assert.Equal(t, h.signDownloadToken(ctx, "docs", "a.png", time.Hour)[:10], h.signRenderToken(ctx, "docs", "a.png", time.Hour, "")[:10], "empty transform = plain token")
}

func TestCreateSignedURL_WithTransform(t *testing.T) {
	h, r := renderHandler(t, map[string]any{"id": "1"}, map[string]domain.Bucket{"docs": {}})
	t.Setenv("INSTANCEZ_SECRET_KEY", "test-secret-key")
	svc := map[string]string{"apikey": "test-secret-key"}
	w := serve(r, "POST", "/storage/v1/object/sign/docs/a.png", `{"expiresIn":60,"transform":{"width":3,"height":5,"resize":"fill"}}`, svc)
	require.Equal(t, 200, w.Code, w.Body.String())
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	prefix := "/render/image/sign/docs/a.png?token="
	require.True(t, strings.HasPrefix(body["signedURL"], prefix), body["signedURL"])
	tf, _, ok := h.verifyRenderToken(context.Background(), strings.TrimPrefix(body["signedURL"], prefix), "docs", "a.png")
	require.True(t, ok)
	assert.Equal(t, "height=5&resize=fill&width=3", tf)

	w = serve(r, "POST", "/storage/v1/object/sign/docs/a.png", `{"expiresIn":60,"transform":{}}`, svc)
	require.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), `"/object/sign/docs/a.png?token=`, "empty transform signs a plain URL")
	assert.Equal(t, 400, serve(r, "POST", "/storage/v1/object/sign/docs/a.png", `{"expiresIn":60,"transform":{"width":-1}}`, svc).Code)
	assert.Equal(t, 400, serve(r, "POST", "/storage/v1/object/sign/docs/a.png", `{"expiresIn":60,"transform":{"width":"big"}}`, svc).Code)
	assert.Equal(t, 400, serve(r, "POST", "/storage/v1/object/sign/docs/a.png", `{"expiresIn":"abc","transform":{"width":"big"}}`, svc).Code, "a bad expiresIn does not hide a bad transform")
	assert.Equal(t, 400, serve(r, "POST", "/storage/v1/object/sign/docs/a.png", `{"transform":"width=3"}`, svc).Code)
	w = serve(r, "POST", "/storage/v1/object/sign/docs/a.png", `{"expiresIn":60,"transform":null}`, svc)
	require.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), `"/object/sign/docs/a.png?token=`)
}

func TestRenderToken_UploadDownloadNeverSwap(t *testing.T) {
	h, r := renderHandler(t, pubRow, map[string]domain.Bucket{"docs": {}})
	ctx := context.Background()
	up := h.signUploadToken("docs", "a.png", "")
	for name, tok := range map[string]string{
		"upload token":            up,
		"upload token, owner set": h.signUploadToken("docs", "a.png", "00000000-0000-0000-0000-000000000001"),
	} {
		assert.Equal(t, 400, serve(r, "GET", "/storage/v1/render/image/sign/docs/a.png?token="+tok, "", nil).Code, name)
		assert.Equal(t, 400, serve(r, "GET", "/storage/v1/object/sign/docs/a.png?token="+tok, "", nil).Code, name)
	}
	for name, tok := range map[string]string{
		"plain download token": h.signDownloadToken(ctx, "docs", "a.png", time.Hour),
		"transform token":      h.signRenderToken(ctx, "docs", "a.png", time.Hour, "width=3"),
	} {
		w := serve(r, "PUT", "/storage/v1/object/upload/sign/docs/a.png?token="+tok, "x", nil)
		assert.Equal(t, 400, w.Code, name)
		assert.Contains(t, w.Body.String(), "invalid_token", name)
	}
}

func TestRenderPublic_PrivateBucketNeverQueries(t *testing.T) {
	db := &stubDB{
		queryRowFn: func(context.Context, string, ...any) (map[string]any, error) {
			t.Error("render/public on a private bucket must not query")
			return pubRow, nil
		},
		withRLSFn: func(ctx context.Context, s domain.Session) (context.Context, error) {
			t.Errorf("render/public on a private bucket must not take a %q context", s.Role)
			return ctx, nil
		},
	}
	_, r := renderHandlerDB(t, db, map[string]domain.Bucket{"priv": {}})
	for _, m := range []string{"GET", "HEAD"} {
		w := serve(r, m, "/storage/v1/render/image/public/priv/a.png?width=3", "", nil)
		assert.Equal(t, 404, w.Code, m)
	}
}

func TestRenderAuthenticated_RunsUnderCallerRLS(t *testing.T) {
	var roles []string
	db := &stubDB{
		queryRowFn: func(context.Context, string, ...any) (map[string]any, error) { return pubRow, nil },
		withRLSFn: func(ctx context.Context, s domain.Session) (context.Context, error) {
			roles = append(roles, s.Role)
			return ctx, nil
		},
	}
	h, r := renderHandlerDB(t, db, map[string]domain.Bucket{"open": {}})
	tok := signToken(t, h.jwtKeys, jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001", "role": "authenticated", "exp": 4102444800})
	w := serve(r, "GET", "/storage/v1/render/image/authenticated/open/a.png?width=2&height=2&resize=fill", "", map[string]string{"apikey": anonTestPK, "Authorization": "Bearer " + tok})
	require.Equal(t, 200, w.Code, w.Body.String())
	wd, ht := pngSize(t, w.Body.Bytes())
	assert.Equal(t, [2]int{2, 2}, [2]int{wd, ht})
	assert.Equal(t, []string{"authenticated"}, roles, "the lookup runs as the caller, never service_role")
}

func TestCreateSignedURL_RejectsUnservableTransform(t *testing.T) {
	_, r := renderHandler(t, map[string]any{"id": "1"}, map[string]domain.Bucket{"docs": {}})
	t.Setenv("INSTANCEZ_SECRET_KEY", "test-secret-key")
	svc := map[string]string{"apikey": "test-secret-key"}
	for _, tf := range []string{
		`{"width":3,"format":"webp"}`, `{"width":3,"format":"avif"}`, `{"width":3,"format":"gif"}`,
		`{"width":3,"quality":19}`, `{"width":3,"quality":101}`, `{"width":3,"quality":-1}`, `{"width":3,"resize":"stretch"}`,
	} {
		w := serve(r, "POST", "/storage/v1/object/sign/docs/a.png", `{"expiresIn":60,"transform":`+tf+`}`, svc)
		assert.Equal(t, 400, w.Code, tf)
		assert.Contains(t, w.Body.String(), "invalid_transform", tf)
	}
	for _, tf := range []string{`{"width":3,"quality":20}`, `{"width":3,"quality":100,"format":"origin","resize":"contain"}`, `{"width":3,"format":"png"}`} {
		w := serve(r, "POST", "/storage/v1/object/sign/docs/a.png", `{"expiresIn":60,"transform":`+tf+`}`, svc)
		require.Equal(t, 200, w.Code, tf)
		assert.Contains(t, w.Body.String(), "/render/image/sign/", tf)
	}
	w := serve(r, "POST", "/storage/v1/object/sign/docs/a.png", `{"expiresIn":60,"transform":{"resize":"fill"}}`, svc)
	require.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), `"/object/sign/docs/a.png?token=`, "a transform without dimensions renders nothing, so it signs a plain URL")
}

func TestRenderSigned_ExpiresHeader(t *testing.T) {
	h, r := renderHandler(t, pubRow, map[string]domain.Bucket{"docs": {}, "pub": {Public: true}})
	ctx := context.Background()
	for name, tok := range map[string]string{
		"transform": h.signRenderToken(ctx, "docs", "a.png", time.Hour, "width=3"),
		"plain":     h.signDownloadToken(ctx, "docs", "a.png", time.Hour),
	} {
		exp, err := strconv.ParseInt(strings.Split(tok, ".")[0], 10, 64)
		require.NoError(t, err)
		w := serve(r, "GET", "/storage/v1/render/image/sign/docs/a.png?token="+tok, "", nil)
		require.Equal(t, 200, w.Code, name)
		assert.Equal(t, time.Unix(exp, 0).UTC().Format(http.TimeFormat), w.Header().Get("Expires"), name)
		assert.Empty(t, w.Header().Get("Cache-Control"), name)
	}
	w := serve(r, "GET", "/storage/v1/render/image/public/pub/a.png?width=3", "", nil)
	assert.Empty(t, w.Header().Get("Expires"), "only signed renders expire")
	assert.Equal(t, "public, max-age=3600", w.Header().Get("Cache-Control"))
}

var taggedRow = map[string]any{
	"id": "1", "name": "a.png", "size": int64(123), "mime": "image/png",
	"uploaded_at":   time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	"metadata":      map[string]any{"eTag": `"abc"`, "cacheControl": "max-age=60"},
	"user_metadata": `{"owner":"ana","tags":["x"]}`,
}

func TestObjectInfo_SupabaseShape(t *testing.T) {
	_, r := renderHandler(t, taggedRow, map[string]domain.Bucket{"pub": {Public: true}})
	var body map[string]any
	w := serve(r, "GET", "/storage/v1/object/info/public/pub/a.png", "", nil)
	require.Equal(t, 200, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "pub", body["bucket_id"])
	assert.Equal(t, `"abc"`, body["etag"])
	assert.Equal(t, "max-age=60", body["cache_control"])
	for _, k := range []string{"created_at", "updated_at", "last_modified"} {
		assert.Equal(t, "2026-01-02T03:04:05.000Z", body[k], k)
	}
	assert.Equal(t, map[string]any{"owner": "ana", "tags": []any{"x"}}, body["metadata"], "metadata is user_metadata, as in Supabase")
	assert.NotContains(t, body, "version", "no object versions, so no version field")

	_, r = renderHandler(t, pubRow, map[string]domain.Bucket{"pub": {Public: true}})
	body = nil
	require.NoError(t, json.Unmarshal(serve(r, "GET", "/storage/v1/object/info/public/pub/a.png", "", nil).Body.Bytes(), &body))
	assert.Contains(t, body, "etag")
	assert.Nil(t, body["etag"], "no stored eTag answers null")
	assert.Nil(t, body["cache_control"])
	assert.Contains(t, body, "metadata")
	assert.Nil(t, body["metadata"], "the storage blob never leaks as metadata")
}

func TestHeadHeaders_ETagAndRenderLength(t *testing.T) {
	_, r := renderHandler(t, taggedRow, map[string]domain.Bucket{"pub": {Public: true}})
	assert.Equal(t, 404, serve(r, "HEAD", "/storage/v1/object/info/public/pub/a.png", "", nil).Code, "Supabase has no HEAD info/public")
	w := serve(r, "HEAD", "/storage/v1/object/public/pub/a.png", "", nil)
	require.Equal(t, 200, w.Code)
	assert.Equal(t, `"abc"`, w.Header().Get("ETag"))

	get := serve(r, "GET", "/storage/v1/render/image/public/pub/a.png?width=3&height=5&resize=fill", "", nil)
	require.Equal(t, 200, get.Code)
	head := serve(r, "HEAD", "/storage/v1/render/image/public/pub/a.png?width=3&height=5&resize=fill", "", nil)
	require.Equal(t, 200, head.Code)
	assert.Equal(t, strconv.Itoa(get.Body.Len()), head.Header().Get("Content-Length"))
	assert.Equal(t, `"abc"`, head.Header().Get("ETag"))
	assert.Equal(t, "123", serve(r, "HEAD", "/storage/v1/render/image/public/pub/a.png", "", nil).Header().Get("Content-Length"), "untransformed HEAD uses the stored size")

	_, r = renderHandler(t, pubRow, map[string]domain.Bucket{"pub": {Public: true}})
	assert.Empty(t, serve(r, "HEAD", "/storage/v1/object/public/pub/a.png", "", nil).Header().Get("ETag"), "no stored eTag, no header")
}

func TestIsoTime(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	for in, want := range map[any]any{
		time.Date(2026, 1, 2, 3, 4, 5, 999_999_999, time.UTC): "2026-01-02T03:04:05.999Z",
		time.Date(2026, 1, 1, 22, 0, 0, 0, ny):                "2026-01-02T03:00:00.000Z",
		time.Time{}:                                           nil,
		"2026-01-02T03:04:05Z":                                "2026-01-02T03:04:05Z",
		"":                                                    nil,
	} {
		assert.Equal(t, want, isoTime(in), in)
	}
	assert.Nil(t, isoTime(nil))
}
