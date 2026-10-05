package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/instancez/instancez/internal/domain"
)

func appleIDToken(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("k"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func appleServer(t *testing.T, idToken string, status int) appleProvider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		writeFakeBody(w, `{"access_token":"at","id_token":"`+idToken+`"}`)
	}))
	t.Cleanup(srv.Close)
	return appleProvider{tokenURL: srv.URL}
}

func validClaims() jwt.MapClaims {
	return jwt.MapClaims{"iss": "https://appleid.apple.com", "aud": "svc.id", "sub": "001.abc",
		"email": "a@privaterelay.appleid.com", "email_verified": "true", "exp": time.Now().Add(time.Hour).Unix()}
}

var appleCfg = &domain.OAuthProvider{ClientID: "svc.id, com.app.ios", ClientSecret: "s", RedirectURL: "https://x/cb"}

func appleLogin(t *testing.T, claims jwt.MapClaims, callback url.Values) (*OAuthUserInfo, error) {
	t.Helper()
	p := appleServer(t, appleIDToken(t, claims), 200)
	tok, err := p.ExchangeCode(appleCfg, "code")
	if err != nil {
		return nil, err
	}
	return p.FetchUser(tok, callback)
}

func TestAppleAuthorizeURL(t *testing.T) {
	p, ok := OAuthRegistry("apple")
	if !ok {
		t.Fatal("apple not registered")
	}
	u := p.AuthorizeURL(appleCfg, "st")
	for _, want := range []string{"response_mode=form_post", "scope=name%20email", "client_id=svc.id", "state=st"} {
		if !strings.Contains(u, want) {
			t.Errorf("%q missing %q", u, want)
		}
	}
	if strings.Contains(u, "+") || strings.Contains(u, "com.app.ios") {
		t.Errorf("unexpected + or second client id in %q", u)
	}
}

func TestAppleSuccess(t *testing.T) {
	cb := url.Values{"user": {`{"name":{"firstName":"Ada","lastName":"L"}}`}}
	u, err := appleLogin(t, validClaims(), cb)
	if err != nil {
		t.Fatal(err)
	}
	if u.ProviderID != "001.abc" || u.Email != "a@privaterelay.appleid.com" || !u.EmailVerified || u.Name != "Ada L" {
		t.Errorf("got %+v", u)
	}
}

func TestAppleRejects(t *testing.T) {
	cases := []struct {
		name string
		set  func(jwt.MapClaims)
	}{
		{"wrong iss", func(c jwt.MapClaims) { c["iss"] = "https://evil.example" }},
		{"wrong aud", func(c jwt.MapClaims) { c["aud"] = "other" }},
		{"second client id aud", func(c jwt.MapClaims) { c["aud"] = "com.app.ios" }},
		{"expired", func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Minute).Unix() }},
		{"missing exp", func(c jwt.MapClaims) { delete(c, "exp") }},
		{"missing sub", func(c jwt.MapClaims) { delete(c, "sub") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validClaims()
			tc.set(c)
			if _, err := appleLogin(t, c, nil); err == nil {
				t.Error("expected error")
			}
		})
	}

	t.Run("empty id_token", func(t *testing.T) {
		p := appleServer(t, "", 200)
		if _, err := p.ExchangeCode(appleCfg, "c"); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("token endpoint 400", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(400)
			writeFakeBody(w, `{"error":"invalid_client"}`)
		}))
		defer srv.Close()
		if _, err := (appleProvider{tokenURL: srv.URL}).ExchangeCode(appleCfg, "c"); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("garbage id_token", func(t *testing.T) {
		p := appleServer(t, "not-a-jwt", 200)
		if _, err := p.ExchangeCode(appleCfg, "c"); err == nil {
			t.Error("expected error")
		}
	})
}

func TestAppleName(t *testing.T) {
	cases := []struct{ name, user, want string }{
		{"no user", "", ""},
		{"malformed", `{"name":`, ""},
		{"oversized", `{"name":{"firstName":"` + strings.Repeat("a", 2048) + `"}}`, ""},
		{"first only", `{"name":{"firstName":"Ada"}}`, "Ada"},
		{"last only", `{"name":{"lastName":"L"}}`, "L"},
		{"empty names", `{"name":{}}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cb := url.Values{}
			if tc.user != "" {
				cb.Set("user", tc.user)
			}
			u, err := appleLogin(t, validClaims(), cb)
			if err != nil {
				t.Fatal(err)
			}
			if u.Name != tc.want {
				t.Errorf("got %q want %q", u.Name, tc.want)
			}
		})
	}
}

func TestClientIDs(t *testing.T) {
	cases := map[string][]string{"": nil, " a , ,b ": {"a", "b"}, "x": {"x"}, ",,": nil}
	for in, want := range cases {
		if got := ClientIDs(in); !slices.Equal(got, want) {
			t.Errorf("%q: got %v want %v", in, got, want)
		}
	}
}

func TestEmailVerifiedClaim(t *testing.T) {
	for in, want := range map[any]bool{true: true, "true": true, false: false, "false": false, "TRUE": false, "": false, 1: false, nil: false} {
		if got := EmailVerifiedClaim(in); got != want {
			t.Errorf("%v: got %v", in, got)
		}
	}
}

func TestExchangeOAuthCode(t *testing.T) {
	serve := func(body string) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeFakeBody(w, body) }))
		t.Cleanup(srv.Close)
		return srv.URL
	}
	tok, err := exchangeOAuthCode(serve(`{"access_token":"at","id_token":"idt"}`), appleCfg, "c")
	if err != nil || tok.AccessToken != "at" || tok.IDToken != "idt" {
		t.Errorf("got %+v, %v", tok, err)
	}
	if _, err := exchangeOAuthCode(serve(`{"id_token":"idt"}`), appleCfg, "c"); err == nil {
		t.Error("missing access_token must fail")
	}
}
