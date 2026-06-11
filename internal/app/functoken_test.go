package app

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestMintServiceToken(t *testing.T) {
	km, err := NewInMemoryJWTKeyManager("kid1", nil)
	if err != nil {
		t.Fatalf("new key manager: %v", err)
	}

	ctx := context.Background()
	tok, err := MintServiceToken(ctx, km, 30*time.Second)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if tok == "" {
		t.Fatal("empty token")
	}

	active, err := km.Active(ctx)
	if err != nil {
		t.Fatalf("active key: %v", err)
	}

	parsed, err := jwt.Parse(tok, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			t.Fatalf("unexpected signing method: %v", token.Header["alg"])
		}
		if kid, _ := token.Header["kid"].(string); kid != active.KID {
			t.Fatalf("kid header = %q, want %q", kid, active.KID)
		}
		return active.PublicKey, nil
	}, jwt.WithExpirationRequired())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !parsed.Valid {
		t.Fatal("token not valid")
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatalf("claims type %T", parsed.Claims)
	}
	if claims["role"] != "service_role" {
		t.Fatalf("role = %v, want service_role", claims["role"])
	}
	// sub must be present and non-empty — the request middleware downgrades
	// tokens with an empty sub to the anon role, defeating escalation.
	if sub, _ := claims["sub"].(string); sub == "" {
		t.Fatalf("sub claim missing/empty: %v", claims["sub"])
	}

	exp, err := claims.GetExpirationTime()
	if err != nil || exp == nil {
		t.Fatalf("exp claim: %v", err)
	}
	if !exp.After(time.Now()) {
		t.Fatalf("token already expired: exp=%v", exp.Time)
	}
	if exp.After(time.Now().Add(2 * time.Minute)) {
		t.Fatalf("token exp too far out for ttl=30s: exp=%v", exp.Time)
	}
}

// TestMintServiceToken_HS256 covers the HS256 signing branch. HS256 is a
// supported active-key algorithm (rowToKey handles it); we construct the
// JWTKeyManager directly using unexported fields since no in-memory
// constructor for HS256 is exposed publicly.
func TestMintServiceToken_HS256(t *testing.T) {
	secret := []byte("test-hs256-secret-at-least-32-bytes!")
	key := &JWTKey{
		KID:       "hs-kid1",
		Algorithm: "HS256",
		Secret:    secret,
	}
	km := &JWTKeyManager{
		active: key,
		byKID:  map[string]*JWTKey{"hs-kid1": key},
	}

	ctx := context.Background()
	tok, err := MintServiceToken(ctx, km, 30*time.Second)
	if err != nil {
		t.Fatalf("mint HS256: %v", err)
	}
	if tok == "" {
		t.Fatal("empty token")
	}

	parsed, err := jwt.Parse(tok, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			t.Fatalf("unexpected signing method: %v", token.Header["alg"])
		}
		if kid, _ := token.Header["kid"].(string); kid != key.KID {
			t.Fatalf("kid header = %q, want %q", kid, key.KID)
		}
		return secret, nil
	}, jwt.WithExpirationRequired())
	if err != nil {
		t.Fatalf("parse HS256: %v", err)
	}
	if !parsed.Valid {
		t.Fatal("token not valid")
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatalf("claims type %T", parsed.Claims)
	}
	if claims["role"] != "service_role" {
		t.Fatalf("role = %v, want service_role", claims["role"])
	}
	if sub, _ := claims["sub"].(string); sub == "" {
		t.Fatalf("sub claim missing/empty: %v", claims["sub"])
	}
}
