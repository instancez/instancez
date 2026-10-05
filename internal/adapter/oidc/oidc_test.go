package oidc

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func newKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func sign(t *testing.T, key *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func goodClaims() jwt.MapClaims {
	return jwt.MapClaims{"iss": "https://accounts.google.com", "aud": "cid", "exp": time.Now().Add(time.Minute).Unix()}
}

// jwksServer serves the given kid->key set (swappable) and counts hits.
func jwksServer(t *testing.T, keys *atomic.Pointer[map[string]*rsa.PublicKey], hits *atomic.Int32) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		var out struct{ Keys []map[string]string }
		for kid, k := range *keys.Load() {
			out.Keys = append(out.Keys, map[string]string{
				"kid": kid, "kty": "RSA",
				"n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes()),
			})
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestVerify_RejectsForgedAlgAndWrongKey(t *testing.T) {
	key := newKey(t)
	SeedKeys("google", map[string]*rsa.PublicKey{"k1": &key.PublicKey})
	t.Cleanup(func() { SeedKeys("google", nil) })

	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, goodClaims())
	hs.Header["kid"] = "k1"
	hsTok, err := hs.SignedString([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	none := jwt.NewWithClaims(jwt.SigningMethodNone, goodClaims())
	none.Header["kid"] = "k1"
	noneTok, err := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	other := sign(t, newKey(t), "k1", goodClaims())

	for name, tok := range map[string]string{"hs256": hsTok, "none": noneTok, "wrong key": other, "garbage": "x.y.z", "empty": ""} {
		if _, err := Verify("google", tok, []string{"cid"}, ""); err == nil {
			t.Errorf("%s token accepted", name)
		}
	}
	if _, err := Verify("google", sign(t, key, "k1", goodClaims()), []string{"cid"}, ""); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
}

func TestVerify_UnsupportedProvider(t *testing.T) {
	if Supported("github") {
		t.Error("github must not be supported")
	}
	if _, err := Verify("github", "x", nil, ""); err == nil {
		t.Error("expected error")
	}
}

func TestVerify_KeyRotationRefreshesOnce(t *testing.T) {
	oldKey, newK := newKey(t), newKey(t)
	var keys atomic.Pointer[map[string]*rsa.PublicKey]
	set := map[string]*rsa.PublicKey{"old": &oldKey.PublicKey}
	keys.Store(&set)
	var hits atomic.Int32
	t.Cleanup(SetJWKSURL("google", jwksServer(t, &keys, &hits)))
	t.Cleanup(func() { SeedKeys("google", nil) })

	c := cacheFor("google")
	SeedKeys("google", set)
	c.mu.Lock()
	c.fetchedAt = time.Now().Add(-2 * refreshMinAge)
	c.mu.Unlock()

	rotated := map[string]*rsa.PublicKey{"old": &oldKey.PublicKey, "new": &newK.PublicKey}
	keys.Store(&rotated)

	tok := sign(t, newK, "new", goodClaims())
	if _, err := Verify("google", tok, []string{"cid"}, ""); err != nil {
		t.Fatalf("rotated key must verify after refresh: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("want 1 fetch, got %d", hits.Load())
	}
}

func TestVerify_UnknownKidNoRefetchStorm(t *testing.T) {
	key := newKey(t)
	var keys atomic.Pointer[map[string]*rsa.PublicKey]
	set := map[string]*rsa.PublicKey{"k1": &key.PublicKey}
	keys.Store(&set)
	var hits atomic.Int32
	t.Cleanup(SetJWKSURL("google", jwksServer(t, &keys, &hits)))
	t.Cleanup(func() { SeedKeys("google", nil) })

	c := cacheFor("google")
	SeedKeys("google", set)
	c.mu.Lock()
	c.fetchedAt = time.Now().Add(-2 * refreshMinAge)
	c.mu.Unlock()

	bad := sign(t, key, "ghost", goodClaims())
	for i := 0; i < 5; i++ {
		if _, err := Verify("google", bad, []string{"cid"}, ""); err == nil {
			t.Fatal("unknown kid accepted")
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("want 1 fetch for repeated unknown kid, got %d", hits.Load())
	}
}

func TestVerify_FetchFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	t.Cleanup(SetJWKSURL("google", srv.URL))
	SeedKeys("google", nil)

	if _, err := Verify("google", sign(t, newKey(t), "k1", goodClaims()), []string{"cid"}, ""); err == nil {
		t.Fatal("expected error on JWKS fetch failure")
	}
}

func TestVerify_ChecksIssuerAudienceNonce(t *testing.T) {
	key := newKey(t)
	SeedKeys("apple", map[string]*rsa.PublicKey{"k1": &key.PublicKey})
	t.Cleanup(func() { SeedKeys("apple", nil) })
	base := func() jwt.MapClaims {
		return jwt.MapClaims{"iss": "https://appleid.apple.com", "aud": "svc", "exp": time.Now().Add(time.Minute).Unix()}
	}
	cases := map[string]func(jwt.MapClaims){
		"bad iss":          func(c jwt.MapClaims) { c["iss"] = "https://evil" },
		"bad aud":          func(c jwt.MapClaims) { c["aud"] = "other" },
		"unexpected nonce": func(c jwt.MapClaims) { c["nonce"] = "n" },
	}
	for name, mut := range cases {
		c := base()
		mut(c)
		if _, err := Verify("apple", sign(t, key, "k1", c), []string{"svc"}, ""); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
