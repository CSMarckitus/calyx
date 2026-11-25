package instances

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/clock"
	"calyx/backend/internal/core"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/live"
	"calyx/backend/internal/tenancy"
)

// dbWriteTimeout limits the database writes that run after a request ended (pairing sessions).
const dbWriteTimeout = 10 * time.Second

// Service is the only code that changes instance rows outside the watcher. Every public method
// takes a principal and loads the row with the tenant scope first.
type Service struct {
	pool     *pgxpool.Pool
	core     *core.Client
	pairings *Pairings
	clk      clock.Clock
	key      []byte
	coreID   string
	// pairingTimeout is the life of a pairing session. Recover uses it.
	pairingTimeout time.Duration
	log            *slog.Logger
	// live tells the browsers that a row changed. A nil Live does nothing.
	live *live.Live

	// busy holds the ids of the instances with a change in progress (see reserve).
	busy sync.Map

	hookMu sync.Mutex
	hook   func(ctx context.Context, instanceID uuid.UUID)
}

func NewService(pool *pgxpool.Pool, c *core.Client, clk clock.Clock, secretKey []byte, coreID string, pairingTimeout time.Duration) *Service {
	return &Service{
		pool: pool, core: c, clk: clk, key: secretKey, coreID: coreID, pairingTimeout: pairingTimeout,
		pairings: NewPairings(clk, pairingTimeout),
		log:      slog.Default(),
	}
}

// SetLive sets the hub that gets a notification after each write of a row.
func (s *Service) SetLive(l *live.Live) { s.live = l }

// published tells the browsers of the tenant of the row that it changed. Call it after the write.
func (s *Service) published(row gen.Instance) { s.live.Publish(row.OrganizationID, row.ClientID) }

// SetStatusCheck sets the hook that runs after a status-related failure (the watcher's check).
func (s *Service) SetStatusCheck(fn func(ctx context.Context, instanceID uuid.UUID)) {
	s.hookMu.Lock()
	defer s.hookMu.Unlock()
	s.hook = fn
}

// TriggerStatusCheck runs the hook in a new goroutine, so the HTTP answer does not wait for it
// and a closed request does not cancel it. Without a hook it does nothing.
func (s *Service) TriggerStatusCheck(ctx context.Context, instanceID uuid.UUID) {
	s.hookMu.Lock()
	fn := s.hook
	s.hookMu.Unlock()
	if fn == nil {
		return
	}
	bg := context.WithoutCancel(ctx)
	go fn(bg, instanceID)
}

// CancelAllPairings ends every pairing session (shutdown).
func (s *Service) CancelAllPairings() { s.pairings.CancelAll() }

// loadReserved loads the row with the tenant scope, reserves its id, and reads the row again.
// Update and Pair send the full configuration to the core, so two of them at the same time can
// mix: the second one would send an old value. The second one gets 409 instead. The second read
// shows what a change that just ended saved. The caller must call release.
func (s *Service) loadReserved(ctx context.Context, p tenancy.Principal, id uuid.UUID) (gen.Instance, func(), error) {
	first, err := s.load(ctx, p, id)
	if err != nil {
		return gen.Instance{}, nil, err
	}
	// The id comes from the first row: a failed read returns an empty row, and release must still
	// free the real id.
	key := first.ID
	if _, taken := s.busy.LoadOrStore(key, struct{}{}); taken {
		return gen.Instance{}, nil, apierr.Conflict(apierr.CodeConflict, "Another change of this instance is running. Try again.")
	}
	var once sync.Once
	release := func() { once.Do(func() { s.busy.Delete(key) }) }
	row, err := s.load(ctx, p, id)
	if err != nil {
		release()
		return gen.Instance{}, nil, err
	}
	return row, release, nil
}

// dbError turns a database error into an API error.
func dbError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return apierr.NotFound()
	}
	if ae, ok := apierr.As(err); ok {
		return ae
	}
	return apierr.Internal(err)
}

// load reads one row with the tenant scope. A row of another tenant is 404.
func (s *Service) load(ctx context.Context, p tenancy.Principal, id uuid.UUID) (gen.Instance, error) {
	if err := tenancy.Allow(p, tenancy.UseInstances); err != nil {
		return gen.Instance{}, err
	}
	sc := tenancy.Scope(p)
	row, err := gen.New(s.pool).GetInstanceInScope(ctx, gen.GetInstanceInScopeParams{
		ID: id, OrganizationID: sc.OrganizationID, ClientID: sc.ClientID,
	})
	if err != nil {
		return gen.Instance{}, dbError(err)
	}
	return row, nil
}

// view builds the View of a row that load returned.
func (s *Service) view(ctx context.Context, row gen.Instance) (View, error) {
	hooks, err := gen.New(s.pool).ListWebhooksOfInstance(ctx, row.ID)
	if err != nil {
		return View{}, dbError(err)
	}
	v := View{Instance: row, Webhooks: make([]WebhookView, 0, len(hooks)), StatusStale: s.live.Stale()}
	for _, h := range hooks {
		v.Webhooks = append(v.Webhooks, WebhookView{URL: h.Url, Event: h.Event, HasSecret: h.SecretCiphertext != nil})
	}
	return v, nil
}

// Get returns one instance of the principal's scope.
func (s *Service) Get(ctx context.Context, p tenancy.Principal, id uuid.UUID) (View, error) {
	if err := tenancy.Allow(p, tenancy.UseInstances); err != nil {
		return View{}, err
	}
	row, err := s.load(ctx, p, id)
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, row)
}

// List returns every instance of the principal's scope, in every state.
func (s *Service) List(ctx context.Context, p tenancy.Principal) ([]View, error) {
	if err := tenancy.Allow(p, tenancy.UseInstances); err != nil {
		return nil, err
	}
	sc := tenancy.Scope(p)
	q := gen.New(s.pool)
	rows, err := q.ListInstancesInScope(ctx, gen.ListInstancesInScopeParams{OrganizationID: sc.OrganizationID, ClientID: sc.ClientID})
	if err != nil {
		return nil, dbError(err)
	}
	hooks, err := q.ListWebhooksInScope(ctx, gen.ListWebhooksInScopeParams{OrganizationID: sc.OrganizationID, ClientID: sc.ClientID})
	if err != nil {
		return nil, dbError(err)
	}
	stale := s.live.Stale()
	views := make([]View, 0, len(rows))
	index := map[uuid.UUID]int{}
	for _, r := range rows {
		index[r.ID] = len(views)
		views = append(views, View{Instance: r, Webhooks: []WebhookView{}, StatusStale: stale})
	}
	for _, h := range hooks {
		if i, ok := index[h.InstanceID]; ok {
			views[i].Webhooks = append(views[i].Webhooks, WebhookView{URL: h.Url, Event: h.Event, HasSecret: h.HasSecret})
		}
	}
	return views, nil
}
