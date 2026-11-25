package engine

import (
	"context"
	"time"

	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// Provider is one connected account: WhatsApp Web through whatsmeow, or the Meta Cloud API.
// The Outbox and the Orchestrator only know this interface; tests use testutil.FakeProvider.
type Provider interface {
	InstanceName() string
	Pacing() PacingPolicy
	Status() corev1.InstanceStatus
	Connect(ctx context.Context) error
	// NormalizeRecipient turns what the client sent ("34600111222", a JID, ...) into the form
	// SendText expects, or returns an InvalidArgument error.
	NormalizeRecipient(to string) (string, error)
	// SendText sends at once. There is no pacing here: every caller goes through the Outbox.
	SendText(ctx context.Context, recipient, text string) (messageID string, err error)
	// ShowTyping is a no-op for providers without typing indicators.
	ShowTyping(ctx context.Context, recipient string, d time.Duration) error
	// SetOnline is a no-op for providers without presence.
	SetOnline(ctx context.Context, online bool) error
	Destroy(ctx context.Context) error
	Logout(ctx context.Context) error
}

// ProviderHooks is how a provider reports to the rest of the core. The orchestrator gives them to
// the factory; the provider calls them from any goroutine and they never block for long.
type ProviderHooks struct {
	// Every time the connection status changes.
	OnStatus func(status corev1.InstanceStatus)
	// Incoming messages, delivery updates and raw provider events (see events.SupportedEventTypes).
	OnEvent func(eventType string, payload any)
}

// PairingMethod is a QR code (QR true) or a pairing code for PhoneNumber.
type PairingMethod struct {
	QR          bool
	PhoneNumber string
}

// Pairer is implemented by providers that link a device (WhatsApp Web). Pair returns once the
// device is linked and connected, or with an error; onCode receives every new QR or pairing code.
// Providers without it (Meta Cloud) are created by validating their credentials with Connect.
type Pairer interface {
	Pair(ctx context.Context, method PairingMethod, onCode func(code string)) error
}

// InstanceRow is an instance as stored in the database.
type InstanceRow struct {
	ID, Name, Provider string
	IsEnabled          bool
	IsSendOnly         bool
	Status             corev1.InstanceStatus
	DeviceJID          string
	ProviderConfig     string
	// WhatsApp Web only, "" when the instance has none (Meta Cloud, or paired before Part B of
	// the core changes): the DeviceIdentity enum name and the locale ("es-ES").
	DeviceIdentity, Locale string
}

// ProviderFactory builds the provider of an instance. Tests replace it to avoid real connections.
type ProviderFactory func(row InstanceRow, hooks ProviderHooks) (Provider, error)

// MetaCredentials is what a Meta Cloud instance stores in InstanceRow.ProviderConfig, as JSON.
type MetaCredentials struct {
	PhoneNumberID string `json:"phoneNumberId"`
	AccessToken   string `json:"accessToken"`
	// Verifies the signature of the webhooks Meta sends to the core.
	AppSecret string `json:"appSecret"`
}
