// Package oidc verifies Google and Apple id_tokens against the provider's published JWKS.
package oidc

import (
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	cacheTTL      = time.Hour
	refreshMinAge = time.Minute
)

var (
	jwksURLs = map[string]string{
		"google": "https://www.googleapis.com/oauth2/v3/certs",
		"apple":  "https://appleid.apple.com/auth/keys",
	}
	issuers = map[string][]string{
		"google": {"https://accounts.google.com", "accounts.google.com"},
		"apple":  {"https://appleid.apple.com"},
	}
)

type jwksCache struct {
	mu          sync.Mutex
	keys        map[string]*rsa.PublicKey
	fetchedAt   time.Time
	lastAttempt time.Time
	inflight    chan struct{}
}

var now = time.Now

var errKeysUnavailable = errors.New("signing keys unavailable")

var caches = struct {
	mu sync.Mutex
	m  map[string]*jwksCache
}{m: make(map[string]*jwksCache)}

// Supported reports whether id_tokens from provider can be verified.
func Supported(provider string) bool {
	_, ok := jwksURLs[provider]
	return ok
}

func cacheFor(provider string) *jwksCache {
	caches.mu.Lock()
	defer caches.mu.Unlock()
	c, ok := caches.m[provider]
	if !ok {
		c = &jwksCache{}
		caches.m[provider] = c
	}
	return c
}

// key returns the signing key for kid. A fetch runs outside the lock, at most once per minute
// (failures count), and stale keys keep serving while refreshes fail.
func (c *jwksCache) key(jwksURL, kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	k, known := c.keys[kid]
	if known && now().Sub(c.fetchedAt) < cacheTTL {
		c.mu.Unlock()
		return k, nil
	}
	if ch := c.inflight; ch != nil {
		c.mu.Unlock()
		if known {
			return k, nil
		}
		<-ch
		return c.lookup(kid)
	}
	if !c.lastAttempt.IsZero() && now().Sub(c.lastAttempt) < refreshMinAge {
		c.mu.Unlock()
		if known {
			return k, nil
		}
		return nil, c.missing(kid)
	}
	ch := make(chan struct{})
	c.inflight, c.lastAttempt = ch, now()
	c.mu.Unlock()

	keys, err := fetchJWKS(jwksURL)

	c.mu.Lock()
	if err == nil && len(keys) > 0 {
		c.keys, c.fetchedAt = keys, now()
	}
	c.inflight = nil
	close(ch)
	c.mu.Unlock()

	if k, err := c.lookup(kid); err == nil {
		return k, nil
	}
	if err != nil {
		return nil, errKeysUnavailable
	}
	return nil, fmt.Errorf("unknown key ID: %s", kid)
}

func (c *jwksCache) lookup(kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if k, ok := c.keys[kid]; ok {
		return k, nil
	}
	return nil, c.missing(kid)
}

// missing must be called with c.mu held.
func (c *jwksCache) missing(kid string) error {
	if len(c.keys) == 0 {
		return errKeysUnavailable
	}
	return fmt.Errorf("unknown key ID: %s", kid)
}

func fetchJWKS(jwksURL string) (map[string]*rsa.PublicKey, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(jwksURL)
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch JWKS: status %d", resp.StatusCode)
	}

	var jwks struct {
		Keys []struct {
			KID string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
			Kty string `json:"kty"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("decode JWKS: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" {
			continue
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		e := int(new(big.Int).SetBytes(eBytes).Int64())
		keys[k.KID] = &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}
	}
	return keys, nil
}

// Verify checks an id_token's RSA signature, issuer, audience and nonce.
func Verify(provider, tokenStr string, audiences []string, expectedNonce string) (jwt.MapClaims, error) {
	jwksURL, ok := jwksURLs[provider]
	if !ok {
		return nil, fmt.Errorf("no JWKS URL for provider %s", provider)
	}
	c := cacheFor(provider)

	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		kid, _ := t.Header["kid"].(string)
		return c.key(jwksURL, kid)
	})
	if err != nil {
		return nil, fmt.Errorf("token parse: %w", err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("token invalid")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("unexpected claims type")
	}

	iss, _ := claims["iss"].(string)
	if !slices.Contains(issuers[provider], iss) {
		return nil, fmt.Errorf("invalid issuer: %s", iss)
	}

	tokenAud, _ := claims.GetAudience()
	if !slices.ContainsFunc(tokenAud, func(a string) bool { return slices.Contains(audiences, a) }) {
		return nil, fmt.Errorf("audience mismatch: got %v, want one of %v", []string(tokenAud), audiences)
	}

	claimNonce, _ := claims["nonce"].(string)
	if (claimNonce == "") != (expectedNonce == "") {
		return nil, fmt.Errorf("nonce in id_token and params should either both exist or not")
	}
	if expectedNonce != "" {
		want := expectedNonce
		if provider == "apple" {
			hashed := sha256.Sum256([]byte(expectedNonce))
			want = hex.EncodeToString(hashed[:])
		}
		if claimNonce != want {
			return nil, fmt.Errorf("nonce mismatch")
		}
	}

	return claims, nil
}
