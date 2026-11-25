package watcher

import "time"

// SetHeaderWait shortens the wait for the headers, so a test of "no headers" does not wait 10 s.
func SetHeaderWait(l *Listener, d time.Duration) { l.headerWait = d }
