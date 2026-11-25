// Package auth holds passwords, tokens, sessions, API keys, invitations, password resets and the
// rate limiter. Plain tokens leave this package once; the database holds only their hashes.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"calyx/backend/internal/apierr"
)

const (
	MinPasswordLength   = 10  // runes
	MaxPasswordLength   = 128 // runes
	MaxConcurrentHashes = 4
)

// Parameters of new hashes. The memory is in KiB.
const (
	argonTime    = 2
	argonMemory  = 19456
	argonThreads = 1
	argonSaltLen = 16
	argonKeyLen  = 32
)

// hashSlots limits the number of argon2id computations that run at once.
var hashSlots = make(chan struct{}, MaxConcurrentHashes)

func computeKey(password string, salt []byte, time, memory uint32, threads uint8, keyLen uint32) []byte {
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	return argon2.IDKey([]byte(password), salt, time, memory, threads, keyLen)
}

// HashPassword returns the PHC string of the password with the current parameters.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := computeKey(password, salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

var errBadHash = errors.New("the password hash is not valid")

// VerifyPassword compares the password with a PHC string. The parameters come from the string.
// A wrong password gives false and a nil error. A malformed string gives an error.
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, errBadHash
	}
	if parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return false, errBadHash
	}
	var m, t, p uint64
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return false, errBadHash
	}
	for i, want := range []string{"m=", "t=", "p="} {
		if !strings.HasPrefix(params[i], want) {
			return false, errBadHash
		}
		v, err := strconv.ParseUint(strings.TrimPrefix(params[i], want), 10, 32)
		if err != nil {
			return false, errBadHash
		}
		switch i {
		case 0:
			m = v
		case 1:
			t = v
		case 2:
			p = v
		}
	}
	if p == 0 || p > 255 || t == 0 || m == 0 {
		return false, errBadHash
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil {
		return false, errBadHash
	}
	key, err := enc.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return false, errBadHash
	}
	got := computeKey(password, salt, uint32(t), uint32(m), uint8(p), uint32(len(key)))
	return subtle.ConstantTimeCompare(got, key) == 1, nil
}

// ValidatePassword returns nil, or 400 invalid_request with a field error at field. It counts
// runes.
func ValidatePassword(field, password string) error {
	var f apierr.Fields
	if n := utf8.RuneCountInString(password); n < MinPasswordLength || n > MaxPasswordLength {
		f.Add(field, fmt.Sprintf("must have %d to %d characters.", MinPasswordLength, MaxPasswordLength))
	}
	return f.Err()
}

// requireAll adds "is required." for each empty value. pairs holds a field and its value, in turn.
func requireAll(pairs ...string) error {
	var f apierr.Fields
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] == "" {
			f.Add(pairs[i], "is required.")
		}
	}
	return f.Err()
}
