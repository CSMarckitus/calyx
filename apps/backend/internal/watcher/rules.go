// Package watcher compares the state of every instance with the core, opens and closes alerts,
// and sends the alert mails. It keeps no state in memory: every alert field lives in the database.
package watcher

import (
	"time"

	"calyx/backend/internal/core"
)

// Facts is what the rules read from an instance row.
type Facts struct {
	State      string // "creating", "active", "removing", "missing"
	Enabled    bool
	LastStatus *string // "CONNECTED", "CONNECTING", "DISCONNECTED", "NEEDS_AUTH", "ERROR", or nil
}

// Alert is the alert part of an instance row. Kind "" means no alert is open.
type Alert struct {
	Kind          string // "", "needs_auth", "error", "disconnected", "missing"
	OpenedAt      *time.Time
	AlertedAt     *time.Time
	Attempts      int32
	NextAttemptAt *time.Time
}

// Evaluate applies the alert rules and says whether a mail is due now.
// The conditions are checked in order; the first that matches decides the next alert.
func Evaluate(f Facts, cur Alert, now time.Time, grace time.Duration) (next Alert, due bool) {
	// replace opens a new alert of kind k, unless an alert of that kind is already open.
	replace := func(k string) Alert {
		if cur.Kind == k {
			return cur
		}
		return Alert{Kind: k, OpenedAt: &now}
	}
	status := ""
	if f.LastStatus != nil {
		status = *f.LastStatus
	}
	switch {
	case !f.Enabled || f.State == "creating" || f.State == "removing":
		next = Alert{}
	case f.State == "missing":
		next = replace("missing")
	case status == core.StatusNeedsAuth:
		next = replace("needs_auth")
	case status == core.StatusError:
		next = replace("error")
	case status == core.StatusDisconnected || status == core.StatusConnecting:
		// A stale "missing" alert is replaced: the instance is back in the core.
		next = cur
		if cur.Kind == "" || cur.Kind == "missing" {
			next = Alert{Kind: "disconnected", OpenedAt: &now}
		}
	case status == core.StatusConnected:
		next = Alert{}
	default:
		next = cur
	}

	due = next.Kind != "" && next.AlertedAt == nil &&
		(next.NextAttemptAt == nil || !now.Before(*next.NextAttemptAt)) &&
		(next.Kind != "disconnected" || (next.OpenedAt != nil && now.Sub(*next.OpenedAt) >= grace))
	return next, due
}

// Backoff is the wait after the given number of failed mails: min(2^attempts minutes, 60 minutes).
func Backoff(attempts int32) time.Duration {
	if attempts <= 0 {
		return 0
	}
	if attempts >= 6 {
		return time.Hour
	}
	return time.Duration(1<<attempts) * time.Minute
}
