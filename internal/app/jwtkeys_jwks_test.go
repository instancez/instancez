package app

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
)

func TestPublicJWK_RS256(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	k := &JWTKey{KID: "kid1", Algorithm: "RS256", PrivateKey: priv, PublicKey: &priv.PublicKey}

	jwk, err := k.PublicJWK()
	if err != nil {
		t.Fatalf("PublicJWK: %v", err)
	}
	if jwk["kty"] != "RSA" || jwk["alg"] != "RS256" || jwk["use"] != "sig" {
		t.Fatalf("unexpected header fields: %v", jwk)
	}
	if jwk["kid"] != "kid1" {
		t.Fatalf("kid = %v", jwk["kid"])
	}
	if jwk["n"] == "" || jwk["e"] == "" {
		t.Fatalf("missing modulus/exponent: %v", jwk)
	}
	// Never leak private material.
	if _, bad := jwk["d"]; bad {
		t.Fatal("JWK leaked private exponent")
	}
}

func TestPublicJWK_NoPublicKey(t *testing.T) {
	k := &JWTKey{KID: "kid1", Algorithm: "HS256", Secret: []byte("x")}
	if _, err := k.PublicJWK(); err == nil {
		t.Fatal("expected error for key without RSA public material")
	}
}
