package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/mail"
	"strings"

	"calyx/backend/internal/apierr"
)

const APIKeyPrefix = "cx_"

const MaxEmailLength = 254 // bytes, after trimming

// NewToken returns a plain token (prefix + 32 random bytes in base64url) and its hash.
func NewToken(prefix string) (plain string, hash []byte) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err) // crypto/rand does not fail on a working system
	}
	plain = prefix + base64.RawURLEncoding.EncodeToString(raw[:])
	return plain, HashToken(plain)
}

// HashToken returns the SHA-256 of the whole plain token, prefix included.
func HashToken(plain string) []byte {
	sum := sha256.Sum256([]byte(plain))
	return sum[:]
}

// NormalizeEmail trims and lower-cases the address and checks its syntax. An error has one field
// error at "email".
func NormalizeEmail(email string) (string, error) {
	var f apierr.Fields
	e := strings.ToLower(strings.TrimSpace(email))
	if e == "" {
		f.Add("email", "is required.")
		return "", f.Err()
	}
	a, err := mail.ParseAddress(e)
	if err != nil || a.Address != e || len(e) > MaxEmailLength {
		f.Add("email", fmt.Sprintf("must be a valid email address of at most %d bytes.", MaxEmailLength))
		return "", f.Err()
	}
	return e, nil
}
