package instances

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/core"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/tenancy"
)

// FirstUpdateWait is how long Create and Pair wait for the first update of the pairing session.
const FirstUpdateWait = 15 * time.Second

// CreateInput is the body of a create request. ClientID is a UUID as text; "" means the user's
// client (client user) or the default client (owner).
type CreateInput struct {
	ClientID    string
	DisplayName string
	Provider    string
	SendOnly    bool
	// WhatsApp Web only. DeviceIdentity is a name of core.DeviceIdentities(); nil lets the core
	// pick one. Locale ("es-ES") is required.
	DeviceIdentity *string
	Locale         *string
	Pairing        string
	Phone          string
	Meta           *core.MetaCredentials
	Webhooks       []WebhookInput

	clientID uuid.UUID // ClientID after validateCreate parsed it
}

// Create reserves a row in state "creating", starts a pairing session and answers with the first
// update of the session. The session goes on without the caller: a browser that leaves does not
// stop the pairing.
func (s *Service) Create(ctx context.Context, p tenancy.Principal, in CreateInput) (View, PairingUpdate, error) {
	if err := tenancy.Allow(p, tenancy.CreateInstance); err != nil {
		return View{}, PairingUpdate{}, err
	}
	in, mode, err := s.validateCreate(in)
	if err != nil {
		return View{}, PairingUpdate{}, err
	}
	q := gen.New(s.pool)
	sc := tenancy.Scope(p)
	clientID := in.clientID
	if clientID == uuid.Nil {
		if p.ClientID != nil {
			clientID = *p.ClientID
		} else if clientID, err = q.GetDefaultClientID(ctx, p.OrganizationID); err != nil {
			return View{}, PairingUpdate{}, dbError(err)
		}
	}
	if _, err := q.GetClientForNewInstance(ctx, gen.GetClientForNewInstanceParams{
		ID: clientID, OrganizationID: sc.OrganizationID, ClientID: sc.ClientID,
	}); err != nil {
		return View{}, PairingUpdate{}, dbError(err)
	}
	ni := tenancy.NewInstance{
		ClientID: clientID, CoreID: s.coreID, DisplayName: in.DisplayName,
		Provider: in.Provider, SendOnly: in.SendOnly, DeviceIdentity: in.DeviceIdentity, Locale: in.Locale,
	}
	if in.Meta != nil {
		ni.PhoneNumberID = &in.Meta.PhoneNumberID
	}
	var row gen.Instance
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		r, err := tenancy.ReserveInstance(ctx, tx, p, ni)
		if err != nil {
			return err
		}
		row = r
		txq := gen.New(tx)
		for _, h := range in.Webhooks {
			var sealed []byte
			if h.Secret != nil && *h.Secret != "" {
				if sealed, err = EncryptSecret(s.key, *h.Secret); err != nil {
					return apierr.Internal(err)
				}
			}
			if err := txq.InsertWebhook(ctx, gen.InsertWebhookParams{
				ID: uuid.New(), InstanceID: row.ID, Url: h.URL, Event: h.Event, SecretCiphertext: sealed,
			}); err != nil {
				return apierr.Internal(err)
			}
		}
		return nil
	})
	if err != nil {
		return View{}, PairingUpdate{}, dbError(err)
	}
	s.log.Info("Instance created.", "instance_id", row.ID, "provider", row.Provider)
	s.published(row)

	cfg := core.InstanceConfig{CoreName: row.CoreName, Provider: in.Provider, SendOnly: &in.SendOnly, Pairing: mode, Meta: in.Meta}
	if in.Provider == "WHATSAPP_WEB" {
		cfg.Phone = in.Phone
	}
	cfg.DeviceIdentity, cfg.Locale = deref(in.DeviceIdentity), deref(in.Locale)
	for _, h := range in.Webhooks {
		w := core.Webhook{URL: h.URL, Event: h.Event}
		if h.Secret != nil {
			w.Secret = *h.Secret
		}
		cfg.Webhooks = append(cfg.Webhooks, w)
	}
	var endErr atomic.Pointer[apierr.Error]
	run := func(ctx context.Context, onUpdate func(core.Update)) error {
		return s.core.CreateInstance(ctx, cfg, s.storeOnConnected(row.ID, onUpdate))
	}
	onEnd := func(res PairingResult) {
		endErr.Store(res.Err)
		s.finishCreate(row, res)
	}
	ch, err := s.pairings.Start(ctx, row.ID, row.OrganizationID, row.ClientID, mode, run, onEnd)
	if err != nil {
		s.finishCreate(row, PairingResult{FailCode: apierr.CodePairingFailed})
		return View{}, PairingUpdate{}, err
	}
	update, err := s.awaitFirst(ctx, ch, &endErr)
	if err != nil {
		return View{}, PairingUpdate{}, err
	}
	fresh, err := gen.New(s.pool).GetInstanceByID(ctx, row.ID)
	if err != nil {
		return View{}, PairingUpdate{}, dbError(err)
	}
	v, err := s.view(ctx, fresh)
	return v, update, err
}

// finishCreate runs when a creation session ended: it activates the row, or deletes it (the
// core deletes a brand-new instance whose pairing did not finish). It never uses a request context.
func (s *Service) finishCreate(row gen.Instance, res PairingResult) {
	id := row.ID
	ctx, cancel := context.WithTimeout(context.Background(), dbWriteTimeout)
	defer cancel()
	defer s.published(row)
	q := gen.New(s.pool)
	if res.Connected {
		if _, err := q.ActivatePairedInstance(ctx, gen.ActivatePairedInstanceParams{Now: s.clk.Now(), ID: id}); err != nil {
			s.log.Error("Cannot activate a paired instance.", "instance_id", id, "error", err)
		}
		return
	}
	if _, err := q.DeleteCreatingInstance(ctx, id); err != nil {
		s.log.Error("Cannot delete an instance whose pairing failed.", "instance_id", id, "error", err)
	}
}

// awaitFirst waits for the first event of a session (at most FirstUpdateWait) and turns it into
// the answer of Create or Pair. endErr holds the core's error of a failed session; onEnd stores it
// before the final event, so reading it after a "failed" event is safe.
func (s *Service) awaitFirst(ctx context.Context, ch <-chan PairingEvent, endErr *atomic.Pointer[apierr.Error]) (PairingUpdate, error) {
	timer := s.clk.NewTimer(FirstUpdateWait)
	defer timer.Stop()
	waiting := PairingUpdate{Status: core.StatusConnecting, Message: "Waiting for the core."}
	var latest *PairingUpdate
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				if ctx.Err() != nil {
					return PairingUpdate{}, apierr.Internal(ctx.Err())
				}
				if latest != nil {
					return *latest, nil
				}
				return waiting, nil
			}
			switch ev.Kind {
			case "failed":
				if ae := endErr.Load(); ae != nil {
					return PairingUpdate{}, ae
				}
				return PairingUpdate{}, apierr.Conflict(apierr.CodeConflict, ev.Detail)
			case "done":
				return PairingUpdate{Status: core.StatusConnected}, nil
			default:
				if ev.Update.Status != core.StatusConnected {
					return ev.Update, nil
				}
				// CONNECTED (Meta): the final event follows at once; wait for it in the same time.
				u := ev.Update
				latest = &u
			}
		case <-timer.C():
			if latest != nil {
				return *latest, nil
			}
			return waiting, nil
		case <-ctx.Done():
			return PairingUpdate{}, apierr.Internal(ctx.Err())
		}
	}
}

// storeOnConnected wraps the onUpdate of a pairing session. On the CONNECTED update it saves the
// device identity that the core used and the locale, before the update goes on, so a client that
// reads the instance at that event sees them. A Meta Cloud instance has neither: both stay NULL.
// When the write fails, the watcher stores the values of the core at its next round.
func (s *Service) storeOnConnected(id uuid.UUID, onUpdate func(core.Update)) func(core.Update) {
	return func(u core.Update) {
		if u.Status == core.StatusConnected {
			ctx, cancel := context.WithTimeout(context.Background(), dbWriteTimeout)
			err := gen.New(s.pool).SetInstanceIdentity(ctx, gen.SetInstanceIdentityParams{
				ID: id, DeviceIdentity: nilIfEmpty(u.DeviceIdentity), Locale: nilIfEmpty(u.Locale),
			})
			cancel()
			if err != nil {
				s.log.Error("Cannot save the device identity and the locale.", "instance_id", id, "error", err)
			}
		}
		onUpdate(u)
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
