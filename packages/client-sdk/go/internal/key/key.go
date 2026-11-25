// Package key makes the idempotency keys that the client sends with each write.
package key

import (
	"crypto/rand"
	"fmt"
)

// New returns a random version 4 UUID, such as
// "3f1c2a4e-9b7d-4c8a-8e21-5d6f0a7b9c13", for use as an idempotency key.
func New() string {
	var b [16]byte
	// Since Go 1.24, rand.Read never returns an error. It stops the program if the
	// system random source fails, so the error is ignored here.
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
