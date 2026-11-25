package auth

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/tenancy"
)

const MaxAPIKeyNameLength = 100 // runes, after strings.TrimSpace

// APIKey is a new key. Token holds the plain key and is set only on creation.
type APIKey struct {
	gen.ApiKey
	Token string
}

func (s *Service) CreateAPIKey(ctx context.Context, p tenancy.Principal, name string) (APIKey, error) {
	if err := tenancy.Allow(p, tenancy.ManageOwnAccount); err != nil {
		return APIKey{}, err
	}
	var f apierr.Fields
	name = strings.TrimSpace(name)
	f.NoControl("name", name)
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxAPIKeyNameLength {
		f.Add("name", fmt.Sprintf("must have 1 to %d characters.", MaxAPIKeyNameLength))
	}
	if err := f.Err(); err != nil {
		return APIKey{}, err
	}
	plain, hash := NewToken(APIKeyPrefix)
	row, err := gen.New(s.pool).CreateAPIKey(ctx, gen.CreateAPIKeyParams{
		ID:        uuid.New(),
		UserID:    p.UserID,
		Name:      name,
		Prefix:    plain[len(APIKeyPrefix) : len(APIKeyPrefix)+8],
		TokenHash: hash,
	})
	if err != nil {
		return APIKey{}, toAPIError(err)
	}
	return APIKey{ApiKey: row, Token: plain}, nil
}

// ListAPIKeys returns the active keys of the user, newest first.
func (s *Service) ListAPIKeys(ctx context.Context, p tenancy.Principal) ([]gen.ApiKey, error) {
	if err := tenancy.Allow(p, tenancy.ManageOwnAccount); err != nil {
		return nil, err
	}
	keys, err := gen.New(s.pool).ListActiveAPIKeysOfUser(ctx, p.UserID)
	if err != nil {
		return nil, toAPIError(err)
	}
	return keys, nil
}

// RevokeAPIKey revokes one key of the user. An unknown, foreign or revoked key is 404.
func (s *Service) RevokeAPIKey(ctx context.Context, p tenancy.Principal, id uuid.UUID) error {
	if err := tenancy.Allow(p, tenancy.ManageOwnAccount); err != nil {
		return err
	}
	n, err := gen.New(s.pool).RevokeAPIKey(ctx, gen.RevokeAPIKeyParams{RevokedAt: s.clk.Now(), ID: id, UserID: p.UserID})
	if err != nil {
		return toAPIError(err)
	}
	if n == 0 {
		return apierr.NotFound()
	}
	return nil
}
