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
		"application/vnd.pgrst.plan+json, application/json",
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

func TestRejectPlan(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"} {
		for accept, want := range map[string]int{
			`application/vnd.pgrst.plan+text; for="application/json"; options=;`: 406,
			"Application/Vnd.Pgrst.Plan+JSON":                                    406,
			`application/vnd.pgrst.plan; for="text/xml"`:                         406,
			"application/vnd.pgrst.plan+json, application/json":                  406,
			"application/json": 200,
			"":                 200,
		} {
			ran := false
			r := gin.New()
			r.Handle(method, "/x", rejectPlan, func(c *gin.Context) { ran = true; c.Status(200) })
			w := httptest.NewRecorder()
			req := httptest.NewRequest(method, "/x", nil)
			req.Header.Set("Accept", accept)
			r.ServeHTTP(w, req)
			if w.Code != want || ran != (want == 200) {
				t.Fatalf("%s %q: status %d ran %v", method, accept, w.Code, ran)
			}
			if want == 406 && method != "HEAD" {
				var body map[string]any
				_ = json.Unmarshal(w.Body.Bytes(), &body)
				if body["code"] != "PGRST107" {
					t.Fatalf("%s %q: code %v", method, accept, body["code"])
				}
			}
		}
	}
}

func TestHandleList_PlanWalWithoutAnalyzeIs400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &CRUDHandler{cfg: &domain.Config{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/rest/v1/todos", nil)
	c.Request.Header.Set("Accept", `application/vnd.pgrst.plan+json; for="application/json"; options=wal|buffers;`)
	c.Set("is_admin", true)
	h.handleList("todos", testTable())(c) // db is nil: reaching it would panic
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != 400 || body["code"] != "22023" {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}
