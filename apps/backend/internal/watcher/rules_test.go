package watcher_test

import (
	"testing"
	"time"

	"calyx/backend/internal/watcher"
)

var (
	now   = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	grace = 5 * time.Minute
)

func s(x string) *string            { return &x }
func at(d time.Duration) *time.Time { t := now.Add(d); return &t }

func TestEvaluateTable(t *testing.T) {
	active := func(st string) watcher.Facts { return watcher.Facts{State: "active", Enabled: true, LastStatus: s(st)} }
	mailed := func(kind string) watcher.Alert {
		return watcher.Alert{Kind: kind, OpenedAt: at(-time.Hour), AlertedAt: at(-time.Hour)}
	}
	opened := func(kind string, ago time.Duration) watcher.Alert {
		return watcher.Alert{Kind: kind, OpenedAt: at(-ago)}
	}
	fresh := func(kind string) watcher.Alert { return watcher.Alert{Kind: kind, OpenedAt: &now} }
	pending := watcher.Alert{Kind: "needs_auth", OpenedAt: at(-time.Hour), Attempts: 2, NextAttemptAt: at(3 * time.Minute)}
	due1 := watcher.Alert{Kind: "needs_auth", OpenedAt: at(-time.Hour), Attempts: 2, NextAttemptAt: at(-time.Minute)}

	cases := []struct {
		n    int
		f    watcher.Facts
		cur  watcher.Alert
		next watcher.Alert
		due  bool
	}{
		{1, watcher.Facts{State: "active", Enabled: false, LastStatus: s("NEEDS_AUTH")}, mailed("needs_auth"), watcher.Alert{}, false},
		{2, watcher.Facts{State: "creating", Enabled: true, LastStatus: s("DISCONNECTED")}, opened("disconnected", time.Minute), watcher.Alert{}, false},
		{3, watcher.Facts{State: "removing", Enabled: true, LastStatus: s("ERROR")}, mailed("error"), watcher.Alert{}, false},
		{4, watcher.Facts{State: "missing", Enabled: true, LastStatus: s("NEEDS_AUTH")}, watcher.Alert{}, fresh("missing"), true},
		{5, watcher.Facts{State: "missing", Enabled: true}, mailed("missing"), mailed("missing"), false},
		{6, watcher.Facts{State: "missing", Enabled: true}, mailed("needs_auth"), fresh("missing"), true},
		{7, active("NEEDS_AUTH"), watcher.Alert{}, fresh("needs_auth"), true},
		{8, active("ERROR"), watcher.Alert{}, fresh("error"), true},
		{9, active("NEEDS_AUTH"), opened("disconnected", 2*time.Minute), fresh("needs_auth"), true},
		{10, active("NEEDS_AUTH"), mailed("disconnected"), fresh("needs_auth"), true},
		{11, active("NEEDS_AUTH"), mailed("needs_auth"), mailed("needs_auth"), false},
		{12, active("NEEDS_AUTH"), pending, pending, false},
		{13, active("NEEDS_AUTH"), due1, due1, true},
		{14, active("ERROR"), mailed("needs_auth"), fresh("error"), true},
		{15, active("NEEDS_AUTH"), mailed("error"), fresh("needs_auth"), true},
		{16, active("DISCONNECTED"), watcher.Alert{}, fresh("disconnected"), false},
		{17, active("CONNECTING"), watcher.Alert{}, fresh("disconnected"), false},
		{18, active("DISCONNECTED"), opened("disconnected", 4*time.Minute+59*time.Second), opened("disconnected", 4*time.Minute+59*time.Second), false},
		{19, active("DISCONNECTED"), opened("disconnected", 5*time.Minute), opened("disconnected", 5*time.Minute), true},
		{20, active("DISCONNECTED"), mailed("needs_auth"), mailed("needs_auth"), false},
		{21, active("DISCONNECTED"), opened("missing", time.Hour), fresh("disconnected"), false},
		{26, active("CONNECTING"), mailed("missing"), fresh("disconnected"), false},
		{22, active("CONNECTED"), mailed("needs_auth"), watcher.Alert{}, false},
		{23, active("CONNECTED"), watcher.Alert{}, watcher.Alert{}, false},
		{24, watcher.Facts{State: "active", Enabled: true}, watcher.Alert{}, watcher.Alert{}, false},
		{25, active("UNSPECIFIED"), opened("disconnected", time.Minute), opened("disconnected", time.Minute), false},
	}
	for _, c := range cases {
		next, due := watcher.Evaluate(c.f, c.cur, now, grace)
		if !alertEqual(next, c.next) || due != c.due {
			t.Errorf("case %d: next %+v due %v; want %+v due %v", c.n, next, due, c.next, c.due)
		}
	}
}

func alertEqual(a, b watcher.Alert) bool {
	eq := func(x, y *time.Time) bool {
		if x == nil || y == nil {
			return x == y
		}
		return x.Equal(*y)
	}
	return a.Kind == b.Kind && a.Attempts == b.Attempts &&
		eq(a.OpenedAt, b.OpenedAt) && eq(a.AlertedAt, b.AlertedAt) && eq(a.NextAttemptAt, b.NextAttemptAt)
}

func TestBackoff(t *testing.T) {
	m := time.Minute
	for attempts, want := range map[int32]time.Duration{-1: 0, 0: 0, 1: 2 * m, 2: 4 * m, 3: 8 * m, 4: 16 * m, 5: 32 * m, 6: 60 * m, 10: 60 * m, 1000: 60 * m} {
		if got := watcher.Backoff(attempts); got != want {
			t.Errorf("Backoff(%d) = %v, want %v", attempts, got, want)
		}
	}
}
