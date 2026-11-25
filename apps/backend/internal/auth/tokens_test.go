package auth_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/auth"
)

func TestNewToken(t *testing.T) {
	plain, hash := auth.NewToken("")
	if len(plain) != 43 {
		t.Fatalf("len = %d", len(plain))
	}
	raw, err := base64.RawURLEncoding.DecodeString(plain)
	if err != nil || len(raw) != 32 {
		t.Fatalf("decode: %d bytes, %v", len(raw), err)
	}
	sum := sha256.Sum256([]byte(plain))
	if !bytes.Equal(hash, sum[:]) {
		t.Fatal("the hash is not the SHA-256 of the token")
	}
	other, _ := auth.NewToken("")
	if other == plain {
		t.Fatal("two tokens are equal")
	}
}

func TestNewAPIKeyToken(t *testing.T) {
	plain, hash := auth.NewToken(auth.APIKeyPrefix)
	if !strings.HasPrefix(plain, "cx_") || len(plain) != 46 {
		t.Fatalf("token = %q", plain)
	}
	if !bytes.Equal(hash, auth.HashToken(plain)) {
		t.Fatal("hash differs from HashToken")
	}
}

func TestNormalizeEmail(t *testing.T) {
	got, err := auth.NormalizeEmail("  Ana@Example.COM ")
	if err != nil || got != "ana@example.com" {
		t.Fatalf("got %q, %v", got, err)
	}
	long := strings.Repeat("a", 255-len("@x.example")) + "@x.example"
	for _, in := range []string{"not-an-email", "Ana <ana@example.com>", "", "a@b@c", long} {
		_, err := auth.NormalizeEmail(in)
		e, ok := apierr.As(err)
		if !ok || e.Status != 400 || e.Code != "invalid_request" {
			t.Errorf("%q: err = %v", in, err)
		}
	}
}
