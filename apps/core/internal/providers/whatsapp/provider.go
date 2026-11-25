package whatsapp

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWa6"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waevents "go.mau.fi/whatsmeow/types/events"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/database"
	"calyx/core/internal/engine"
	"calyx/core/internal/events"
	"calyx/core/internal/locale"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

const (
	// How long Connect and the end of a pairing wait for WhatsApp to accept the session.
	connectTimeout = 60 * time.Second
	firstBackoff   = 5 * time.Second
	maxBackoff     = 5 * time.Minute
)

// Backoff is the wait before reconnection attempt number attempt (0 is the first).
func Backoff(attempt int) time.Duration {
	delay := firstBackoff
	for range attempt {
		delay *= 2
		if delay >= maxBackoff {
			return maxBackoff
		}
	}
	return delay
}

// Provider is one WhatsApp Web session (a linked device) through whatsmeow.
type Provider struct {
	instanceID, name string
	sendOnly         bool
	db               *sql.DB
	container        *sqlstore.Container
	hooks            engine.ProviderHooks
	log              *slog.Logger
	// The device identity and the locale of the instance. Without them (an instance paired before
	// the core stored them), whatsmeow's payload goes out unchanged.
	entry    deviceEntry
	loc      locale.Locale
	hasEntry bool
	// Asks WhatsApp for a pairing code. Tests replace it, because the real one needs WhatsApp.
	pairPhone func(ctx context.Context, phone string, clientType whatsmeow.PairClientType, text string) (string, error)
	// Opens the socket of a client in Connect. Tests replace it, because the real one needs WhatsApp.
	connectClient func(ctx context.Context, client *whatsmeow.Client) error
	// Closes the socket of a client in Destroy. Tests replace it to see the order of the close.
	disconnectClient func(client *whatsmeow.Client)
	// whatsmeow keeps a socket open only while the context given to ConnectContext lives, so
	// every connection uses this one, which ends with Destroy or Logout. A call's context would
	// close the session as soon as that call returns.
	life      context.Context
	endOfLife context.CancelFunc

	// reportMu holds a status report from its check of closed to the end of the hook. Destroy and
	// Logout take it to set closed: no report starts after that, and none is still in flight.
	// Order: reportMu, then mu. The hook only writes the row and publishes, so the wait is short.
	reportMu sync.Mutex

	mu        sync.Mutex
	deviceJID string
	client    *whatsmeow.Client
	status    corev1.InstanceStatus
	changed   chan struct{} // closed and replaced on every status change
	closed    bool          // after Destroy or Logout: nothing reconnects any more
	pairing   bool          // during Pair, disconnections are the pairing's business
	dropped   bool          // WhatsApp closed the connection during Pair
	accepted  bool          // WhatsApp accepted the session, so Destroy closes it in order
	unlink    func() bool   // stops the link of a Connect context to p.life; nil when not armed
	linkEnded bool          // that link ended p.life before WhatsApp accepted the session
	attempt   int           // reconnection attempts since the last success
	reconnect *time.Timer
}

func New(row engine.InstanceRow, hooks engine.ProviderHooks, db *sql.DB, container *sqlstore.Container) *Provider {
	life, endOfLife := context.WithCancel(context.Background())
	p := &Provider{
		life:       life,
		endOfLife:  endOfLife,
		instanceID: row.ID,
		name:       row.Name,
		sendOnly:   row.IsSendOnly,
		db:         db,
		container:  container,
		hooks:      hooks,
		log:        slog.With("instance", row.Name),
		deviceJID:  row.DeviceJID,
		status:     corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED,
		changed:    make(chan struct{}),
	}
	entry, hasEntry := deviceEntries[corev1.DeviceIdentity(corev1.DeviceIdentity_value[row.DeviceIdentity])]
	loc, err := locale.Parse(row.Locale)
	if hasEntry && err == nil {
		p.entry, p.loc, p.hasEntry = entry, loc, true
	}
	p.pairPhone = func(ctx context.Context, phone string, clientType whatsmeow.PairClientType, text string) (string, error) {
		return p.currentClient().PairPhone(ctx, phone, true, clientType, text)
	}
	p.connectClient = func(ctx context.Context, client *whatsmeow.Client) error { return client.ConnectContext(ctx) }
	p.disconnectClient = (*whatsmeow.Client).Disconnect
	return p
}

func (p *Provider) InstanceName() string { return p.name }

func (p *Provider) Pacing() engine.PacingPolicy { return engine.HumanPacing }

func (p *Provider) Status() corev1.InstanceStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

func (p *Provider) NormalizeRecipient(to string) (string, error) { return NormalizeRecipient(to) }

// setStatus records a new status. report is false for changes nobody must hear about (Destroy).
// After Destroy or Logout, a reported change does nothing: the row of a closed instance keeps its
// status, so the wake loop does not see a false one.
func (p *Provider) setStatus(status corev1.InstanceStatus, report bool) {
	if report {
		p.reportMu.Lock()
		defer p.reportMu.Unlock()
	}
	p.mu.Lock()
	if report && p.closed {
		p.mu.Unlock()
		return
	}
	if p.status == status {
		p.mu.Unlock()
		return
	}
	p.status = status
	close(p.changed)
	p.changed = make(chan struct{})
	p.mu.Unlock()
	if report && p.hooks.OnStatus != nil {
		p.hooks.OnStatus(status)
	}
}

// waitConnected returns when WhatsApp accepted the session, or why it did not.
func (p *Provider) waitConnected(ctx context.Context) error {
	timeout := time.NewTimer(connectTimeout)
	defer timeout.Stop()
	for {
		p.mu.Lock()
		status, changed := p.status, p.changed
		p.mu.Unlock()
		switch status {
		case corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED:
			return nil
		case corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH:
			return calyxerr.New(codes.FailedPrecondition, "Instance %s needs to authenticate again.", p.name)
		case corev1.InstanceStatus_INSTANCE_STATUS_ERROR:
			return calyxerr.New(codes.FailedPrecondition, "WhatsApp refused the connection of instance %s.", p.name)
		}
		select {
		case <-changed:
		case <-timeout.C:
			return calyxerr.New(codes.Unavailable, "Instance %s could not connect in time.", p.name)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// newClient builds the whatsmeow client of the stored device jid, or of a new device when jid is
// empty. It returns nil when the stored device no longer exists.
func (p *Provider) newClient(ctx context.Context, jid types.JID) (*whatsmeow.Client, error) {
	device := p.container.NewDevice()
	if !jid.IsEmpty() {
		stored, err := p.container.GetDevice(ctx, jid)
		if err != nil || stored == nil {
			return nil, err
		}
		device = stored
	}
	client := whatsmeow.NewClient(device, newLogger(p.name))
	if p.hasEntry {
		// whatsmeow reads this field before its shared default, so each instance has its own.
		client.QRClientType = p.entry.PairType
		client.GetClientPayload = func() *waWa6.ClientPayload { return clientPayload(client.Store, p.entry, p.loc, p.log) }
	}
	// Reconnection is done here, with the backoff of reconnectLater.
	client.EnableAutoReconnect = false
	client.AddEventHandler(p.handleEvent)
	p.mu.Lock()
	p.client = client
	p.mu.Unlock()
	return client, nil
}

// Connect opens the session of an already paired device and waits until WhatsApp accepts it.
func (p *Provider) Connect(ctx context.Context) error {
	p.mu.Lock()
	deviceJID := p.deviceJID
	p.mu.Unlock()

	jid, err := types.ParseJID(deviceJID)
	var client *whatsmeow.Client
	if deviceJID != "" && err == nil {
		client, err = p.newClient(ctx, jid)
		if err != nil {
			return err
		}
	}
	if client == nil {
		p.setStatus(corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH, true)
		return calyxerr.New(codes.FailedPrecondition, "Instance %s needs to authenticate again.", p.name)
	}

	p.setStatus(corev1.InstanceStatus_INSTANCE_STATUS_CONNECTING, true)
	// The dial uses p.life, so only this link lets the end of ctx stop a dial that hangs.
	stopLink := context.AfterFunc(ctx, p.endOfLife)
	p.mu.Lock()
	p.unlink = stopLink
	p.mu.Unlock()
	if err := p.connectClient(p.life, client); err != nil {
		p.setStatus(corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED, true)
		p.reconnectLater()
		return calyxerr.New(codes.Unavailable, "Instance %s could not connect: %v", p.name, err)
	}
	if err := p.waitConnected(ctx); err != nil {
		return err
	}
	if !p.accept() {
		return calyxerr.New(codes.Unavailable, "Instance %s could not connect in time.", p.name)
	}
	return nil
}

// accept records that WhatsApp accepted the session: from now on, only Destroy and Logout end it.
// It returns false when the end of a Connect context already ended p.life.
func (p *Provider) accept() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.unlink != nil {
		p.linkEnded = !p.unlink()
		p.unlink = nil
	}
	p.accepted = !p.linkEnded
	return p.accepted
}

// Pair links a new device. onCode receives every QR code, or the one pairing code.
func (p *Provider) Pair(ctx context.Context, method engine.PairingMethod, onCode func(string)) error {
	p.mu.Lock()
	p.pairing, p.dropped = true, false
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.pairing = false
		p.mu.Unlock()
	}()

	client, err := p.newClient(ctx, types.EmptyJID)
	if err != nil {
		return err
	}
	// The QR channel must exist before connecting. It also tells when the pairing ends.
	qrChannel, err := client.GetQRChannel(ctx)
	if err != nil {
		return err
	}
	if err := client.ConnectContext(p.life); err != nil {
		return calyxerr.New(codes.Unavailable, "Could not reach WhatsApp to pair: %v", err)
	}

	askedForCode := false
	for {
		select {
		case <-ctx.Done():
			client.Disconnect()
			return ctx.Err()
		case item, open := <-qrChannel:
			if !open {
				return calyxerr.New(codes.Unavailable, "The pairing was interrupted.")
			}
			switch {
			case item.Event == whatsmeow.QRChannelEventCode && method.QR:
				// The text of the QR: the client draws the image (see instance.proto).
				onCode(item.Code)
			case item.Event == whatsmeow.QRChannelEventCode && !askedForCode:
				// With a pairing code, the first QR only says the socket is ready to ask for it.
				askedForCode = true
				code, err := p.requestPairingCode(ctx, method.PhoneNumber)
				if err != nil {
					client.Disconnect()
					return err
				}
				onCode(code)
			case item.Event == whatsmeow.QRChannelEventCode:
				// A new QR while waiting for the code to be entered: nothing to do.
			case item == whatsmeow.QRChannelSuccess:
				return p.finishPairing(ctx, client)
			case item == whatsmeow.QRChannelTimeout:
				p.mu.Lock()
				dropped := p.dropped
				p.mu.Unlock()
				return pairingEnd(item, dropped)
			case item == whatsmeow.QRChannelScannedWithoutMultidevice, item == whatsmeow.QRChannelClientOutdated:
				return pairingEnd(item, false)
			default:
				client.Disconnect()
				return pairingEnd(item, false)
			}
		}
	}
}

// pairingEnd gives the error for a QR channel item that ends a pairing. whatsmeow reports the end
// of the codes and a dropped connection with the same timeout item, so disconnected tells them apart.
// Only the end of the codes gives DeadlineExceeded.
func pairingEnd(item whatsmeow.QRChannelItem, disconnected bool) error {
	switch {
	case item == whatsmeow.QRChannelTimeout && disconnected:
		return calyxerr.New(codes.Unavailable, "The connection to WhatsApp ended during the pairing.")
	case item == whatsmeow.QRChannelTimeout:
		return calyxerr.New(codes.DeadlineExceeded, "The pairing expired: the code was not used in time.")
	case item == whatsmeow.QRChannelScannedWithoutMultidevice:
		return calyxerr.New(codes.FailedPrecondition, "The phone scanned the code without multi-device support.")
	case item == whatsmeow.QRChannelClientOutdated:
		return calyxerr.New(codes.FailedPrecondition, "WhatsApp says this client is outdated. Update whatsmeow.")
	}
	return calyxerr.New(codes.Unavailable, "The pairing failed (%s): %v", item.Event, item.Error)
}

// requestPairingCode asks WhatsApp for the pairing code, with the device identity of the instance.
func (p *Provider) requestPairingCode(ctx context.Context, phone string) (string, error) {
	entry := p.entry
	if !p.hasEntry {
		// The engine always gives a pairing an identity; this keeps the label of before.
		entry = deviceEntry{Text: "Chrome (Linux)", PairType: whatsmeow.PairClientChrome}
	}
	code, err := p.pairPhone(ctx, phone, entry.PairType, entry.Text)
	if errors.Is(err, whatsmeow.ErrIQBadRequest) {
		// WhatsApp gives no reason: a bad phone number and a refused label get the same answer.
		return "", calyxerr.New(codes.FailedPrecondition, "WhatsApp refused the request for a pairing code. "+
			"Check the phone number. If it is correct, choose another device identity than %s.", entry.Text)
	}
	if err != nil {
		return "", calyxerr.New(codes.FailedPrecondition, "Could not generate the pairing code: %v", err)
	}
	return code, nil
}

// finishPairing stores which device belongs to the instance and waits for the session, which
// whatsmeow reopens by itself right after a successful pairing.
func (p *Provider) finishPairing(ctx context.Context, client *whatsmeow.Client) error {
	jid := client.Store.ID.String()
	p.mu.Lock()
	p.deviceJID = jid
	p.mu.Unlock()
	if _, err := p.db.ExecContext(ctx, "UPDATE instances SET device_jid = ? WHERE id = ?", jid, p.instanceID); err != nil {
		return err
	}
	return p.waitConnected(ctx)
}

func (p *Provider) handleEvent(evt any) {
	switch e := evt.(type) {
	case *waevents.Connected:
		p.accept()
		p.mu.Lock()
		p.attempt = 0
		p.mu.Unlock()
		p.setStatus(corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED, true)
	case *waevents.Message:
		if payload, ok := MessageReceived(e, p.altOf); ok && !p.sendOnly {
			p.emit("message.received", payload)
		}
	case *waevents.Receipt:
		if !p.sendOnly {
			for _, status := range MessageStatuses(e, p.altOf) {
				p.emit("message.status", status)
			}
		}
	}

	if status, reconnect, ok := DisconnectStatus(evt); ok {
		p.onDisconnect(status, reconnect)
	}

	if t := reflect.TypeOf(evt); !p.sendOnly && t != nil && t.Kind() == reflect.Pointer {
		name := t.Elem().Name() // "Message" for *events.Message
		if slices.Contains(events.WhatsmeowRawEvents, name) {
			p.emit("whatsmeow."+name, evt)
		}
	}
}

func (p *Provider) emit(eventType string, payload any) {
	if p.hooks.OnEvent != nil {
		p.hooks.OnEvent(eventType, payload)
	}
}

func (p *Provider) onDisconnect(status corev1.InstanceStatus, reconnect bool) {
	p.mu.Lock()
	closed, pairing := p.closed, p.pairing
	if pairing {
		p.dropped = true
	}
	p.mu.Unlock()
	if closed {
		return
	}
	if status == corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH {
		// whatsmeow already deleted the device keys; the instance no longer has a device.
		p.mu.Lock()
		p.deviceJID = ""
		p.mu.Unlock()
		if _, err := p.db.Exec("UPDATE instances SET device_jid = NULL WHERE id = ?", p.instanceID); err != nil {
			p.log.Error("Could not clear the device of a logged out instance.", "err", err)
		}
		p.forgetUnusedData(context.Background())
		p.log.Warn("The device was unlinked: the instance needs to be paired again.")
	}
	p.setStatus(status, true)
	if reconnect && !pairing {
		p.reconnectLater()
	}
}

// reconnectLater schedules one reconnection attempt, if none is scheduled yet.
func (p *Provider) reconnectLater() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.reconnect != nil {
		return
	}
	delay := Backoff(p.attempt)
	p.attempt++
	p.log.Info("Reconnecting soon.", "in", delay.String(), "attempt", p.attempt)
	p.reconnect = time.AfterFunc(delay, p.tryReconnect)
}

func (p *Provider) tryReconnect() {
	p.mu.Lock()
	p.reconnect = nil
	client, closed := p.client, p.closed
	p.mu.Unlock()
	if closed || client == nil {
		return
	}
	p.setStatus(corev1.InstanceStatus_INSTANCE_STATUS_CONNECTING, true)
	err := client.ConnectContext(p.life)
	if err == nil || errors.Is(err, whatsmeow.ErrAlreadyConnected) {
		return // the Connected event finishes the job
	}
	p.log.Warn("Reconnection failed.", "err", err)
	p.setStatus(corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED, true)
	p.reconnectLater()
}

// altOf returns the other address of a user from whatsmeow's LID map, or an empty JID.
func (p *Provider) altOf(jid types.JID) types.JID {
	client := p.currentClient()
	if client == nil || client.Store.LIDs == nil {
		return types.EmptyJID
	}
	var alt types.JID
	var err error
	switch jid.Server {
	case types.HiddenUserServer:
		alt, err = client.Store.LIDs.GetPNForLID(context.Background(), jid)
	case types.DefaultUserServer:
		alt, err = client.Store.LIDs.GetLIDForPN(context.Background(), jid)
	default:
		return types.EmptyJID
	}
	if err != nil {
		return types.EmptyJID
	}
	return alt
}

func (p *Provider) currentClient() *whatsmeow.Client {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.client
}

func (p *Provider) loggedInClient() (*whatsmeow.Client, error) {
	client := p.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return nil, calyxerr.New(codes.Unavailable, "Instance %s is not connected right now.", p.name)
	}
	return client, nil
}

func (p *Provider) SendText(ctx context.Context, recipient, text string) (string, error) {
	client, err := p.loggedInClient()
	if err != nil {
		return "", err
	}
	jid, err := types.ParseJID(recipient)
	if err != nil {
		return "", calyxerr.New(codes.InvalidArgument, "'%s' is not a valid recipient.", recipient)
	}
	resp, err := client.SendMessage(ctx, jid, &waE2E.Message{Conversation: proto.String(text)})
	if err != nil {
		return "", sendError(p.name, err)
	}
	return resp.ID, nil
}

// sendError: UNAVAILABLE means "not sent", because nothing left towards WhatsApp. Any other
// error may have reached WhatsApp, so it stays as it is (an unknown outcome).
func sendError(name string, err error) error {
	if errors.Is(err, whatsmeow.ErrNotConnected) || errors.Is(err, whatsmeow.ErrNotLoggedIn) {
		return calyxerr.New(codes.Unavailable, "Instance %s lost its connection.", name)
	}
	return err
}

// ShowTyping shows "typing..." for d, then stops. It returns early if ctx ends.
func (p *Provider) ShowTyping(ctx context.Context, recipient string, d time.Duration) error {
	client, err := p.loggedInClient()
	if err != nil {
		return err
	}
	jid, err := types.ParseJID(recipient)
	if err != nil {
		return err
	}
	if err := client.SendChatPresence(ctx, jid, types.ChatPresenceComposing, types.ChatPresenceMediaText); err != nil {
		return err
	}
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
	return client.SendChatPresence(context.Background(), jid, types.ChatPresencePaused, types.ChatPresenceMediaText)
}

func (p *Provider) SetOnline(ctx context.Context, online bool) error {
	client, err := p.loggedInClient()
	if err != nil {
		return err
	}
	presence := types.PresenceUnavailable
	if online {
		presence = types.PresenceAvailable
	}
	return client.SendPresence(ctx, presence)
}

// Destroy closes the session and keeps the device, so the instance can connect again later. The
// status change is not reported: a sleeping send-only instance keeps its last known status.
func (p *Provider) Destroy(ctx context.Context) error {
	p.reportMu.Lock()
	p.mu.Lock()
	p.closed = true
	if p.reconnect != nil {
		p.reconnect.Stop()
		p.reconnect = nil
	}
	// A failed connect leaves its link armed. The context of a load lives as long as the core, so
	// the link must not stay on it.
	if p.unlink != nil {
		p.unlink()
		p.unlink = nil
	}
	client, accepted := p.client, p.accepted
	p.mu.Unlock()
	p.reportMu.Unlock()
	// Disconnect waits for a dial that runs, and only the end of p.life stops that dial. An accepted
	// session disconnects first: the end of p.life closes its socket with no close frame.
	if !accepted {
		p.endOfLife()
	}
	if client != nil {
		p.disconnectClient(client)
	}
	p.endOfLife()
	p.setStatus(corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED, false)
	return nil
}

// Logout unlinks the device from the phone and deletes its keys. Without a connection the device
// cannot be unlinked (the phone keeps showing it until removed there), but the keys are still
// deleted so nothing is left behind.
func (p *Provider) Logout(ctx context.Context) error {
	p.reportMu.Lock()
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.reportMu.Unlock()
	defer func() { _ = p.Destroy(ctx) }()
	defer p.forgetUnusedData(context.WithoutCancel(ctx)) // after the device is deleted below

	client := p.currentClient()
	if client == nil {
		return nil
	}
	var logoutErr error
	if client.IsLoggedIn() {
		// On success whatsmeow also deletes the device keys.
		if logoutErr = client.Logout(ctx); logoutErr == nil {
			return nil
		}
	}
	if client.Store.ID != nil {
		if err := client.Store.Delete(ctx); err != nil {
			p.log.Warn("Could not delete the device keys.", "err", err)
		}
	}
	return logoutErr
}

// forgetUnusedData deletes what whatsmeow keeps after a device is gone (see
// database.ForgetUnusedWhatsmeowData).
func (p *Provider) forgetUnusedData(ctx context.Context) {
	if err := database.ForgetUnusedWhatsmeowData(ctx, p.db); err != nil {
		p.log.Warn("Could not delete the data of removed devices.", "err", err)
	}
}

var (
	_ engine.Provider = (*Provider)(nil)
	_ engine.Pairer   = (*Provider)(nil)
)
