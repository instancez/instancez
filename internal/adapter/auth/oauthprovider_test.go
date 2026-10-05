package auth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

// writeFakeBody sends a fake OAuth provider response body through a helper
// instead of a direct ResponseWriter.Write call.
func writeFakeBody(w http.ResponseWriter, body string) {
	_, _ = io.WriteString(w, body)
}

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

func githubServer(t *testing.T, user, emails string, emailsStatus int) *githubProvider {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) { writeFakeBody(w, user) })
	mux.HandleFunc("/user/emails", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(emailsStatus)
		writeFakeBody(w, emails)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &githubProvider{userAPI: srv.URL + "/user", emailAPI: srv.URL + "/user/emails"}
}

func TestGithubFetchUser_UsesVerifiedEmailOverProfileEmail(t *testing.T) {
	gh := githubServer(t, `{"id":42,"email":"public@evil.co","name":"A"}`,
		`[{"email":"public@evil.co","primary":false,"verified":false},{"email":"real@b.co","primary":true,"verified":true}]`, 200)
	u, err := gh.FetchUser(&OAuthToken{AccessToken: "tok"}, nil)
	if err != nil || u.Email != "real@b.co" || !u.EmailVerified || u.ProviderID != "42" {
		t.Fatalf("user=%+v err=%v", u, err)
	}
}

func TestGithubFetchUser_UnverifiedPrimaryFallsBackToVerifiedSecondary(t *testing.T) {
	gh := githubServer(t, `{"id":7,"email":"primary@b.co"}`,
		`[{"email":"primary@b.co","primary":true,"verified":false},{"email":"second@b.co","primary":false,"verified":true}]`, 200)
	u, err := gh.FetchUser(&OAuthToken{AccessToken: "tok"}, nil)
	if err != nil || u.Email != "second@b.co" || !u.EmailVerified {
		t.Fatalf("user=%+v err=%v", u, err)
	}
}

func TestGithubFetchUser_UnverifiedFallback(t *testing.T) {
	for name, gh := range map[string]*githubProvider{
		"no verified email": githubServer(t, `{"id":1,"email":"a@b.co"}`, `[{"email":"a@b.co","primary":true,"verified":false}]`, 200),
		"empty email list":  githubServer(t, `{"id":1,"email":"a@b.co"}`, `[]`, 200),
		"emails api down":   githubServer(t, `{"id":1,"email":"a@b.co"}`, `boom`, 500),
		"emails api 403 with verified body": githubServer(t, `{"id":1,"email":"a@b.co"}`,
			`[{"email":"a@b.co","primary":true,"verified":true}]`, 403),
		"no emails api": {userAPI: githubServer(t, `{"id":1,"email":"a@b.co"}`, ``, 200).userAPI},
	} {
		u, err := gh.FetchUser(&OAuthToken{AccessToken: "tok"}, nil)
		if err != nil || u.EmailVerified || u.Email != "a@b.co" {
			t.Errorf("%s: user=%+v err=%v, want unverified a@b.co", name, u, err)
		}
	}
}

func TestGithubFetchUser_RejectsMissingID(t *testing.T) {
	gh := githubServer(t, `{"email":"a@b.co"}`, `[{"email":"a@b.co","primary":true,"verified":true}]`, 200)
	if u, err := gh.FetchUser(&OAuthToken{AccessToken: "tok"}, nil); err == nil {
		t.Fatalf("want error for missing id, got %+v", u)
	}
}

func TestGoogleFetchUser_ReadsVerifiedEmail(t *testing.T) {
	for body, want := range map[string]bool{
		`{"id":"g1","email":"a@b.co","verified_email":true}`:  true,
		`{"id":"g1","email":"a@b.co","verified_email":false}`: false,
		`{"id":"g1","email":"a@b.co"}`:                        false,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeFakeBody(w, body) }))
		u, err := googleProvider{userAPI: srv.URL}.FetchUser(&OAuthToken{AccessToken: "tok"}, nil)
		srv.Close()
		if err != nil || u.EmailVerified != want || u.ProviderID != "g1" {
			t.Errorf("%s: user=%+v err=%v", body, u, err)
		}
	}
}
