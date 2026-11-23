package cloud

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func srvClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return NewClient(s.URL, "tok")
}

func TestResolveSite(t *testing.T) {
	c := srvClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/instancez/sites", r.URL.Path)
		_, _ = w.Write([]byte(`{"sites":[{"Id":"a1","Slug":"blog"},{"Id":"b2","Slug":"a1"}]}`))
	})
	s, err := c.ResolveSite("a1")
	require.NoError(t, err)
	assert.Equal(t, "a1", s.ID, "id wins over a slug that looks like an id")
	s, err = c.ResolveSite("blog")
	require.NoError(t, err)
	assert.Equal(t, "a1", s.ID)
	_, err = c.ResolveSite("nope")
	assert.ErrorContains(t, err, `no site with id or slug "nope"`)
	_, err = c.ResolveSite("  ")
	assert.Error(t, err)
}

func TestResolveSite_EmptyListAndAPIError(t *testing.T) {
	c := srvClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"sites":[]}`)) })
	_, err := c.ResolveSite("x")
	assert.Error(t, err)
	c = srvClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":"feature_not_available"}`))
	})
	_, err = c.ResolveSite("x")
	assert.ErrorContains(t, err, "feature_not_available")
}

func TestMintSiteUpload(t *testing.T) {
	var body string
	c := srvClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/mcp", r.URL.Path)
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"isError":false,"content":[{"type":"text","text":"{\"upload_url\":\"https://h/up?token=t\",\"expires_at\":\"2030-01-01T00:00:00Z\"}"}]}}`))
	})
	up, err := c.MintSiteUpload("s1")
	require.NoError(t, err)
	assert.Equal(t, "https://h/up?token=t", up.UploadURL)
	assert.Contains(t, body, `"method":"tools/call"`)
	assert.Contains(t, body, `"name":"create_site_upload"`)
	assert.Contains(t, body, `"site_id":"s1"`)
}

func TestMintSiteUpload_APIPrefixedBaseUsesRootMCP(t *testing.T) {
	var path string
	c := srvClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"result":{"content":[{"text":"{\"upload_url\":\"https://h/up\"}"}]}}`))
	})
	c.BaseURL += "/api"
	_, err := c.MintSiteUpload("s1")
	require.NoError(t, err)
	assert.Equal(t, "/mcp", path)
}

func TestMintSiteUpload_Errors(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   string
	}{
		"tool error":    {200, `{"result":{"isError":true,"content":[{"type":"text","text":"site not found"}]}}`, "site not found"},
		"rpc error":     {200, `{"error":{"code":-32601,"message":"method not found"}}`, "method not found"},
		"http 401":      {401, `{"error":"unauthorized"}`, "unauthorized"},
		"empty result":  {200, `{"result":{"content":[]}}`, "empty response"},
		"no url":        {200, `{"result":{"content":[{"text":"{}"}]}}`, "no upload_url"},
		"non-json text": {200, `{"result":{"content":[{"text":"hi"}]}}`, "no upload_url"},
		"null result":   {200, `{"jsonrpc":"2.0","id":1}`, "empty response"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := srvClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			_, err := c.MintSiteUpload("s1")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestUploadSiteArchive(t *testing.T) {
	var got string
	var cl int64
	var edge, ctype string
	c := srvClient(t, func(w http.ResponseWriter, r *http.Request) {
		edge, ctype = r.Header.Get("X-Edge"), r.Header.Get("Content-Type")
		assert.Equal(t, "PUT", r.Method)
		assert.Empty(t, r.Header.Get("Authorization"))
		cl = r.ContentLength
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		_, _ = w.Write([]byte(`{"deploy_id":"d1","site_id":"s1","files":3,"url":"https://x"}`))
	})
	c.Headers = http.Header{"X-Edge": {"1"}}
	res, err := c.UploadSiteArchive(c.BaseURL+"/up?token=t", strings.NewReader("abc"), 3)
	require.NoError(t, err)
	assert.Equal(t, "abc", got)
	assert.Equal(t, "1", edge)
	assert.Equal(t, "application/gzip", ctype)
	assert.EqualValues(t, 3, cl)
	assert.Equal(t, "d1", res.DeployID)
	assert.Equal(t, 3, res.Files)
}

func TestUploadSiteArchive_Rejections(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   string
	}{
		"expired token": {401, `{"error":"token expired"}`, "(401): token expired"},
		"bad bundle":    {400, `{"error":"index.html required"}`, "(400): index.html required"},
		"too large":     {413, ``, "(413): Request Entity Too Large"},
		"plain text":    {502, `bad gateway`, "(502): bad gateway"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := srvClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			_, err := c.UploadSiteArchive(c.BaseURL+"/up?token=SECRET", strings.NewReader("x"), 1)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.NotContains(t, err.Error(), "SECRET")
		})
	}
}

func TestUploadSiteArchive_TransportErrorHidesToken(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", "")
	_, err := c.UploadSiteArchive("http://127.0.0.1:1/up?token=SECRET", strings.NewReader("x"), 1)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRET")
}

func TestUploadSiteArchive_Any2xxIsSuccess(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"empty body": {200, ""},
		"204":        {204, ""},
		"html body":  {200, "<html>ok</html>"},
	} {
		t.Run(name, func(t *testing.T) {
			c := srvClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			res, err := c.UploadSiteArchive(c.BaseURL+"/up?token=t", strings.NewReader("x"), 1)
			require.NoError(t, err)
			assert.Empty(t, res.URL)
		})
	}
}

func TestUploadSiteArchive_RedirectIsError(t *testing.T) {
	hit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		_, _ = w.Write([]byte("<html>login</html>"))
	}))
	defer target.Close()
	for _, code := range []int{301, 302, 307, 308} {
		c := srvClient(t, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL, code)
		})
		_, err := c.UploadSiteArchive(c.BaseURL+"/up?token=SECRET", strings.NewReader("x"), 1)
		require.Error(t, err, code)
		assert.Contains(t, err.Error(), "upload rejected")
		assert.NotContains(t, err.Error(), "SECRET")
	}
	assert.False(t, hit, "redirect must not be followed")
}

func TestUploadSiteArchive_StalledServerTimesOut(t *testing.T) {
	release := make(chan struct{})
	c := srvClient(t, func(w http.ResponseWriter, r *http.Request) { <-release })
	t.Cleanup(func() { close(release) })
	c.UploadTimeout = 100 * time.Millisecond
	start := time.Now()
	_, err := c.UploadSiteArchive(c.BaseURL+"/up?token=SECRET", strings.NewReader("x"), 1)
	require.Error(t, err)
	assert.Less(t, time.Since(start), 5*time.Second)
	assert.NotContains(t, err.Error(), "SECRET")
}

func TestUploadSiteArchive_RejectionEchoingURLIsDropped(t *testing.T) {
	for name, body := range map[string]string{
		"plain": "bad request for /up?token=SECRET",
		"json":  `{"error":"cannot use /up?token=SECRET"}`,
		"token": "token SECRET is invalid",
	} {
		t.Run(name, func(t *testing.T) {
			c := srvClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(403)
				_, _ = w.Write([]byte(body))
			})
			_, err := c.UploadSiteArchive(c.BaseURL+"/up?token=SECRET", strings.NewReader("x"), 1)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "SECRET")
			assert.Contains(t, err.Error(), "(403): Forbidden")
		})
	}
}

func TestUploadSiteArchive_NeverForwardsCredentialHeaders(t *testing.T) {
	var got http.Header
	h := func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`{}`))
	}
	check := func(t *testing.T, c *Client) {
		t.Helper()
		got = nil
		_, err := c.UploadSiteArchive(c.BaseURL+"/up?token=t", strings.NewReader("x"), 1)
		require.NoError(t, err)
		assert.Empty(t, got.Values("Authorization"))
		assert.Empty(t, got.Values("Cookie"))
		assert.Empty(t, got.Values("Proxy-Authorization"))
		assert.Equal(t, "1", got.Get("X-Edge"))
	}

	t.Run("explicit headers", func(t *testing.T) {
		c := srvClient(t, h)
		c.Headers = http.Header{}
		c.Headers["authorization"] = []string{"Bearer SECRET"}
		c.Headers.Set("COOKIE", "s=SECRET")
		c.Headers.Set("proxy-authorization", "Basic SECRET")
		c.Headers.Set("X-Edge", "1")
		check(t, c)
	})
	t.Run("env headers", func(t *testing.T) {
		t.Setenv("INSTANCEZ_CLOUD_HEADERS", "authorization: Bearer SECRET\nCookie: s=SECRET\nX-Edge: 1")
		c := srvClient(t, h)
		c.Headers = ExtraHeaders()
		require.NotEmpty(t, c.Headers)
		check(t, c)
	})
}
