package mail

import "time"

// SetSendTimeout changes the send limit for a test. The returned function restores it.
func SetSendTimeout(d time.Duration) (restore func()) {
	old := sendTimeout
	sendTimeout = d
	return func() { sendTimeout = old }
}
