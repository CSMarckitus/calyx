package instances

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/core"
	"calyx/backend/internal/database"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/tenancy"
)

// Change is a PATCH. A nil field is not changed. Webhooks is the full new list when it is not nil.
type Change struct {
	DisplayName       *string
	Enabled, SendOnly *bool
	Webhooks          *[]WebhookInput
	Meta              *core.MetaCredentials
}

// resolvedWebhook is a webhook of the new list with its secret in plain text (for the core) and
// sealed (for the database).
type resolvedWebhook struct {
	url, event string
	plain      string
	sealed     []byte
}

// Update changes an instance. The core goes first: when it does not confirm, nothing is saved.
// The display name never reaches the core.
func (s *Service) Update(ctx context.Context, p tenancy.Principal, id uuid.UUID, ch Change) (View, error) {
	if err := tenancy.Allow(p, tenancy.UseInstances); err != nil {
		return View{}, err
	}
	// The checks that do not need the row come first: a bad body is 400 for any id.
	var f apierr.Fields
	if ch.DisplayName != nil {
		name := trimmedName(&f, "display_name", *ch.DisplayName, MaxDisplayNameLength)
		ch.DisplayName = &name
	}
	if ch.Meta != nil {
		ch.Meta = validMeta(&f, ch.Meta)
	}
	if ch.Webhooks != nil {
		checkWebhookFields(&f, *ch.Webhooks, true)
	}
	if err := f.Err(); err != nil {
		return View{}, err
	}
	row, release, err := s.loadReserved(ctx, p, id)
	if err != nil {
		return View{}, err
	}
	defer release()
	q := gen.New(s.pool)
	stored, err := q.ListWebhooksOfInstance(ctx, row.ID)
	if err != nil {
		return View{}, dbError(err)
	}
	// The provider rules need the row.
	if ch.Meta != nil && row.Provider != "META_CLOUD" {
		f.Add("meta_credentials", "is only for META_CLOUD.")
	}
	if ch.SendOnly != nil && *ch.SendOnly && row.Provider == "META_CLOUD" {
		f.Add("send_only", "is not supported for META_CLOUD.")
	}

	coreChange := ch.Enabled != nil || ch.SendOnly != nil || ch.Webhooks != nil || ch.Meta != nil
	// Validate the webhooks that the instance will have after the change.
	sendOnly := row.SendOnly
	if ch.SendOnly != nil {
		sendOnly = *ch.SendOnly
	}
	var resulting []WebhookInput
	if ch.Webhooks != nil {
		resulting = *ch.Webhooks
	} else {
		for _, h := range stored {
			resulting = append(resulting, WebhookInput{URL: h.Url, Event: h.Event})
		}
	}
	if coreChange {
		checkWebhookEvents(&f, row.Provider, sendOnly, resulting)
	}
	if err := f.Err(); err != nil {
		return View{}, err
	}

	switch {
	case row.State == "creating":
		return View{}, apierr.Conflict(apierr.CodePairingInProgress, "The instance is being created. Follow or cancel its pairing.")
	case row.State == "removing":
		return View{}, apierr.Conflict(apierr.CodeConflict, "The instance is being removed.")
	case !coreChange:
	case row.State == "missing":
		return View{}, apierr.Conflict(apierr.CodeConflict, "The core no longer has this instance. Remove it and create it again.")
	case s.pairings.Running(row.ID):
		return View{}, apierr.Conflict(apierr.CodePairingInProgress, "A pairing is running for this instance. Wait for it or cancel it.")
	}
	if ch.Meta != nil {
		used, err := q.PhoneNumberIDUsedByOther(ctx, gen.PhoneNumberIDUsedByOtherParams{PhoneNumberID: ch.Meta.PhoneNumberID, ExceptID: row.ID})
		if err != nil {
			return View{}, dbError(err)
		}
		if used {
			return View{}, errPhoneNumberInUse()
		}
	}

	var list []resolvedWebhook
	if coreChange {
		if list, err = s.resolveWebhooks(row.ID, resulting, ch.Webhooks != nil, stored); err != nil {
			return View{}, err
		}
		cfg := core.InstanceConfig{CoreName: row.CoreName, Provider: row.Provider, SendOnly: ch.SendOnly, Enabled: ch.Enabled, Meta: ch.Meta}
		for _, w := range list {
			cfg.Webhooks = append(cfg.Webhooks, core.Webhook{URL: w.url, Event: w.event, Secret: w.plain})
		}
		if err := s.core.UpdateInstanceConfig(ctx, cfg); err != nil {
			return View{}, err
		}
	}

	// The core has answered: the save must not stop because the request ended.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dbWriteTimeout)
	defer cancel()
	err = pgx.BeginFunc(wctx, s.pool, func(tx pgx.Tx) error {
		txq := gen.New(tx)
		params := gen.UpdateInstanceSettingsParams{ID: row.ID, DisplayName: ch.DisplayName, Enabled: ch.Enabled, SendOnly: ch.SendOnly}
		if ch.Meta != nil {
			params.PhoneNumberID = &ch.Meta.PhoneNumberID
		}
		if _, err := txq.UpdateInstanceSettings(wctx, params); err != nil {
			return err
		}
		if ch.Webhooks == nil {
			return nil
		}
		if err := txq.DeleteWebhooksOfInstance(wctx, row.ID); err != nil {
			return err
		}
		for _, w := range list {
			if err := txq.InsertWebhook(wctx, gen.InsertWebhookParams{
				ID: uuid.New(), InstanceID: row.ID, Url: w.url, Event: w.event, SecretCiphertext: w.sealed,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if coreChange {
			s.log.Error("The core accepted a change but the database did not save it.", "instance_id", row.ID)
		}
		if constraint, ok := database.UniqueViolation(err); ok && constraint == "instances_phone_number_id_key" {
			return View{}, errPhoneNumberInUse()
		}
		return View{}, apierr.Internal(err)
	}
	s.published(row)
	if coreChange {
		// The core sends no event when it closes an instance.
		s.TriggerStatusCheck(ctx, row.ID)
	}
	fresh, err := q.GetInstanceByID(wctx, row.ID)
	if err != nil {
		return View{}, dbError(err)
	}
	return s.view(wctx, fresh)
}

func errPhoneNumberInUse() error {
	return apierr.Conflict(apierr.CodePhoneNumberIDInUse, "This phone number id is already used by another instance.")
}

// resolveWebhooks applies the secret rules to the new list. When the request has no list, the
// stored webhooks keep their secrets. A nil secret keeps the stored secret of the webhook with
// the same event and URL; "" removes it; any other value is sealed again.
func (s *Service) resolveWebhooks(instanceID uuid.UUID, wanted []WebhookInput, listChanged bool, stored []gen.Webhook) ([]resolvedWebhook, error) {
	byEvent := map[string]gen.Webhook{}
	for _, h := range stored {
		byEvent[h.Event] = h
	}
	out := make([]resolvedWebhook, 0, len(wanted))
	for _, w := range wanted {
		r := resolvedWebhook{url: w.URL, event: w.Event}
		switch {
		case w.Secret == nil || !listChanged:
			if old, ok := byEvent[w.Event]; ok && old.Url == w.URL && old.SecretCiphertext != nil {
				plain, err := DecryptSecret(s.key, old.SecretCiphertext)
				if err != nil {
					s.log.Error("Cannot decrypt a webhook secret.", "instance_id", instanceID, "event", w.Event)
					return nil, apierr.Internal(err)
				}
				r.plain, r.sealed = plain, old.SecretCiphertext
			}
		case *w.Secret != "":
			sealed, err := EncryptSecret(s.key, *w.Secret)
			if err != nil {
				return nil, apierr.Internal(err)
			}
			r.plain, r.sealed = *w.Secret, sealed
		}
		out = append(out, r)
	}
	return out, nil
}
