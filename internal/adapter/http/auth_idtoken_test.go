package http

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/instancez/instancez/internal/domain"
)

type idTokenResult struct {
	code int
	body string
	got  domain.OAuthLogin
}

// runIDToken signs claims with a fresh key seeded as the provider's JWKS and posts the grant.
func runIDToken(t *testing.T, provider, clientID string, configured bool, claims jwt.MapClaims, nonce string) idTokenResult {
	t.Helper()
	gin.SetMode(gin.TestMode)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	providerJWKS.mu.Lock()
	providerJWKS.cache[provider] = &jwksCache{keys: map[string]*rsa.PublicKey{"k1": &key.PublicKey}, fetchedAt: time.Now()}
	providerJWKS.mu.Unlock()
	t.Cleanup(func() {
		providerJWKS.mu.Lock()
		delete(providerJWKS.cache, provider)
		providerJWKS.mu.Unlock()
	})
	claims["exp"] = time.Now().Add(time.Minute).Unix()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "k1"
	signed, _ := tok.SignedString(key)

	oauth := map[string]*domain.OAuthProvider{}
	if configured {
		oauth[provider] = &domain.OAuthProvider{ClientID: clientID}
	}
	var res idTokenResult
	h := &AuthHandler{
		cfg:    &domain.Config{Auth: &domain.Auth{OAuth: oauth}},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)), jwtKeys: stubKeys(t),
		authSvc: &stubAuthService{upsertOAuthUserFn: func(ctx context.Context, in domain.OAuthLogin) (map[string]any, error) {
			res.got = in
			return nil, domain.ErrProviderEmailUnverified
		}},
	}
	r := gin.New()
	r.POST("/token", h.handleIDTokenGrant)
	body := `{"provider":"` + provider + `","token":"` + signed + `"`
	if nonce != "" {
		body += `,"nonce":"` + nonce + `"`
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/token", strings.NewReader(body+"}")))
	res.code, res.body = w.Code, w.Body.String()
	return res
}

func appleClaims(aud any) jwt.MapClaims {
	return jwt.MapClaims{"iss": "https://appleid.apple.com", "aud": aud, "sub": "a-1", "email": "a@e.com", "email_verified": "true"}
}

func hashedNonce(raw string) string {
	s := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(s[:])
}

func TestIDTokenGrant_Apple(t *testing.T) {
	const ids = "svc.id, com.app.ios"
	reached := func(t *testing.T, r idTokenResult) {
		t.Helper()
		if r.got.Provider != "apple" || r.got.ProviderUserID != "a-1" || !r.got.EmailVerified {
			t.Fatalf("login not passed to service: %+v (status %d %s)", r.got, r.code, r.body)
		}
	}
	rejected := func(t *testing.T, r idTokenResult, code int) {
		t.Helper()
		if r.code != code || r.got.Provider != "" {
			t.Fatalf("want %d and no upsert, got %d %s %+v", code, r.code, r.body, r.got)
		}
	}

	t.Run("second of two client ids", func(t *testing.T) {
		reached(t, runIDToken(t, "apple", ids, true, appleClaims("com.app.ios"), ""))
	})
	t.Run("unlisted aud", func(t *testing.T) {
		rejected(t, runIDToken(t, "apple", ids, true, appleClaims("evil.app"), ""), 401)
	})
	t.Run("aud as array", func(t *testing.T) {
		reached(t, runIDToken(t, "apple", ids, true, appleClaims([]string{"x", "com.app.ios"}), ""))
	})
	t.Run("aud array without match", func(t *testing.T) {
		rejected(t, runIDToken(t, "apple", ids, true, appleClaims([]string{"x", "y"}), ""), 401)
	})
	t.Run("empty client_id rejects empty aud", func(t *testing.T) {
		for _, cid := range []string{"", " ", " , "} {
			rejected(t, runIDToken(t, "apple", cid, true, appleClaims(""), ""), 401)
		}
	})
	t.Run("empty client_id rejects missing aud", func(t *testing.T) {
		c := appleClaims("x")
		delete(c, "aud")
		rejected(t, runIDToken(t, "apple", "", true, c, ""), 401)
	})
	t.Run("raw nonce", func(t *testing.T) {
		c := appleClaims("svc.id")
		c["nonce"] = "n-123"
		reached(t, runIDToken(t, "apple", ids, true, c, "n-123"))
	})
	t.Run("hashed nonce", func(t *testing.T) {
		c := appleClaims("svc.id")
		c["nonce"] = hashedNonce("n-123")
		reached(t, runIDToken(t, "apple", ids, true, c, "n-123"))
	})
	t.Run("wrong nonce", func(t *testing.T) {
		c := appleClaims("svc.id")
		c["nonce"] = hashedNonce("other")
		rejected(t, runIDToken(t, "apple", ids, true, c, "n-123"), 401)
	})
	t.Run("missing nonce claim", func(t *testing.T) {
		rejected(t, runIDToken(t, "apple", ids, true, appleClaims("svc.id"), "n-123"), 401)
	})
	t.Run("not configured", func(t *testing.T) {
		r := runIDToken(t, "apple", "", false, appleClaims("svc.id"), "")
		rejected(t, r, 400)
		if !strings.Contains(r.body, "Apple provider not configured") {
			t.Fatalf("body %s", r.body)
		}
	})
}

func TestIDTokenGrant_ProviderGate(t *testing.T) {
	r := runIDToken(t, "github", "cid", true, jwt.MapClaims{"iss": "x", "aud": "cid"}, "")
	if r.code != 400 || !strings.Contains(r.body, "Unsupported provider for ID token: github") {
		t.Fatalf("github: %d %s", r.code, r.body)
	}
	r = runIDToken(t, "google", "", false, jwt.MapClaims{"iss": "https://accounts.google.com", "aud": "cid"}, "")
	if r.code != 400 || !strings.Contains(r.body, "Google provider not configured") {
		t.Fatalf("google unconfigured: %d %s", r.code, r.body)
	}
}

func TestIDTokenGrant_GoogleUnchanged(t *testing.T) {
	c := jwt.MapClaims{"iss": "https://accounts.google.com", "aud": "cid", "sub": "g-1", "email": "g@e.com", "email_verified": true}
	r := runIDToken(t, "google", "cid", true, c, "")
	if r.got.Provider != "google" || !r.got.EmailVerified {
		t.Fatalf("%+v %d %s", r.got, r.code, r.body)
	}
	c = jwt.MapClaims{"iss": "https://accounts.google.com", "aud": "other", "sub": "g-1", "email": "g@e.com"}
	if r = runIDToken(t, "google", "cid", true, c, ""); r.code != 401 {
		t.Fatalf("aud mismatch: %d %s", r.code, r.body)
	}
	c = jwt.MapClaims{"iss": "https://accounts.google.com", "aud": "cid", "sub": "g-1", "email": "g@e.com", "nonce": "raw"}
	if r = runIDToken(t, "google", "cid", true, c, "raw"); r.got.Provider != "google" {
		t.Fatalf("raw nonce: %d %s", r.code, r.body)
	}
}
