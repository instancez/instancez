package app

import (
	"context"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// serviceTokenSub is the synthetic subject used for inz-minted service-role
// tokens. The request middleware downgrades tokens with an empty `sub` claim
// to the anon role (see internal/adapter/http/middleware.go), which would
// silently defeat escalation, so a stable non-empty UUID is always emitted.
//
// The synthetic sub is an internal detail: it satisfies the middleware's
// non-empty-`sub` requirement but is NEVER surfaced to auth.uid(). The
// per-request role switch (internal/adapter/postgres buildSessionSetup) leaves
// app.user_id empty whenever the assumed role is service_role, so auth.uid()
// resolves to NULL for ctx.serviceClient calls, matching Supabase.
const serviceTokenSub = "00000000-0000-0000-0000-000000000000"

// MintServiceToken signs a short-lived service_role JWT using the active key
// from km. The token validates in the same request middleware as user
// sessions: it carries a non-empty `sub`, `role: "service_role"`, `iat`, and a
// `ttl`-bounded `exp`, signed with the active key's algorithm and `kid` header.
//
// Functions use this for explicit escalation (ctx.serviceClient), which maps to
// a BYPASSRLS Postgres role.
func MintServiceToken(ctx context.Context, km *JWTKeyManager, ttl time.Duration) (string, error) {
	if km == nil {
		return "", fmt.Errorf("funcs: mint service token: nil key manager")
	}
	key, err := km.Active(ctx)
	if err != nil {
		return "", fmt.Errorf("funcs: mint service token: active key: %w", err)
	}

	now := time.Now()
	claims := jwt.MapClaims{
		"iss":  "instancez",
		"sub":  serviceTokenSub,
		"aud":  "authenticated",
		"role": "service_role",
		"iat":  now.Unix(),
		"exp":  now.Add(ttl).Unix(),
	}

	var signingMethod jwt.SigningMethod
	var signingKey any
	switch key.Algorithm {
	case "RS256":
		signingMethod = jwt.SigningMethodRS256
		signingKey = key.PrivateKey
	default:
		signingMethod = jwt.SigningMethodHS256
		signingKey = key.Secret
	}

	token := jwt.NewWithClaims(signingMethod, claims)
	token.Header["kid"] = key.KID
	signed, err := token.SignedString(signingKey)
	if err != nil {
		return "", fmt.Errorf("funcs: mint service token: sign: %w", err)
	}
	return signed, nil
}

// MintAnonToken signs a role=anon JWT using the active key from km. This is the
// "anon key" the injected data-access clients (ctx.supabase, anonymous calls)
// send as the `apikey` header and as the Bearer token for anonymous requests.
//
// Unlike a user token it carries NO `sub` claim: the request middleware pins an
// empty-sub bearer token to the anon role, which is exactly what we want. ttl
// is long (these tokens are minted once at boot and embedded in every worker
// context) but the token still has an `exp` so it satisfies the middleware's
// WithExpirationRequired() check.
func MintAnonToken(ctx context.Context, km *JWTKeyManager, ttl time.Duration) (string, error) {
	if km == nil {
		return "", fmt.Errorf("funcs: mint anon token: nil key manager")
	}
	key, err := km.Active(ctx)
	if err != nil {
		return "", fmt.Errorf("funcs: mint anon token: active key: %w", err)
	}

	now := time.Now()
	claims := jwt.MapClaims{
		"iss":  "instancez",
		"role": "anon",
		"iat":  now.Unix(),
		"exp":  now.Add(ttl).Unix(),
	}

	var signingMethod jwt.SigningMethod
	var signingKey any
	switch key.Algorithm {
	case "RS256":
		signingMethod = jwt.SigningMethodRS256
		signingKey = key.PrivateKey
	default:
		signingMethod = jwt.SigningMethodHS256
		signingKey = key.Secret
	}

	token := jwt.NewWithClaims(signingMethod, claims)
	token.Header["kid"] = key.KID
	signed, err := token.SignedString(signingKey)
	if err != nil {
		return "", fmt.Errorf("funcs: mint anon token: sign: %w", err)
	}
	return signed, nil
}

// MintStableAnonKey signs the project's publishable anon key — the value the
// dashboard shows for client apps to copy, mirroring Supabase's Settings → API
// anon key. Unlike MintAnonToken its claims are anchored to the signing key's
// creation time (iat = created_at, exp = created_at + 10 years, the Supabase
// anon-key lifetime), so the token is byte-identical across requests and
// restarts and only changes when the signing key rotates.
func MintStableAnonKey(ctx context.Context, km *JWTKeyManager) (string, error) {
	if km == nil {
		return "", fmt.Errorf("anon key: nil key manager")
	}
	key, err := km.Active(ctx)
	if err != nil {
		return "", fmt.Errorf("anon key: active key: %w", err)
	}

	anchor := key.CreatedAt
	if anchor.IsZero() {
		// No creation time on the key (e.g. legacy rows read before created_at
		// was selected). The token is still valid, just not stable.
		anchor = time.Now().UTC()
	}
	anchor = anchor.Truncate(time.Second)

	claims := jwt.MapClaims{
		"iss":  "instancez",
		"role": "anon",
		"iat":  anchor.Unix(),
		"exp":  anchor.AddDate(10, 0, 0).Unix(),
	}

	var signingMethod jwt.SigningMethod
	var signingKey any
	switch key.Algorithm {
	case "RS256":
		signingMethod = jwt.SigningMethodRS256
		signingKey = key.PrivateKey
	default:
		signingMethod = jwt.SigningMethodHS256
		signingKey = key.Secret
	}

	token := jwt.NewWithClaims(signingMethod, claims)
	token.Header["kid"] = key.KID
	signed, err := token.SignedString(signingKey)
	if err != nil {
		return "", fmt.Errorf("anon key: sign: %w", err)
	}
	return signed, nil
}
