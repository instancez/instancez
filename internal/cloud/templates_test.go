package cloud

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/instancez/instancez/pkg/templates"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tplServer(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, "")
}

func TestGetTemplateSuccess(t *testing.T) {
	var gotPath, gotAuth string
	c := tplServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"manifest":{"id":"blog","name":"Blog"},"files":{"instancez.yaml":"version: 1\n"}}`))
	})
	b, err := c.GetTemplate("blog")
	require.NoError(t, err)
	assert.Equal(t, "/public/templates/blog", gotPath)
	assert.Empty(t, gotAuth, "public route must not send a bearer")
	assert.Equal(t, "blog", b.Manifest.ID)
	assert.Equal(t, "version: 1\n", b.Files["instancez.yaml"])
}

func TestGetTemplateNotFoundCarriesSuggestions(t *testing.T) {
	c := tplServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":"template_not_found","suggestions":["blog"]}`))
	})
	_, err := c.GetTemplate("blgo")
	var nf *TemplateNotFoundError
	require.True(t, errors.As(err, &nf))
	assert.Equal(t, "blgo", nf.ID)
	assert.Equal(t, []string{"blog"}, nf.Suggestions)
}

func TestGetTemplateNotFoundWithoutSuggestions(t *testing.T) {
	c := tplServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":"template_not_found"}`))
	})
	_, err := c.GetTemplate("zzzz")
	var nf *TemplateNotFoundError
	require.True(t, errors.As(err, &nf))
	assert.NotNil(t, nf.Suggestions)
	assert.Empty(t, nf.Suggestions)
}

func TestGetTemplateErrors(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"500":       func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) },
		"429":       func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) },
		"bad json":  func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{not json`)) },
		"html page": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`<html>spa</html>`)) },
		"empty":     func(w http.ResponseWriter, r *http.Request) {},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			c := tplServer(t, h)
			b, err := c.GetTemplate("blog")
			assert.Error(t, err)
			assert.Nil(t, b)
		})
	}
}

func TestGetTemplateRejectsOversizeBody(t *testing.T) {
	c := tplServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"manifest":{},"files":{"instancez.yaml":"`))
		_, _ = w.Write([]byte(strings.Repeat("a", MaxTemplateBytes+10)))
		_, _ = w.Write([]byte(`"}}`))
	})
	_, err := c.GetTemplate("blog")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too large")
}

func TestGetTemplateRejectsInvalidIDBeforeNetwork(t *testing.T) {
	called := false
	c := tplServer(t, func(w http.ResponseWriter, r *http.Request) { called = true })
	for _, id := range []string{"", "../x", "Blog", "a/b", "a b", strings.Repeat("a", 40)} {
		_, err := c.GetTemplate(id)
		assert.Error(t, err, id)
	}
	assert.False(t, called)
}

func TestGetTemplateUnreachable(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", "")
	c.HTTP.Timeout = 2 * time.Second
	_, err := c.GetTemplate("blog")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "127.0.0.1:1")
}

func TestListTemplates(t *testing.T) {
	c := tplServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/public/templates", r.URL.Path)
		_, _ = w.Write([]byte(`{"templates":[{"id":"blog","tagline":"t","category":"content"}]}`))
	})
	got, err := c.ListTemplates()
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, templates.Manifest{ID: "blog", Tagline: "t", Category: "content"}, got[0])
}

func TestListTemplatesEmptyIsNotAnError(t *testing.T) {
	c := tplServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"templates":[]}`)) })
	got, err := c.ListTemplates()
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestGetTemplateSendsExtraHeaders(t *testing.T) {
	t.Setenv("INSTANCEZ_CLOUD_HEADERS", "X-Bypass: yes")
	var got string
	c := tplServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Bypass")
		_, _ = w.Write([]byte(`{"manifest":{"id":"blog"},"files":{"instancez.yaml":"x"}}`))
	})
	_, err := c.GetTemplate("blog")
	require.NoError(t, err)
	assert.Equal(t, "yes", got)
}

func TestPublicTemplateCallsDropOrgHeader(t *testing.T) {
	t.Setenv("INSTANCEZ_ORG", "org_1")
	var gotOrg []string
	c := tplServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotOrg = append(gotOrg, r.Header.Get("X-Instancez-Org"))
		if r.URL.Path == "/public/templates" {
			_, _ = w.Write([]byte(`{"templates":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"manifest":{"id":"blog"},"files":{"instancez.yaml":"x"}}`))
	})
	require.Equal(t, "org_1", c.Org)
	_, err := c.ListTemplates()
	require.NoError(t, err)
	_, err = c.GetTemplate("blog")
	require.NoError(t, err)
	assert.Equal(t, []string{"", ""}, gotOrg)
}

func TestGetTemplateBodySizeBoundary(t *testing.T) {
	const pre, post = `{"manifest":{},"files":{"instancez.yaml":"`, `"}}`
	body := func(total int) string {
		return pre + strings.Repeat("a", total-len(pre)-len(post)) + post
	}
	for name, tc := range map[string]struct {
		total   int
		wantErr bool
	}{
		"exact limit": {MaxTemplateBytes, false},
		"one over":    {MaxTemplateBytes + 1, true},
	} {
		t.Run(name, func(t *testing.T) {
			payload := body(tc.total)
			require.Len(t, payload, tc.total)
			c := tplServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(payload)) })
			_, err := c.GetTemplate("blog")
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "too large")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestListTemplatesNotFoundIsError(t *testing.T) {
	c := tplServer(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	got, err := c.ListTemplates()
	require.Error(t, err)
	assert.Nil(t, got)
}

func TestGetTemplateNotFoundNonJSONBody(t *testing.T) {
	c := tplServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`<html>not found</html>`))
	})
	_, err := c.GetTemplate("zzzz")
	var nf *TemplateNotFoundError
	require.True(t, errors.As(err, &nf))
	assert.NotNil(t, nf.Suggestions)
	assert.Empty(t, nf.Suggestions)
}
