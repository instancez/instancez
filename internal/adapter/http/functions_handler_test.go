package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/domain"
)

// fakeRuntime is a test double for domain.FunctionRuntime.
// If invokeFunc is set it is called by Invoke; otherwise the response is
// looked up from known.
type fakeRuntime struct {
	known        map[string]*domain.FunctionResponse
	authRequired map[string]bool // if true for a name, AuthRequired returns true
	invokeFunc   func(context.Context, domain.FunctionRequest) (*domain.FunctionResponse, error)
	invokeCalled bool // set to true whenever Invoke is called
}

func (f *fakeRuntime) Has(name string) bool {
	_, ok := f.known[name]
	return ok
}

func (f *fakeRuntime) AuthRequired(name string) bool {
	return f.authRequired[name]
}

func (f *fakeRuntime) Invoke(ctx context.Context, req domain.FunctionRequest) (*domain.FunctionResponse, error) {
	f.invokeCalled = true
	if f.invokeFunc != nil {
		return f.invokeFunc(ctx, req)
	}
	return f.known[req.Name], nil
}

func (f *fakeRuntime) Close() error { return nil }

// TestFunctionsRoute501WhenNoRuntime asserts that a nil runtime returns 501.
func TestFunctionsRoute501WhenNoRuntime(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewFunctionsHandler(nil)
	r := gin.New()
	h.Mount(r.Group("/functions/v1"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/functions/v1/whatever", nil))
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("want 501, got %d", w.Code)
	}
}

// TestFunctionsRoute404WhenUnknown asserts that an unknown function returns 404.
func TestFunctionsRoute404WhenUnknown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rt := &fakeRuntime{known: map[string]*domain.FunctionResponse{
		"known": {Status: 200, Body: []byte(`{"ok":true}`)},
	}}
	h := NewFunctionsHandler(rt)
	r := gin.New()
	h.Mount(r.Group("/functions/v1"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/functions/v1/nope", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

// TestFunctionsRouteRelaysResponse asserts that a known function's response is
// forwarded (status + body) to the HTTP response.
func TestFunctionsRouteRelaysResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rt := &fakeRuntime{known: map[string]*domain.FunctionResponse{
		"known": {Status: 200, Body: []byte(`{"ok":true}`)},
	}}
	h := NewFunctionsHandler(rt)
	r := gin.New()
	h.Mount(r.Group("/functions/v1"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/functions/v1/known", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if got := w.Body.String(); got != `{"ok":true}` {
		t.Fatalf("want body %q, got %q", `{"ok":true}`, got)
	}
}

// TestFunctionsRouteRelaysHeaders asserts that response headers from the
// function are forwarded to the HTTP response.
func TestFunctionsRouteRelaysHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rt := &fakeRuntime{known: map[string]*domain.FunctionResponse{
		"known": {
			Status:  200,
			Headers: http.Header{"X-Custom": {"val"}},
			Body:    []byte(`{}`),
		},
	}}
	h := NewFunctionsHandler(rt)
	r := gin.New()
	h.Mount(r.Group("/functions/v1"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/functions/v1/known", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if got := w.Header().Get("X-Custom"); got != "val" {
		t.Fatalf("want X-Custom header %q, got %q", "val", got)
	}
}

// TestFunctionsRoute502OnInvokeError asserts that when Invoke returns a
// non-nil error the handler responds with 502.
func TestFunctionsRoute502OnInvokeError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rt := &fakeRuntime{
		known: map[string]*domain.FunctionResponse{
			"boom": nil,
		},
		invokeFunc: func(_ context.Context, _ domain.FunctionRequest) (*domain.FunctionResponse, error) {
			return nil, errors.New("runtime exploded")
		},
	}
	h := NewFunctionsHandler(rt)
	r := gin.New()
	h.Mount(r.Group("/functions/v1"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/functions/v1/boom", nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d", w.Code)
	}
}

// TestFunctionsAuthRequiredAnonymous asserts that an auth_required function
// returns 401 for an anonymous caller and does NOT invoke the function.
func TestFunctionsAuthRequiredAnonymous(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rt := &fakeRuntime{
		known: map[string]*domain.FunctionResponse{
			"secret": {Status: 200, Body: []byte(`{}`)},
		},
		authRequired: map[string]bool{"secret": true},
	}
	h := NewFunctionsHandler(rt)
	r := gin.New()
	h.Mount(r.Group("/functions/v1"))

	// No session set on context → anonymous caller.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/functions/v1/secret", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
	if rt.invokeCalled {
		t.Fatalf("Invoke must NOT be called for anonymous caller of auth_required function")
	}
}

// TestFunctionsAuthRequiredAuthenticated asserts that an auth_required function
// is invoked normally when the caller has an authenticated session.
func TestFunctionsAuthRequiredAuthenticated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rt := &fakeRuntime{
		known: map[string]*domain.FunctionResponse{
			"secret": {Status: 200, Body: []byte(`{"ok":true}`)},
		},
		authRequired: map[string]bool{"secret": true},
	}
	h := NewFunctionsHandler(rt)
	r := gin.New()
	// Inject an authenticated session via middleware before the handler.
	r.Use(func(c *gin.Context) {
		c.Set(contextKeySession, domain.Session{
			IsAuthenticated: true,
			Role:            "authenticated",
			UserID:          "user-1",
		})
		c.Next()
	})
	h.Mount(r.Group("/functions/v1"))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/functions/v1/secret", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if !rt.invokeCalled {
		t.Fatalf("Invoke must be called for authenticated caller of auth_required function")
	}
}

// TestFunctionsNoAuthRequiredAnonymousAllowed asserts that a function without
// auth_required is invoked even for an anonymous caller (existing behaviour).
func TestFunctionsNoAuthRequiredAnonymousAllowed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rt := &fakeRuntime{
		known: map[string]*domain.FunctionResponse{
			"public": {Status: 200, Body: []byte(`{"ok":true}`)},
		},
		// authRequired not set → defaults to false for "public"
	}
	h := NewFunctionsHandler(rt)
	r := gin.New()
	h.Mount(r.Group("/functions/v1"))

	// No session set → anonymous caller.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/functions/v1/public", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if !rt.invokeCalled {
		t.Fatalf("Invoke must be called for anonymous caller of non-auth_required function")
	}
}
