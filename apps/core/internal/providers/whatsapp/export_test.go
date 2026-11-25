package whatsapp

import (
	"context"

	"go.mau.fi/whatsmeow"
	waevents "go.mau.fi/whatsmeow/types/events"
)

// Hooks for the external tests of package whatsapp_test.

// SetConnectClient replaces the call that opens the socket of a client in Connect.
func SetConnectClient(p *Provider, fn func(ctx context.Context, client *whatsmeow.Client) error) {
	p.connectClient = fn
}

// Accept does what whatsmeow does when WhatsApp accepts the session.
func Accept(p *Provider) { p.handleEvent(&waevents.Connected{}) }

// SetDisconnectClient replaces the call that closes the socket of a client in Destroy.
func SetDisconnectClient(p *Provider, fn func(client *whatsmeow.Client)) { p.disconnectClient = fn }

// TryReconnect runs one reconnection attempt now, as the timer of reconnectLater does.
func TryReconnect(p *Provider) { p.tryReconnect() }
