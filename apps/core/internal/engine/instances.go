package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/events"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

type WebhookInput struct {
	URL, Event, Secret string
}

type CreateInstanceParams struct {
	Name     string
	Provider string // WHATSAPP_WEB or META_CLOUD
	SendOnly *bool  // nil: false for a new instance, the stored flag for a re-pair
	Webhooks []WebhookInput
	// WhatsApp Web only.
	Pairing PairingMethod
	// WhatsApp Web only. UNSPECIFIED: a random entry. "": the stored locale of a re-pair.
	DeviceIdentity corev1.DeviceIdentity
	Locale         string
	// Provider credentials as JSON (Meta Cloud).
	ProviderConfig string
}

type CreateInstanceUpdate struct {
	Status      corev1.InstanceStatus
	PairingCode string // a QR as text, or the pairing code
	Message     string
	// Only on the CONNECTED update of a WhatsApp Web instance.
	DeviceIdentity corev1.DeviceIdentity
	Locale         string
}

// UpdateInstanceParams: only the fields that are not nil are changed.
type UpdateInstanceParams struct {
	NewName        *string
	IsEnabled      *bool
	SendOnly       *bool
	Webhooks       *[]WebhookInput // replaces the whole list
	ProviderConfig *string         // Meta Cloud credentials as JSON
}

type WebhookSummary struct {
	URL, Event          string
	IsActive, HasSecret bool
}

type InstanceSummary struct {
	Name, Provider      string
	Status              corev1.InstanceStatus
	IsEnabled, SendOnly bool
	Webhooks            []WebhookSummary
	DeviceIdentity      corev1.DeviceIdentity // UNSPECIFIED when the instance has none
	Locale              string
}

// CreateInstance creates an instance, or pairs again one that is in NEEDS_AUTH (a re-pair). A
// re-pair keeps the stored values that the request does not carry. onUpdate receives every pairing
// code and, at the end, one CONNECTED update. Cancelling ctx stops the pairing.
func (o *Orchestrator) CreateInstance(ctx context.Context, params CreateInstanceParams, onUpdate func(CreateInstanceUpdate)) error {
	name := params.Name
	if strings.TrimSpace(name) == "" {
		return calyxerr.New(codes.InvalidArgument, "instance_name is required.")
	}

	o.mu.Lock()
	if o.pairing[name] {
		o.mu.Unlock()
		return calyxerr.New(codes.AlreadyExists, "Instance %s is being paired right now.", name)
	}
	o.pairing[name] = true
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		delete(o.pairing, name)
		o.mu.Unlock()
		o.invalidateWebhooks(name)
	}()

	row, found, err := o.writeConfiguration(ctx, params)
	if err != nil {
		return err
	}

	wired := &atomic.Bool{}
	provider, err := o.opts.BuildProvider(row, o.hooksFor(row, wired))
	if err == nil {
		err = o.authenticate(ctx, provider, params, onUpdate)
	}
	if err == nil {
		err = o.markConnected(row)
	}
	if err != nil {
		if provider != nil {
			_ = provider.Destroy(context.Background())
		}
		if !found {
			// Do not leave behind an instance nobody managed to pair.
			_, _ = o.db.ExecContext(context.Background(), "DELETE FROM instances WHERE id = ?", row.ID)
		}
		return err
	}

	if o.pairedHook != nil {
		o.pairedHook()
	}
	wired.Store(true)
	if err := o.register(row, provider); err != nil {
		return err
	}
	o.bus.Publish(name, "connection.update", events.ConnectionUpdate{Status: StatusName(corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED)})
	onUpdate(CreateInstanceUpdate{
		Status: corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED, Message: "Instance connected successfully.",
		DeviceIdentity: identityFromName(row.DeviceIdentity), Locale: row.Locale,
	})
	return nil
}

// writeConfiguration runs steps 2 to 5 of a pairing: it reads the stored instance, merges and
// checks the configuration, and writes it. configMu keeps an UpdateInstanceConfig out of the time
// between the read and the write, so the written configuration is the one that was checked.
func (o *Orchestrator) writeConfiguration(ctx context.Context, params CreateInstanceParams) (InstanceRow, bool, error) {
	o.configMu.Lock()
	defer o.configMu.Unlock()
	name := params.Name
	existing, found, err := lookupInstance(ctx, o.db, name)
	if err != nil {
		return InstanceRow{}, false, err
	}
	if found && existing.Status != corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH {
		return InstanceRow{}, false, calyxerr.New(codes.AlreadyExists, "Instance with name %s already exists.", name)
	}
	if found && existing.Provider != params.Provider {
		return InstanceRow{}, false, calyxerr.New(codes.InvalidArgument,
			"Instance %s exists with the provider %s. Remove it to use a different one.", name, existing.Provider)
	}
	if found && !existing.IsEnabled {
		return InstanceRow{}, false, calyxerr.New(codes.FailedPrecondition,
			"Instance %s is disabled. Enable it before you pair it again.", name)
	}

	identity, loc, source, err := o.resolveIdentityAndLocale(params, existing, found)
	if err != nil {
		return InstanceRow{}, false, err
	}
	params.DeviceIdentity, params.Locale = identity, loc

	var stored []WebhookInput
	if found && len(params.Webhooks) == 0 {
		if stored, err = storedWebhookInputs(ctx, o.db, existing.ID); err != nil {
			return InstanceRow{}, false, err
		}
	}
	sendOnly, webhooks, replace := mergeConfiguration(existing, found, params, stored)
	if err := validateWebhooks(webhooks, params.Provider, sendOnly); err != nil {
		return InstanceRow{}, false, err
	}
	if err := validateSendOnly(params.Provider, sendOnly); err != nil {
		return InstanceRow{}, false, err
	}
	if err := o.checkPhoneNumberIDIsFree(ctx, params.ProviderConfig, existing.ID); err != nil {
		return InstanceRow{}, false, err
	}

	// An instance that lost its session can still be loaded: close it before pairing again. The
	// pairing flag stops new loads, but a load that started before it registers first. A close
	// that another caller started is not in o.live, and its session stays open until it ends.
	o.waitForLoad(name)
	o.mu.Lock()
	previous := o.live[name]
	o.mu.Unlock()
	if previous != nil {
		o.closeLive(previous, false)
	}
	o.mu.Lock()
	closing := o.closing[name]
	o.mu.Unlock()
	if closing != nil {
		<-closing
	}

	row, err := o.saveNewInstance(ctx, params, existing, found, sendOnly, replace)
	if err == nil && row.DeviceIdentity != "" {
		slog.Info("Device identity chosen.", "instance", name, "device_identity", row.DeviceIdentity, "source", source)
	}
	return row, found, err
}

// markConnected writes the CONNECTED status at the end of a pairing. When the row is gone, the
// pairing must not register a live instance that nothing can remove.
func (o *Orchestrator) markConnected(row InstanceRow) error {
	result, err := o.db.ExecContext(context.Background(),
		"UPDATE instances SET connection_status = 'CONNECTED', updated_at = ? WHERE id = ?", time.Now().UnixMilli(), row.ID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return calyxerr.New(codes.NotFound, "Instance with name %s not found.", row.Name)
	}
	return nil
}

// authenticate pairs a device (WhatsApp Web) or validates credentials (Meta Cloud).
func (o *Orchestrator) authenticate(ctx context.Context, provider Provider, params CreateInstanceParams, onUpdate func(CreateInstanceUpdate)) error {
	pairer, ok := provider.(Pairer)
	if !ok {
		return provider.Connect(ctx)
	}
	message := "Please enter the pairing code to authenticate."
	if params.Pairing.QR {
		message = "Please scan the QR code to authenticate."
	}
	return pairer.Pair(ctx, params.Pairing, func(code string) {
		onUpdate(CreateInstanceUpdate{Status: corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH, PairingCode: code, Message: message})
	})
}

// mergeConfiguration applies the request to the stored configuration. A new instance takes the
// request as it is. A re-pair keeps each value that the request does not carry. stored holds the
// webhooks of a re-pair whose request has none. replace tells if params.Webhooks must be written.
func mergeConfiguration(existing InstanceRow, found bool, params CreateInstanceParams, stored []WebhookInput) (sendOnly bool, webhooks []WebhookInput, replace bool) {
	if found {
		sendOnly = existing.IsSendOnly
	}
	if params.SendOnly != nil {
		sendOnly = *params.SendOnly
	}
	// Proto3 cannot tell an empty list from an absent one, so an empty list keeps the stored webhooks.
	if !found || len(params.Webhooks) > 0 {
		return sendOnly, params.Webhooks, true
	}
	return sendOnly, stored, false
}

// saveNewInstance creates the row of a new instance, or refreshes the one that pairs again. It
// writes params.Webhooks only when replaceHooks is true. params holds the resolved device
// identity and locale.
func (o *Orchestrator) saveNewInstance(ctx context.Context, params CreateInstanceParams, existing InstanceRow, found, sendOnly, replaceHooks bool) (InstanceRow, error) {
	row := InstanceRow{
		ID: existing.ID, Name: params.Name, Provider: params.Provider, IsEnabled: true,
		IsSendOnly: sendOnly, Status: corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH,
		DeviceJID: existing.DeviceJID, ProviderConfig: params.ProviderConfig,
		DeviceIdentity: identityName(params.DeviceIdentity), Locale: params.Locale,
	}
	now := time.Now().UnixMilli()
	err := inTx(ctx, o.db, func(tx *sql.Tx) error {
		var err error
		if found {
			var result sql.Result
			result, err = tx.ExecContext(ctx, `UPDATE instances SET is_used_only_for_send_messages = ?,
				connection_status = 'NEEDS_AUTH', provider_config = ?, device_identity = ?, locale = ?,
				updated_at = ? WHERE id = ?`,
				row.IsSendOnly, nullable(row.ProviderConfig), nullable(row.DeviceIdentity), nullable(row.Locale), now, row.ID)
			if err != nil {
				return err
			}
			// A removal that the re-pair waited for deleted the row after the re-pair read it.
			var n int64
			n, err = result.RowsAffected()
			if err != nil {
				return err
			}
			if n == 0 {
				return calyxerr.New(codes.NotFound, "Instance with name %s not found.", params.Name)
			}
		} else {
			row.ID = newID()
			_, err = tx.ExecContext(ctx, `INSERT INTO instances (id, name, provider, is_used_only_for_send_messages,
				connection_status, provider_config, device_identity, locale, created_at, updated_at)
				VALUES (?, ?, ?, ?, 'NEEDS_AUTH', ?, ?, ?, ?, ?)`,
				row.ID, row.Name, row.Provider, row.IsSendOnly, nullable(row.ProviderConfig),
				nullable(row.DeviceIdentity), nullable(row.Locale), now, now)
		}
		if err != nil || !replaceHooks {
			return err
		}
		return replaceWebhooks(ctx, tx, row.ID, params.Webhooks)
	})
	if isUniqueViolation(err) {
		return row, calyxerr.New(codes.AlreadyExists, "Instance with name %s already exists.", params.Name)
	}
	return row, err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// writeChanges checks and writes the changes of UpdateInstanceConfig, and closes the session that
// they change. It returns the new name.
func (o *Orchestrator) writeChanges(ctx context.Context, name string, changes UpdateInstanceParams) (string, error) {
	o.configMu.Lock()
	defer o.configMu.Unlock()
	// A removal publishes its close under configMu, then deletes the row. After this wait the row is
	// gone, so a removal never deletes an instance that this change renamed.
	o.waitForClose(name)
	row, err := findInstance(ctx, o.db, name)
	if err != nil {
		return "", err
	}
	// CreateInstance sets the flag before it takes configMu, so either the pairing reads this
	// change or this change sees the flag.
	o.mu.Lock()
	pairing := o.pairing[name]
	o.mu.Unlock()
	if pairing {
		return "", calyxerr.New(codes.FailedPrecondition, "Instance %s is being paired. Cancel the pairing first.", name)
	}
	if changes.NewName != nil && strings.TrimSpace(*changes.NewName) == "" {
		return "", calyxerr.New(codes.InvalidArgument, "new_instance_name cannot be empty.")
	}
	if changes.ProviderConfig != nil && row.Provider != "META_CLOUD" {
		return "", calyxerr.New(codes.InvalidArgument, "Provider credentials only apply to Meta Cloud instances.")
	}
	if changes.ProviderConfig != nil {
		if err := o.checkPhoneNumberIDIsFree(ctx, *changes.ProviderConfig, row.ID); err != nil {
			return "", err
		}
	}

	sendOnly := row.IsSendOnly
	if changes.SendOnly != nil {
		sendOnly = *changes.SendOnly
	}
	if err := validateSendOnly(row.Provider, sendOnly); err != nil {
		return "", err
	}
	var webhooks []WebhookInput
	if changes.Webhooks != nil {
		webhooks = *changes.Webhooks
	} else if webhooks, err = storedWebhookInputs(ctx, o.db, row.ID); err != nil {
		return "", err
	}
	if err := validateWebhooks(webhooks, row.Provider, sendOnly); err != nil {
		return "", err
	}

	newName := name
	if changes.NewName != nil {
		newName = *changes.NewName
	}
	// A row with the new name makes the rename fail. Checked first, so the hold below does not wait
	// for a load of that other instance. The unique index stays the real guard.
	release := func() {}
	if newName != name {
		if _, taken, err := lookupInstance(ctx, o.db, newName); err != nil {
			return "", err
		} else if taken {
			return "", calyxerr.New(codes.AlreadyExists, "Instance with name %s already exists.", newName)
		}
		release = o.holdName(newName)
	}
	defer release()
	err = inTx(ctx, o.db, func(tx *sql.Tx) error {
		set := []string{"updated_at = ?"}
		args := []any{time.Now().UnixMilli()}
		if changes.NewName != nil {
			set, args = append(set, "name = ?"), append(args, newName)
		}
		if changes.IsEnabled != nil {
			set, args = append(set, "is_enabled = ?"), append(args, *changes.IsEnabled)
		}
		if changes.SendOnly != nil {
			set, args = append(set, "is_used_only_for_send_messages = ?"), append(args, *changes.SendOnly)
		}
		if changes.ProviderConfig != nil {
			set, args = append(set, "provider_config = ?"), append(args, nullable(*changes.ProviderConfig))
			// New credentials deserve a new try: the rebuild in UpdateInstanceConfig validates them.
			if row.Status == corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH {
				set = append(set, "connection_status = 'DISCONNECTED'")
			}
		}
		if _, err := tx.ExecContext(ctx, "UPDATE instances SET "+strings.Join(set, ", ")+" WHERE id = ?", append(args, row.ID)...); err != nil {
			return err
		}
		if changes.Webhooks != nil {
			return replaceWebhooks(ctx, tx, row.ID, *changes.Webhooks)
		}
		return nil
	})
	if isUniqueViolation(err) {
		return "", calyxerr.New(codes.AlreadyExists, "Instance with name %s already exists.", newName)
	}
	if err != nil {
		return "", err
	}
	// The close runs under configMu, so a hold never outlives configMu, and no rename waits for the
	// hold of another rename.
	if rebuilds(changes) {
		o.closeForChange(name)
	}
	return newName, nil
}

// rebuilds tells whether the changes need a new live instance. Webhooks are read from the database
// on every event, so a change of webhooks alone does not.
func rebuilds(changes UpdateInstanceParams) bool {
	return changes.NewName != nil || changes.IsEnabled != nil || changes.SendOnly != nil || changes.ProviderConfig != nil
}

// closeForChange closes the session of name after a change of its configuration.
func (o *Orchestrator) closeForChange(name string) {
	// A load that read the row before this change registers first, so it is closed here too.
	o.waitForLoad(name)
	o.mu.Lock()
	live := o.live[name]
	o.mu.Unlock()
	if live != nil {
		o.closeLive(live, false)
	}
	// A close that another caller started (the idle timer) is not in o.live. The session of the
	// device stays open until it ends, and it writes the last send for the next outbox.
	o.waitForClose(name)
}

// waitForClose waits for the close of name that runs now, if there is one.
func (o *Orchestrator) waitForClose(name string) {
	o.mu.Lock()
	closing := o.closing[name]
	o.mu.Unlock()
	if closing != nil {
		<-closing
	}
}

// holdName makes every new load of name wait, as it waits for a close, until release. A rename holds
// its new name from before its commit until the session of the old name is closed. Without it, a
// load of the new name opens a second session of the device, and its outbox does not get the last
// send of the old one (the minimum gap). Call it only under configMu.
func (o *Orchestrator) holdName(name string) (release func()) {
	o.mu.Lock()
	previous := o.closing[name]
	held := make(chan struct{})
	o.closing[name] = held
	inFlight := o.loading[name]
	o.mu.Unlock()
	// A hold exists only under configMu, so previous is a close or a removal of another instance
	// with this name. Neither one needs configMu, so these waits end.
	if previous != nil {
		<-previous
	}
	if inFlight != nil {
		<-inFlight.done
	}
	return func() {
		o.mu.Lock()
		if o.closing[name] == held {
			delete(o.closing, name)
		}
		o.mu.Unlock()
		close(held)
	}
}

func (o *Orchestrator) UpdateInstanceConfig(ctx context.Context, name string, changes UpdateInstanceParams) error {
	newName, err := o.writeChanges(ctx, name, changes)
	if err != nil {
		return err
	}
	o.invalidateWebhooks(name)
	o.invalidateWebhooks(newName)
	if rebuilds(changes) {
		o.reloadIfNeeded(ctx, newName)
	}
	return nil
}

func (o *Orchestrator) ListInstances(ctx context.Context) ([]InstanceSummary, error) {
	rows, err := listInstanceRows(ctx, o.db, "")
	if err != nil {
		return nil, err
	}
	webhooks, err := listWebhookRows(ctx, o.db)
	if err != nil {
		return nil, err
	}
	summaries := make([]InstanceSummary, 0, len(rows))
	for _, row := range rows {
		summary := InstanceSummary{
			Name: row.Name, Provider: row.Provider, Status: o.statusOf(row),
			IsEnabled: row.IsEnabled, SendOnly: row.IsSendOnly,
			DeviceIdentity: identityFromName(row.DeviceIdentity), Locale: row.Locale,
		}
		for _, w := range webhooks {
			if w.InstanceID == row.ID {
				summary.Webhooks = append(summary.Webhooks, w.WebhookSummary)
			}
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

// GetInstanceStatus never opens a connection: it reports what is known right now.
func (o *Orchestrator) GetInstanceStatus(ctx context.Context, name string) (corev1.InstanceStatus, error) {
	row, err := findInstance(ctx, o.db, name)
	if err != nil {
		return 0, err
	}
	return o.statusOf(row), nil
}

func (o *Orchestrator) statusOf(row InstanceRow) corev1.InstanceStatus {
	o.mu.Lock()
	live := o.live[row.Name]
	o.mu.Unlock()
	if live != nil {
		return live.provider.Status()
	}
	switch {
	case row.Status == corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH, row.Status == corev1.InstanceStatus_INSTANCE_STATUS_ERROR:
		return row.Status
	case row.IsSendOnly && row.IsEnabled:
		// A sleeping send-only instance is as usable as the last time it was connected.
		return row.Status
	default:
		return corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED
	}
}

// ---------------------------------------------------------------- validation

// validateSendOnly: only WhatsApp Web has a connection worth closing between messages.
func validateSendOnly(provider string, sendOnly bool) error {
	if sendOnly && provider == "META_CLOUD" {
		return calyxerr.New(codes.InvalidArgument,
			"is_used_only_for_send_messages does not apply to Meta Cloud: there is no connection to save.")
	}
	return nil
}

// checkPhoneNumberIDIsFree makes sure two instances never share a Meta phone number: webhooks
// from Meta are routed by it. exceptID is the instance being created again or updated.
func (o *Orchestrator) checkPhoneNumberIDIsFree(ctx context.Context, providerConfig, exceptID string) error {
	if providerConfig == "" {
		return nil
	}
	var wanted MetaCredentials
	if err := json.Unmarshal([]byte(providerConfig), &wanted); err != nil || wanted.PhoneNumberID == "" {
		return nil
	}
	rows, err := listInstanceRows(ctx, o.db, "WHERE provider = 'META_CLOUD' AND id <> ?", exceptID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		var other MetaCredentials
		if json.Unmarshal([]byte(row.ProviderConfig), &other) == nil && other.PhoneNumberID == wanted.PhoneNumberID {
			return calyxerr.New(codes.AlreadyExists, "The phone number id %s already belongs to instance %s.",
				wanted.PhoneNumberID, row.Name)
		}
	}
	return nil
}

func validateWebhooks(webhooks []WebhookInput, provider string, sendOnly bool) error {
	if sendOnly && len(webhooks) > 0 {
		return calyxerr.New(codes.InvalidArgument, "An instance used only for sending messages cannot have webhooks: "+
			"it does not listen to events. Remove the webhooks or set 'is_used_only_for_send_messages' to false.")
	}
	supported := events.SupportedEventTypes(provider)
	seen := map[string]bool{}
	for _, w := range webhooks {
		if !slices.Contains(supported, w.Event) {
			return calyxerr.New(codes.InvalidArgument, "Event '%s' is not supported. Supported events: %s.",
				w.Event, strings.Join(supported, ", "))
		}
		if seen[w.Event] {
			return calyxerr.New(codes.InvalidArgument, "Event '%s' has more than one webhook. Only one is allowed per event.", w.Event)
		}
		seen[w.Event] = true
		parsed, err := url.Parse(w.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return calyxerr.New(codes.InvalidArgument, "'%s' is not a valid webhook URL. It must start with http:// or https://.", w.URL)
		}
	}
	return nil
}

func (o *Orchestrator) invalidateWebhooks(name string) {
	if o.opts.Webhooks != nil {
		o.opts.Webhooks.Invalidate(name)
	}
}
