package oidc

import (
	"crypto/rsa"
	"time"
)

// SeedKeys sets a fresh JWKS cache for provider; nil keys clears it. Test hook.
func SeedKeys(provider string, keys map[string]*rsa.PublicKey) {
	c := cacheFor(provider)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys, c.fetchedAt, c.lastAttempt = keys, now(), now()
	if keys == nil {
		c.fetchedAt, c.lastAttempt = time.Time{}, time.Time{}
	}
}

// SetJWKSURL points provider at another JWKS endpoint and returns a restore func. Test hook.
func SetJWKSURL(provider, url string) func() {
	old := jwksURLs[provider]
	jwksURLs[provider] = url
	return func() { jwksURLs[provider] = old }
}
