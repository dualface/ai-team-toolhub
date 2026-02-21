package github

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"testing"
)

func TestParseRSAPrivateKeyPKCS1AndPKCS8(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}

	pkcs1 := x509.MarshalPKCS1PrivateKey(key)
	parsed1, err := parseRSAPrivateKey(pkcs1)
	if err != nil {
		t.Fatalf("parse pkcs1: %v", err)
	}
	if parsed1.N.Cmp(key.N) != 0 {
		t.Fatal("parsed pkcs1 key does not match original")
	}

	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	parsed8, err := parseRSAPrivateKey(pkcs8)
	if err != nil {
		t.Fatalf("parse pkcs8: %v", err)
	}
	if parsed8.N.Cmp(key.N) != 0 {
		t.Fatal("parsed pkcs8 key does not match original")
	}
}
