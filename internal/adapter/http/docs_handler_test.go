package http

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/saedx1/ultrabase/internal/domain"
)

// handleDocs must reference openapi.json with a path *relative* to the docs
// page (no leading slash) so the browser resolves it against the public URL.
// A root-absolute path (e.g. /openapi.json) 404s behind a prefix-stripping
// proxy that publishes docs at /api/xyz/docs but forwards /docs to the backend.
func TestHandleDocs_RelativeOpenAPIURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{cfg: &domain.Config{Project: domain.Project{Name: "Test"}}}

	// Even when the backend sees a non-root path, the emitted reference stays
	// relative and never gains a leading slash.
	for _, path := range []string{"/docs", "/api/docs", "/api/xyz/docs"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", path, nil)

		s.handleDocs(c)

		body := w.Body.String()
		if !strings.Contains(body, `data-url="openapi.json"`) {
			t.Errorf("path %s: expected relative data-url=\"openapi.json\", got: %s", path, body)
		}
		if strings.Contains(body, `data-url="/`) {
			t.Errorf("path %s: docs must not use a root-absolute openapi.json path (breaks behind a prefix-stripping proxy)", path)
		}
	}
}
