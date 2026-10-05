package oidc

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
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

type fakeJWKS struct {
	hits   atomic.Int32
	status atomic.Int32
	delay  atomic.Int64
	keys   atomic.Pointer[map[string]*rsa.PublicKey]
}

// newFakeJWKS serves keys at a test URL registered for google; it counts hits.
func newFakeJWKS(t *testing.T, keys map[string]*rsa.PublicKey) *fakeJWKS {
	t.Helper()
	f := &fakeJWKS{}
	f.status.Store(200)
	f.keys.Store(&keys)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		time.Sleep(time.Duration(f.delay.Load()))
		if st := int(f.status.Load()); st != 200 {
			w.WriteHeader(st)
			return
		}
		var out struct{ Keys []map[string]string }
		for kid, k := range *f.keys.Load() {
			out.Keys = append(out.Keys, map[string]string{
				"kid": kid, "kty": "RSA",
				"n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes()),
			})
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(SetJWKSURL("google", srv.URL))
	t.Cleanup(func() { SeedKeys("google", nil) })
	return f
}

// fakeClock replaces the package clock; advance moves it forward.
func fakeClock(t *testing.T) func(time.Duration) {
	t.Helper()
	cur := time.Now()
	var mu sync.Mutex
	old := now
	now = func() time.Time { mu.Lock(); defer mu.Unlock(); return cur }
	t.Cleanup(func() { now = old })
	return func(d time.Duration) { mu.Lock(); cur = cur.Add(d); mu.Unlock() }
}

func verifyKid(t *testing.T, key *rsa.PrivateKey, kid string) error {
	t.Helper()
	_, err := Verify("google", sign(t, key, kid, goodClaims()), []string{"cid"}, "")
	return err
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
	f := newFakeJWKS(t, map[string]*rsa.PublicKey{"old": &oldKey.PublicKey, "new": &newK.PublicKey})
	advance := fakeClock(t)
	SeedKeys("google", map[string]*rsa.PublicKey{"old": &oldKey.PublicKey})
	advance(2 * refreshMinAge)

	if err := verifyKid(t, newK, "new"); err != nil {
		t.Fatalf("rotated key must verify after refresh: %v", err)
	}
	if f.hits.Load() != 1 {
		t.Fatalf("want 1 fetch, got %d", f.hits.Load())
	}
}

func TestVerify_UnknownKidNoRefetchStorm(t *testing.T) {
	key := newKey(t)
	f := newFakeJWKS(t, map[string]*rsa.PublicKey{"k1": &key.PublicKey})
	advance := fakeClock(t)
	SeedKeys("google", map[string]*rsa.PublicKey{"k1": &key.PublicKey})
	advance(2 * refreshMinAge)

	for i := 0; i < 5; i++ {
		if err := verifyKid(t, key, "ghost"); err == nil {
			t.Fatal("unknown kid accepted")
		}
	}
	if f.hits.Load() != 1 {
		t.Fatalf("want 1 fetch for repeated unknown kid, got %d", f.hits.Load())
	}
	advance(refreshMinAge)
	_ = verifyKid(t, key, "ghost")
	if f.hits.Load() != 2 {
		t.Fatalf("want a second fetch after the window, got %d", f.hits.Load())
	}
}

func TestVerify_FailingEndpointConcurrentRandomKids(t *testing.T) {
	key := newKey(t)
	f := newFakeJWKS(t, map[string]*rsa.PublicKey{"k1": &key.PublicKey})
	f.status.Store(503)
	f.delay.Store(int64(50 * time.Millisecond))
	advance := fakeClock(t)
	SeedKeys("google", map[string]*rsa.PublicKey{"k1": &key.PublicKey})
	advance(2 * refreshMinAge)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := verifyKid(t, key, fmt.Sprintf("rand-%d", i)); err == nil {
				t.Error("random kid accepted")
			}
		}(i)
	}
	wg.Wait()
	if f.hits.Load() != 1 {
		t.Fatalf("want exactly 1 fetch, got %d", f.hits.Load())
	}
}

func TestVerify_ColdStartFailureFailsClosedThenRetries(t *testing.T) {
	key := newKey(t)
	f := newFakeJWKS(t, map[string]*rsa.PublicKey{"k1": &key.PublicKey})
	f.status.Store(503)
	advance := fakeClock(t)

	for i := 0; i < 3; i++ {
		if err := verifyKid(t, key, "k1"); err == nil {
			t.Fatal("accepted with no keys")
		}
	}
	if f.hits.Load() != 1 {
		t.Fatalf("want 1 fetch in window, got %d", f.hits.Load())
	}
	f.status.Store(200)
	advance(refreshMinAge)
	if err := verifyKid(t, key, "k1"); err != nil {
		t.Fatalf("must recover after the window: %v", err)
	}
}

func TestVerify_KnownKidNeverWaitsOnFetch(t *testing.T) {
	key := newKey(t)
	f := newFakeJWKS(t, map[string]*rsa.PublicKey{"k1": &key.PublicKey})
	f.delay.Store(int64(500 * time.Millisecond))
	advance := fakeClock(t)
	SeedKeys("google", map[string]*rsa.PublicKey{"k1": &key.PublicKey})
	advance(refreshMinAge + time.Second)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = verifyKid(t, key, "ghost")
	}()
	defer func() { <-done }()
	for f.hits.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	if err := verifyKid(t, key, "k1"); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("known kid waited %v behind a slow fetch", d)
	}
}

func TestVerify_FailedRefreshKeepsOldKeys(t *testing.T) {
	key := newKey(t)
	f := newFakeJWKS(t, map[string]*rsa.PublicKey{"k1": &key.PublicKey})
	f.status.Store(503)
	advance := fakeClock(t)
	SeedKeys("google", map[string]*rsa.PublicKey{"k1": &key.PublicKey})
	advance(2 * refreshMinAge)

	_ = verifyKid(t, key, "ghost")
	if err := verifyKid(t, key, "k1"); err != nil {
		t.Fatalf("old keys must survive a failed refresh: %v", err)
	}
}

func TestVerify_EmptyKeySetDoesNotWipeCache(t *testing.T) {
	key := newKey(t)
	f := newFakeJWKS(t, map[string]*rsa.PublicKey{})
	advance := fakeClock(t)
	SeedKeys("google", map[string]*rsa.PublicKey{"k1": &key.PublicKey})
	advance(2 * refreshMinAge)

	_ = verifyKid(t, key, "ghost")
	if f.hits.Load() != 1 {
		t.Fatalf("expected a fetch, got %d", f.hits.Load())
	}
	if err := verifyKid(t, key, "k1"); err != nil {
		t.Fatalf("empty JWKS wiped the cache: %v", err)
	}
}

func TestVerify_ExpiredCacheEndpointDown(t *testing.T) {
	key := newKey(t)
	f := newFakeJWKS(t, map[string]*rsa.PublicKey{"k1": &key.PublicKey})
	f.status.Store(503)
	advance := fakeClock(t)
	SeedKeys("google", map[string]*rsa.PublicKey{"k1": &key.PublicKey})
	advance(cacheTTL + time.Minute)

	for i := 0; i < 5; i++ {
		if err := verifyKid(t, key, "k1"); err != nil {
			t.Fatalf("stale keys must keep serving: %v", err)
		}
	}
	if f.hits.Load() != 1 {
		t.Fatalf("want 1 attempt per window, got %d", f.hits.Load())
	}
	advance(refreshMinAge)
	_ = verifyKid(t, key, "k1")
	if f.hits.Load() != 2 {
		t.Fatalf("want a retry after 60s, got %d", f.hits.Load())
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
