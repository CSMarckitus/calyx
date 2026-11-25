package bulk

import (
	"testing"
	"time"
)

// SetListTimeout changes the total limit of ListForInstance for one test.
func SetListTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := listTimeout
	listTimeout = d
	t.Cleanup(func() { listTimeout = old })
}
