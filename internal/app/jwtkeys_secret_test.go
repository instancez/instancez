package app

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"testing"
)

func TestSymmetricSecret_NilKey(t *testing.T) {
	var k *JWTKey
	if got := k.SymmetricSecret(); got != nil {
		t.Fatalf("nil key: got %v, want nil", got)
	}
}

func TestSymmetricSecret_HS256UsesSecretDirectly(t *testing.T) {
	k := &JWTKey{Algorithm: "HS256", Secret: []byte("hs256-secret")}
	got := k.SymmetricSecret()
	if !bytes.Equal(got, []byte("hs256-secret")) {
		t.Fatalf("got %q, want %q", got, "hs256-secret")
	}
}

func TestSymmetricSecret_RS256DerivedIsStableAndNonEmpty(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	k := &JWTKey{Algorithm: "RS256", PrivateKey: priv, PublicKey: &priv.PublicKey}

	first := k.SymmetricSecret()
	if len(first) == 0 {
		t.Fatal("expected non-empty derived secret for RS256 key")
	}
	second := k.SymmetricSecret()
	if !bytes.Equal(first, second) {
		t.Fatalf("derived secret not stable across calls: %x vs %x", first, second)
	}
}

func TestSymmetricSecret_RS256DiffersPerKey(t *testing.T) {
	priv1, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	priv2, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	k1 := &JWTKey{Algorithm: "RS256", PrivateKey: priv1}
	k2 := &JWTKey{Algorithm: "RS256", PrivateKey: priv2}

	if bytes.Equal(k1.SymmetricSecret(), k2.SymmetricSecret()) {
		t.Fatal("derived secrets for two distinct RSA keys must differ")
	}
}

func TestSymmetricSecret_NoMaterialReturnsNil(t *testing.T) {
	k := &JWTKey{Algorithm: "HS256"}
	if got := k.SymmetricSecret(); got != nil {
		t.Fatalf("key with no secret and no private key: got %v, want nil", got)
	}
}
