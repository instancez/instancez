package http

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/instancez/instancez/internal/app"
)

// These tests pin the security-critical key-selection edges of verifySignedJWT
// (via jwtAuth): a bearer is verified against the key its own kid names, a
// retired key still verifies, and forged or mis-keyed tokens are rejected. The
// happy path lives in auth_handler_test.go; this file covers the rejections and
// the rotation/legacy contracts that had no direct coverage.

func authClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"sub":   "11111111-2222-3333-4444-555555555555",
		"role":  "authenticated",
		"aud":   "authenticated",
		"email": "u@e.com",
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(time.Hour).Unix(),
	}
}

func rs256Token(t *testing.T, priv *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	if kid != "" {
		tok.Header["kid"] = kid
	}
	s, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign rs256: %v", err)
	}
	return s
}

func hs256Token(t *testing.T, secret []byte, kid string, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(secret)
	if err != nil {
		t.Fatalf("sign hs256: %v", err)
	}
	return s
}

func pkcs1PEM(t *testing.T, priv *rsa.PrivateKey) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	})
}

// probe runs a bearer through jwtAuth on a required route and returns the
// recorder, mirroring the setup the other jwtAuth tests use.
func probe(km *app.JWTKeyManager, bearer string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/probe", jwtAuth(km, true), func(c *gin.Context) {
		c.JSON(200, gin.H{"role": getSession(c).Role})
	})
	req := httptest.NewRequest("GET", "/probe", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// A token signed by a different key than the one its kid resolves to must be
// rejected: this proves the signature is actually checked, not merely parsed.
func TestJWTAuth_RejectsWrongSignature(t *testing.T) {
	verifier := stubKeys(t) // kid "test-kid", key A

	attacker, err := rsa.GenerateKey(rand.Reader, 2048) // key B
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	// Same kid as the verifier's key, signed with the wrong private key.
	tok := rs256Token(t, attacker, "test-kid", authClaims())

	if code := probe(verifier, tok).Code; code != 401 {
		t.Fatalf("status = %d, want 401 for wrong-signature token", code)
	}
}

// The RS256 -> HS256 confusion attack: forge an HS256 token whose kid names an
// RS256 key, using the RSA public key as the HMAC secret. It must be rejected
// because an RS256 key row carries no HMAC secret.
func TestJWTAuth_RejectsAlgConfusion(t *testing.T) {
	km := stubKeys(t)
	active, err := km.Active(context.Background())
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(active.PublicKey)
	if err != nil {
		t.Fatalf("marshal pub: %v", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})

	tok := hs256Token(t, pubPEM, "test-kid", authClaims())

	if code := probe(km, tok).Code; code != 401 {
		t.Fatalf("status = %d, want 401 for alg-confusion token", code)
	}
}

// A token with no kid header cannot select a key and must be rejected.
func TestJWTAuth_RejectsMissingKid(t *testing.T) {
	km := stubKeys(t)
	active, err := km.Active(context.Background())
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	tok := rs256Token(t, active.PrivateKey, "", authClaims()) // no kid header

	if code := probe(km, tok).Code; code != 401 {
		t.Fatalf("status = %d, want 401 for missing-kid token", code)
	}
}

// A token whose kid matches no row in the store must be rejected.
func TestJWTAuth_RejectsUnknownKid(t *testing.T) {
	db := &stubDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			return nil, nil // no such key
		},
	}
	km := app.NewJWTKeyManager(db)

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	tok := rs256Token(t, priv, "no-such-kid", authClaims())

	if code := probe(km, tok).Code; code != 401 {
		t.Fatalf("status = %d, want 401 for unknown-kid token", code)
	}
}

// After rotation the old key is retired but must still verify tokens it signed
// before the rotation. The by-kid lookup therefore must NOT filter on
// retired_at (unlike Active/AllPublicKeys), and such a token must yield 200.
func TestJWTAuth_AcceptsRetiredKey(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	privPEM := pkcs1PEM(t, priv)

	var gotQuery string
	db := &stubDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			gotQuery = q
			// The row exists even though this key has been retired.
			return map[string]any{
				"kid":        "retired-kid",
				"algorithm":  "RS256",
				"secret":     privPEM,
				"created_at": time.Now(),
			}, nil
		},
	}
	km := app.NewJWTKeyManager(db)
	tok := rs256Token(t, priv, "retired-kid", authClaims())

	w := probe(km, tok)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 for retired-key token (body: %s)", w.Code, w.Body.String())
	}
	if strings.Contains(gotQuery, "retired_at") {
		t.Fatalf("by-kid lookup filters on retired_at, so retired keys would stop verifying: %q", gotQuery)
	}
}

// Legacy HS256 keys (secret, no public key) must still verify HS256 tokens by
// kid, so tokens predating the RS256 switch keep working.
func TestJWTAuth_AcceptsHS256LegacyKey(t *testing.T) {
	secret := []byte("legacy-hs256-shared-secret")
	db := &stubDB{
		queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
			return map[string]any{
				"kid":        "legacy-kid",
				"algorithm":  "HS256",
				"secret":     secret,
				"created_at": time.Now(),
			}, nil
		},
	}
	km := app.NewJWTKeyManager(db)
	tok := hs256Token(t, secret, "legacy-kid", authClaims())

	w := probe(km, tok)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 for HS256 legacy token (body: %s)", w.Code, w.Body.String())
	}
	if role := roleFromBody(t, w); role != "authenticated" {
		t.Fatalf("role = %q, want authenticated", role)
	}
}

func roleFromBody(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	role, _ := body["role"].(string)
	return role
}
