package instances

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/google/uuid"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/core"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/tenancy"
)

// PairInput is the body of a pair request. DeviceIdentity and Locale are sent only when the user
// gives them: without them the core picks a new identity and keeps its stored locale.
type PairInput struct {
	Pairing, Phone         string
	DeviceIdentity, Locale *string
}

// Pair starts a new pairing session for an instance that needs it. It sends the full stored
// configuration to the core (the webhooks with their secrets, and the send-only flag), so the
// core matches the backend after the re-pair. It answers with the first update of the session.
func (s *Service) Pair(ctx context.Context, p tenancy.Principal, id uuid.UUID, in PairInput) (PairingUpdate, error) {
	if err := tenancy.Allow(p, tenancy.UseInstances); err != nil {
		return PairingUpdate{}, err
	}
	var f apierr.Fields
	mode, phone := validPairing(&f, in.Pairing, in.Phone)
	checkIdentityAndLocale(&f, in.DeviceIdentity, in.Locale)
	if err := f.Err(); err != nil {
		return PairingUpdate{}, err
	}
	row, release, err := s.loadReserved(ctx, p, id)
	if err != nil {
		return PairingUpdate{}, err
	}
	defer release()
	switch {
	case row.State == "creating":
		return PairingUpdate{}, apierr.Conflict(apierr.CodePairingInProgress, "The instance is being created. Follow or cancel its pairing.")
	case row.State == "removing":
		return PairingUpdate{}, apierr.Conflict(apierr.CodeConflict, "The instance is being removed.")
	case row.State == "missing":
		return PairingUpdate{}, apierr.Conflict(apierr.CodeConflict, "The core no longer has this instance. Remove it and create it again.")
	case row.Provider == "META_CLOUD":
		return PairingUpdate{}, apierr.Conflict(apierr.CodePairingNotSupported,
			"Meta Cloud instances recover with new credentials: use PATCH with meta_credentials.")
	case !row.Enabled:
		// The core refuses it too. The check here saves the request and keeps the 409 stable.
		return PairingUpdate{}, apierr.Conflict(apierr.CodeConflict, "The instance is disabled. Enable it before you pair it again.")
	}
	if row.Locale == nil && in.Locale == nil {
		// An instance created before the backend stored a locale: the core has none either.
		var lf apierr.Fields
		lf.Add("locale", "is required: this instance has no stored locale. Give one, for example es-ES.")
		return PairingUpdate{}, lf.Err()
	}
	org, err := gen.New(s.pool).GetOrganizationWhatsAppWeb(ctx, p.OrganizationID)
	if err != nil {
		return PairingUpdate{}, dbError(err)
	}
	if !org.WhatsappWebAllowed {
		return PairingUpdate{}, apierr.Forbidden(apierr.CodeWhatsAppWebNotAllowed, "WhatsApp Web is not allowed for this organization.")
	}
	if s.pairings.Running(id) {
		return PairingUpdate{}, apierr.Conflict(apierr.CodePairingInProgress, "A pairing is already running for this instance.")
	}
	last := "unknown"
	if row.LastStatus != nil {
		last = *row.LastStatus
	}
	switch last {
	case core.StatusError:
		return PairingUpdate{}, apierr.Conflict(apierr.CodeInstanceInError, "The instance is in an error state. Remove it and create it again.")
	case core.StatusNeedsAuth:
	default:
		return PairingUpdate{}, apierr.Conflict(apierr.CodeInstanceConnected,
			fmt.Sprintf("The instance does not need pairing; its status is %s.", last))
	}

	hooks, err := gen.New(s.pool).ListWebhooksOfInstance(ctx, row.ID)
	if err != nil {
		return PairingUpdate{}, dbError(err)
	}
	cfg := core.InstanceConfig{
		CoreName: row.CoreName, Provider: "WHATSAPP_WEB", SendOnly: &row.SendOnly, Pairing: mode, Phone: phone,
		DeviceIdentity: deref(in.DeviceIdentity), Locale: deref(in.Locale),
	}
	for _, h := range hooks {
		w := core.Webhook{URL: h.Url, Event: h.Event}
		if h.SecretCiphertext != nil {
			if w.Secret, err = DecryptSecret(s.key, h.SecretCiphertext); err != nil {
				s.log.Error("Cannot decrypt a webhook secret.", "instance_id", row.ID, "event", h.Event)
				return PairingUpdate{}, apierr.Internal(err)
			}
		}
		cfg.Webhooks = append(cfg.Webhooks, w)
	}

	var endErr atomic.Pointer[apierr.Error]
	run := func(ctx context.Context, onUpdate func(core.Update)) error {
		// The session is in the map now. Remove sets "removing" and then looks at the map, so
		// either Remove sees this session, or this read sees "removing". The race is closed.
		cur, err := gen.New(s.pool).GetInstanceByID(ctx, row.ID)
		if err != nil {
			return dbError(err) // 404 when a Remove already deleted the row
		}
		if cur.State != "active" {
			return apierr.Conflict(apierr.CodeConflict, "The instance is being removed.")
		}
		if len(cfg.Webhooks) == 0 {
			// A re-pair without webhooks keeps the webhooks of the core. The backend has none, so
			// a webhook added in the core directly would survive, or refuse a send-only re-pair.
			// An update with only an empty list removes them and does not rebuild the instance.
			if err := s.core.UpdateInstanceConfig(ctx, core.InstanceConfig{CoreName: row.CoreName, Provider: "WHATSAPP_WEB"}); err != nil {
				return err
			}
		}
		return s.core.CreateInstance(ctx, cfg, s.storeOnConnected(row.ID, onUpdate))
	}
	onEnd := func(res PairingResult) {
		endErr.Store(res.Err)
		defer s.published(row)
		if !res.Connected {
			return // the row stays active with its status and its webhooks
		}
		wctx, cancel := context.WithTimeout(context.Background(), dbWriteTimeout)
		defer cancel()
		err := gen.New(s.pool).SetRepairedInstanceConnected(wctx, gen.SetRepairedInstanceConnectedParams{Now: s.clk.Now(), ID: row.ID})
		if err != nil {
			s.log.Error("Cannot save the paired status.", "instance_id", row.ID, "error", err)
		}
		// An event of the core can arrive before this write, so ask for a new check.
		s.TriggerStatusCheck(context.Background(), row.ID)
	}
	ch, err := s.pairings.Start(ctx, row.ID, row.OrganizationID, row.ClientID, mode, run, onEnd)
	release() // the session is registered: Running refuses a second change from now on
	if err != nil {
		return PairingUpdate{}, err
	}
	return s.awaitFirst(ctx, ch, &endErr)
}

// FollowPairing answers in this order: a running session gives its stream; a row in CONNECTED
// gives 404; a kept failure gives the event failed, and the channel closes; all else gives 404.
// The channel of a running session closes when the session ends or ctx ends.
func (s *Service) FollowPairing(ctx context.Context, p tenancy.Principal, id uuid.UUID) (<-chan PairingEvent, error) {
	if err := tenancy.Allow(p, tenancy.UseInstances); err != nil {
		return nil, err
	}
	row, err := s.load(ctx, p, id)
	rowFound := err == nil
	if ae, ok := apierr.As(err); err != nil && (!ok || ae.Status != 404) {
		return nil, err
	}
	if !rowFound {
		// A new instance whose pairing failed has no row. The session holds the scope of the row.
		org, client, ok := s.pairings.Owner(id)
		if !ok || !inScope(p, org, client) {
			return nil, apierr.NotFound()
		}
	}
	ch, ended, ok := s.pairings.Follow(ctx, id)
	if !ok {
		return nil, apierr.NotFound()
	}
	if ended && rowFound && row.LastStatus != nil && *row.LastStatus == core.StatusConnected {
		return nil, apierr.NotFound() // the instance shows its success itself
	}
	return ch, nil
}

// inScope applies the rule of ScopeFilter.Covers to the organization and the client of a row.
func inScope(p tenancy.Principal, organizationID, clientID uuid.UUID) bool {
	return tenancy.Scope(p).Covers(organizationID, clientID)
}

// CancelPairing ends the running pairing session of an instance and returns after it ended, so a
// row in state "creating" is already deleted. Without a session the answer is 404.
func (s *Service) CancelPairing(ctx context.Context, p tenancy.Principal, id uuid.UUID) error {
	if err := tenancy.Allow(p, tenancy.UseInstances); err != nil {
		return err
	}
	if _, err := s.load(ctx, p, id); err != nil {
		return err
	}
	if !s.pairings.Cancel(id) {
		return apierr.NotFound()
	}
	return nil
}
