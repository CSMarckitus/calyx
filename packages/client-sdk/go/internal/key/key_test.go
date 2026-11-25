package key

import (
	"regexp"
	"testing"
)

// uuidV4 matches a version 4 UUID with the RFC 4122 variant.
var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewFormat(t *testing.T) {
	for i := 0; i < 100; i++ {
		if k := New(); !uuidV4.MatchString(k) {
			t.Fatalf("New() = %q, want a version 4 UUID", k)
		}
	}
}

func TestNewIsUnique(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		k := New()
		if seen[k] {
			t.Fatalf("New() repeated %q after %d keys", k, i)
		}
		seen[k] = true
	}
}
