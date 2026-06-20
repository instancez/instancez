package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

func TestOAuthRegistryBuiltins(t *testing.T) {
	for _, name := range []string{"google", "github"} {
		if _, ok := OAuthRegistry(name); !ok {
			t.Errorf("provider %q not registered", name)
		}
	}
	if _, ok := OAuthRegistry("nope"); ok {
		t.Error("unexpected provider \"nope\"")
	}
}

func TestGoogleAuthorizeURL(t *testing.T) {
	p, ok := OAuthRegistry("google")
	if !ok {
		t.Fatal("google not registered")
	}
	cfg := &domain.OAuthProvider{ClientID: "cid", RedirectURL: "https://app/cb"}
	got := p.AuthorizeURL(cfg, "st8")
	for _, want := range []string{"accounts.google.com", "client_id=cid", "state=st8", "scope=openid+email+profile"} {
		if !strings.Contains(got, want) {
			t.Errorf("authorize url missing %q: %s", want, got)
		}
	}
}

func TestGithubFetchUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":42,"email":"a@b.co","name":"A","login":"a"}`))
	}))
	defer srv.Close()
	gh := &githubProvider{userAPI: srv.URL}
	u, err := gh.FetchUser("tok")
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "a@b.co" || u.ProviderID != "42" {
		t.Errorf("user = %#v", u)
	}
}
