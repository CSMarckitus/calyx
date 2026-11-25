package auth

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"

	"calyx/backend/internal/apierr"
)

func TestHashThenVerify(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected prefix: %q", h[:30])
	}
	ok, err := VerifyPassword(h, "correct horse battery")
	if !ok || err != nil {
		t.Fatalf("VerifyPassword = %v, %v", ok, err)
	}
}

func TestTheSaltAndKeyHaveTheirSizes(t *testing.T) {
	h, _ := HashPassword("correct horse battery")
	parts := strings.Split(h, "$")
	if len(parts) != 6 {
		t.Fatalf("want 6 fields, got %d", len(parts))
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != 16 {
		t.Fatalf("salt: %d bytes, %v", len(salt), err)
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) != 32 {
		t.Fatalf("key: %d bytes, %v", len(key), err)
	}
	if strings.Contains(parts[4]+parts[5], "=") {
		t.Fatal("the salt or the key has padding")
	}
}

func TestTwoHashesOfOnePasswordDiffer(t *testing.T) {
	a, _ := HashPassword("correct horse battery")
	b, _ := HashPassword("correct horse battery")
	if a == b {
		t.Fatal("two hashes are equal")
	}
}

func TestAWrongPasswordDoesNotVerify(t *testing.T) {
	h, _ := HashPassword("correct horse battery")
	ok, err := VerifyPassword(h, "correct horse batterY")
	if ok || err != nil {
		t.Fatalf("VerifyPassword = %v, %v", ok, err)
	}
}

func TestAHashWithOtherParametersStillVerifies(t *testing.T) {
	salt := []byte("0123456789abcdef")
	enc := base64.RawStdEncoding
	for _, keyLen := range []uint32{32, 16} {
		key := argon2.IDKey([]byte("pw-for-test"), salt, 1, 8192, 2, keyLen)
		h := fmt.Sprintf("$argon2id$v=19$m=8192,t=1,p=2$%s$%s", enc.EncodeToString(salt), enc.EncodeToString(key))
		ok, err := VerifyPassword(h, "pw-for-test")
		if !ok || err != nil {
			t.Fatalf("keyLen %d: VerifyPassword = %v, %v", keyLen, ok, err)
		}
	}
}

func TestMalformedHashesAreErrors(t *testing.T) {
	for _, h := range []string{
		"",
		"$argon2i$v=19$m=19456,t=2,p=1$c2FsdA$a2V5",
		"$argon2id$v=18$m=19456,t=2,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=x,t=2,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=19456,t=2,p=1$!!$a2V5",
		"$argon2id$v=19$m=19456,t=2,p=300$c2FsdA$a2V5",
	} {
		ok, err := VerifyPassword(h, "whatever")
		if ok || err == nil {
			t.Errorf("%q: VerifyPassword = %v, %v", h, ok, err)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	bad := func(p string) {
		t.Helper()
		err := ValidatePassword("password", p)
		e, ok := apierr.As(err)
		if !ok || e.Status != 400 || e.Code != "invalid_request" || len(e.Errors) != 1 || e.Errors[0].Field != "password" {
			t.Errorf("%d runes: err = %v", len([]rune(p)), err)
		}
	}
	good := func(p string) {
		t.Helper()
		if err := ValidatePassword("password", p); err != nil {
			t.Errorf("%d runes: err = %v", len([]rune(p)), err)
		}
	}
	bad(strings.Repeat("a", 9))
	good(strings.Repeat("a", 10))
	good(strings.Repeat("a", 128))
	bad(strings.Repeat("a", 129))
	good(strings.Repeat("é", 10))
	bad(strings.Repeat("é", 129))
}

func TestAFifthConcurrentHashWaits(t *testing.T) {
	for i := 0; i < MaxConcurrentHashes; i++ {
		hashSlots <- struct{}{}
	}
	done := make(chan struct{})
	go func() {
		_, _ = HashPassword("0123456789")
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("the fifth hash did not wait")
	case <-time.After(200 * time.Millisecond):
	}
	<-hashSlots
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the hash did not finish after a slot was free")
	}
	for len(hashSlots) > 0 {
		<-hashSlots
	}
}
