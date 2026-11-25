package tenancy

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/clock"
)

type Service struct {
	pool *pgxpool.Pool
	clk  clock.Clock
}

func NewService(pool *pgxpool.Pool, clk clock.Clock) *Service {
	return &Service{pool: pool, clk: clk}
}

const (
	DefaultClientName = "Default"
	MaxNameLength     = 100   // runes, after strings.TrimSpace; organizations and clients
	MaxInstancesLimit = 10000 // highest accepted value of max_instances
)

// validName trims the name and adds the errors of its rules to f.
func validName(f *apierr.Fields, name string) string {
	name = strings.TrimSpace(name)
	f.NoControl("name", name)
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxNameLength {
		f.Add("name", fmt.Sprintf("must have 1 to %d characters.", MaxNameLength))
	}
	return name
}

func validMaxInstances(f *apierr.Fields, n int) {
	if n < 0 || n > MaxInstancesLimit {
		f.Add("max_instances", fmt.Sprintf("must be 0 to %d.", MaxInstancesLimit))
	}
}

// toAPIError maps pgx.ErrNoRows to 404 and any other error to 500. An *apierr.Error stays as it is.
func toAPIError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := apierr.As(err); ok {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return apierr.NotFound()
	}
	return apierr.Internal(err)
}
