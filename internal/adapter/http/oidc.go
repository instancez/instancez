package http

import (
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// OIDC provider JWKS URLs
var oidcJWKSURLs = map[string]string{
	"google": "https://www.googleapis.com/oauth2/v3/certs",
}

var oidcIssuers = map[string][]string{
	"google": {"https://accounts.google.com", "accounts.google.com"},
}

type jwksCache struct {
	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

var providerJWKS = struct {
	mu    sync.Mutex
	cache map[string]*jwksCache
}{cache: make(map[string]*jwksCache)}

func getJWKS(provider string) (map[string]*rsa.PublicKey, error) {
	jwksURL, ok := oidcJWKSURLs[provider]
	if !ok {
		return nil, fmt.Errorf("no JWKS URL for provider %s", provider)
	}

	providerJWKS.mu.Lock()
	c, ok := providerJWKS.cache[provider]
	if !ok {
		c = &jwksCache{}
		providerJWKS.cache[provider] = c
	}
	providerJWKS.mu.Unlock()

	c.mu.RLock()
	if time.Since(c.fetchedAt) < time.Hour && len(c.keys) > 0 {
		keys := c.keys
		c.mu.RUnlock()
		return keys, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if time.Since(c.fetchedAt) < time.Hour && len(c.keys) > 0 {
		return c.keys, nil
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(jwksURL)
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var jwks struct {
		Keys []struct {
			KID string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
			Kty string `json:"kty"`
			Use string `json:"use"`
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
		n := new(big.Int).SetBytes(nBytes)
		e := int(new(big.Int).SetBytes(eBytes).Int64())
		keys[k.KID] = &rsa.PublicKey{N: n, E: e}
	}

	c.keys = keys
	c.fetchedAt = time.Now()
	return keys, nil
}

func verifyIDToken(provider, tokenStr, expectedAudience, expectedNonce string) (jwt.MapClaims, error) {
	keys, err := getJWKS(provider)
	if err != nil {
		return nil, err
	}

	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		kid, _ := t.Header["kid"].(string)
		key, ok := keys[kid]
		if !ok {
			return nil, fmt.Errorf("unknown key ID: %s", kid)
		}
		return key, nil
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

	// Verify issuer
	iss, _ := claims["iss"].(string)
	validIssuers := oidcIssuers[provider]
	issValid := false
	for _, vi := range validIssuers {
		if iss == vi {
			issValid = true
			break
		}
	}
	if !issValid {
		return nil, fmt.Errorf("invalid issuer: %s", iss)
	}

	// Verify audience
	aud, _ := claims["aud"].(string)
	if aud != expectedAudience {
		return nil, fmt.Errorf("audience mismatch: got %s, want %s", aud, expectedAudience)
	}

	// Verify nonce if provided
	if expectedNonce != "" {
		nonce, _ := claims["nonce"].(string)
		if nonce != expectedNonce {
			return nil, fmt.Errorf("nonce mismatch")
		}
	}

	return claims, nil
}

// verifyCodeChallenge verifies a PKCE code_verifier against the stored code_challenge.
// method is matched case-insensitively: real @supabase/supabase-js sends a
// lowercase "s256" (see getCodeChallengeAndMethod in auth-js), not the
// uppercase "S256" the PKCE RFC uses in prose.
func verifyCodeChallenge(method, verifier, challenge string) bool {
	switch strings.ToUpper(method) {
	case "S256", "":
		h := sha256.Sum256([]byte(verifier))
		computed := base64.RawURLEncoding.EncodeToString(h[:])
		return computed == challenge
	case "PLAIN":
		return verifier == challenge
	default:
		return false
	}
}
