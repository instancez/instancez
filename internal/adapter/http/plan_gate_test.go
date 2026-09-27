package http

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/domain"
)

func TestHandleList_PlanRefusedWithoutSecretKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &CRUDHandler{cfg: &domain.Config{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	accepts := []string{
		"application/vnd.pgrst.plan+json",
		"application/vnd.pgrst.plan+text",
		`application/vnd.pgrst.plan+text; for="application/json"; options=analyze`, // supabase-js .explain()
		"application/vnd.pgrst.plan",
		"Application/Vnd.Pgrst.Plan+Json",
		`application/vnd.pgrst.plan+text; for="application/json"; options=;`,
	}
	for _, role := range []string{"anon", "authenticated"} {
		for _, accept := range accepts {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/rest/v1/todos", nil)
			c.Request.Header.Set("Accept", accept)
			c.Set(contextKeySession, domain.Session{Role: role, IsAuthenticated: role != "anon"})
			h.handleList("todos", testTable())(c) // db is nil: reaching it would panic
			if w.Code != 406 {
				t.Fatalf("%s %q: status %d, want 406", role, accept, w.Code)
			}
			var body map[string]any
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			if body["code"] != "PGRST107" {
				t.Fatalf("%s %q: code %v, want PGRST107", role, accept, body["code"])
			}
		}
	}
}
