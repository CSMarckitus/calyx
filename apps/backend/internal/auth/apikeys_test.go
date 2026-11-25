package auth_test

import (
	"context"
	"crypto/sha256"
	"strings"
	"testing"
	"time"

	"calyx/backend/internal/auth"
)

func TestCreateAPIKey(t *testing.T) {
	e, _, owner := setup(t)
	k, err := e.svc.CreateAPIKey(context.Background(), principalOf(owner), "  n8n ")
	if err != nil {
		t.Fatal(err)
	}
	if k.Name != "n8n" || !strings.HasPrefix(k.Token, "cx_") || len(k.Token) != 46 || k.Prefix != k.Token[3:11] || k.LastUsedAt != nil {
		t.Fatalf("key = %+v", k)
	}
	h := sha256.Sum256([]byte(k.Token))
	if n := countRows(t, e.pool, `SELECT count(*) FROM api_keys WHERE token_hash = $1`, h[:]); n != 1 {
		t.Fatalf("rows = %d", n)
	}
}

func TestTheAPIKeyAuthenticates(t *testing.T) {
	e, org, owner := setup(t)
	ctx := context.Background()
	k, _ := e.svc.CreateAPIKey(ctx, principalOf(owner), "k")
	p, err := e.svc.Authenticate(ctx, "", k.Token)
	if err != nil {
		t.Fatal(err)
	}
	if p.UserID != owner.ID || p.OrganizationID != org || p.ClientID != nil {
		t.Fatalf("principal = %+v", p)
	}
	if p.APIKeyID == nil || *p.APIKeyID != k.ID {
		t.Fatalf("APIKeyID = %v, want %v", p.APIKeyID, k.ID)
	}
	cookie, err := e.svc.Authenticate(ctx, login(t, e, "ana@x.example").Token, "")
	if err != nil {
		t.Fatal(err)
	}
	if cookie.APIKeyID != nil {
		t.Fatalf("cookie APIKeyID = %v, want nil", cookie.APIKeyID)
	}
	last := func() time.Time {
		var ts time.Time
		if err := e.pool.QueryRow(ctx, `SELECT last_used_at FROM api_keys WHERE id=$1`, k.ID).Scan(&ts); err != nil {
			t.Fatal(err)
		}
		return ts
	}
	if !last().Equal(start) {
		t.Fatalf("last_used_at = %v", last())
	}
	e.clk.Advance(time.Hour)
	if _, err := e.svc.Authenticate(ctx, "", k.Token); err != nil {
		t.Fatal(err)
	}
	if !last().Equal(start.Add(time.Hour)) {
		t.Fatalf("last_used_at = %v", last())
	}
}

func TestLastUsedAtChangesAtMostOnceAMinute(t *testing.T) {
	e, _, owner := setup(t)
	ctx := context.Background()
	k, _ := e.svc.CreateAPIKey(ctx, principalOf(owner), "k")
	use := func() time.Time {
		if _, err := e.svc.Authenticate(ctx, "", k.Token); err != nil {
			t.Fatal(err)
		}
		var ts time.Time
		if err := e.pool.QueryRow(ctx, `SELECT last_used_at FROM api_keys WHERE id=$1`, k.ID).Scan(&ts); err != nil {
			t.Fatal(err)
		}
		return ts
	}
	first := use()
	e.clk.Advance(10 * time.Second)
	if got := use(); !got.Equal(first) {
		t.Fatalf("after 10 s last_used_at = %v, want %v", got, first)
	}
	e.clk.Advance(51 * time.Second)
	if got := use(); !got.Equal(first.Add(61 * time.Second)) {
		t.Fatalf("after 61 s last_used_at = %v, want %v", got, first.Add(61*time.Second))
	}
}

func TestABearerWithoutThePrefixIsRefused(t *testing.T) {
	e, _, owner := setup(t)
	ctx := context.Background()
	k, _ := e.svc.CreateAPIKey(ctx, principalOf(owner), "k")
	_, err := e.svc.Authenticate(ctx, "", k.Token[3:])
	requireCode(t, err, 401, "unauthenticated")
	s := login(t, e, "ana@x.example")
	_, err = e.svc.Authenticate(ctx, "", s.Token)
	requireCode(t, err, 401, "unauthenticated")
}

func TestABadBearerDoesNotFallBackToTheSession(t *testing.T) {
	e, _, _ := setup(t)
	s := login(t, e, "ana@x.example")
	_, err := e.svc.Authenticate(context.Background(), s.Token, "cx_wrong")
	requireCode(t, err, 401, "unauthenticated")
}

func TestListAPIKeys(t *testing.T) {
	e, org, owner := setup(t)
	ctx := context.Background()
	p := principalOf(owner)
	k1, _ := e.svc.CreateAPIKey(ctx, p, "one")
	e.clk.Advance(time.Second)
	if _, err := e.pool.Exec(ctx, `UPDATE api_keys SET created_at = created_at - interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	k2, _ := e.svc.CreateAPIKey(ctx, p, "two")
	cid := newClient(t, e.pool, org, "Gym")
	other := newUser(t, e.pool, org, &cid, "gym@x.example")
	if _, err := e.svc.CreateAPIKey(ctx, principalOf(other), "theirs"); err != nil {
		t.Fatal(err)
	}
	list, err := e.svc.ListAPIKeys(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != k2.ID || list[1].ID != k1.ID {
		t.Fatalf("list = %+v", list)
	}
	for _, it := range list {
		if it.Name == k1.Token || it.Prefix == k1.Token || it.Name == k2.Token || it.Prefix == k2.Token {
			t.Fatal("a field holds a plain token")
		}
	}
}

func TestRevokeAPIKey(t *testing.T) {
	e, _, owner := setup(t)
	ctx := context.Background()
	p := principalOf(owner)
	k, _ := e.svc.CreateAPIKey(ctx, p, "k")
	if err := e.svc.RevokeAPIKey(ctx, p, k.ID); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Authenticate(ctx, "", k.Token)
	requireCode(t, err, 401, "unauthenticated")
	list, _ := e.svc.ListAPIKeys(ctx, p)
	if len(list) != 0 {
		t.Fatalf("list = %d", len(list))
	}
	requireCode(t, e.svc.RevokeAPIKey(ctx, p, k.ID), 404, "not_found")
}

func TestRevokeAnotherUsersKey(t *testing.T) {
	e, org, owner := setup(t)
	ctx := context.Background()
	k, _ := e.svc.CreateAPIKey(ctx, principalOf(owner), "k")
	cid := newClient(t, e.pool, org, "Gym")
	other := newUser(t, e.pool, org, &cid, "gym@x.example")
	requireCode(t, e.svc.RevokeAPIKey(ctx, principalOf(other), k.ID), 404, "not_found")
	if _, err := e.svc.Authenticate(ctx, "", k.Token); err != nil {
		t.Fatal(err)
	}
}

func TestAPIKeyNameRules(t *testing.T) {
	e, _, owner := setup(t)
	ctx := context.Background()
	p := principalOf(owner)
	for _, name := range []string{"", "   ", strings.Repeat("é", 101), "a\nb", "a\u2029b", "a\x00b"} {
		_, err := e.svc.CreateAPIKey(ctx, p, name)
		requireCode(t, err, 400, "invalid_request")
	}
	if _, err := e.svc.CreateAPIKey(ctx, p, strings.Repeat("é", auth.MaxAPIKeyNameLength)); err != nil {
		t.Fatal(err)
	}
}
