package httpapi

import (
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"calyx/backend/internal/auth"
	"calyx/backend/internal/bulk"
	"calyx/backend/internal/config"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/instances"
	"calyx/backend/internal/tenancy"
)

// utc returns t in UTC, so encoding/json writes RFC 3339 with "Z".
func utc(t time.Time) time.Time { return t.UTC() }

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

type userJSON struct {
	ID             uuid.UUID  `json:"id" doc:"The id of the user, a UUID."`
	Email          string     `json:"email" doc:"The email address of the login, in lower case."`
	Role           string     `json:"role" doc:"owner or client. An owner manages the organization; a client user works only with the instances of its own client."`
	ClientID       *uuid.UUID `json:"client_id" doc:"The client of a client user, a UUID. null for an owner."`
	OrganizationID uuid.UUID  `json:"organization_id" doc:"The organization of the user, a UUID."`
}

func userResource(u gen.User) userJSON {
	return userJSON{ID: u.ID, Email: u.Email, Role: u.Role, ClientID: u.ClientID, OrganizationID: u.OrganizationID}
}

type metaWebhook struct {
	URL         string `json:"url" doc:"The callback URL to set in the Meta app."`
	VerifyToken string `json:"verify_token" doc:"The verify token to set in the Meta app, next to the URL."`
}

// metaWebhookJSON is the meta_webhook field: the object or null. Huma refuses a nullable
// object field, so its Schema documents both.
type metaWebhookJSON metaWebhook

func (metaWebhookJSON) Schema(r huma.Registry) *huma.Schema {
	return &huma.Schema{OneOf: []*huma.Schema{
		r.Schema(reflect.TypeFor[metaWebhook](), true, ""),
		{Type: huma.TypeNull},
	}}
}

type organizationJSON struct {
	ID                        uuid.UUID        `json:"id" doc:"The id of the organization, a UUID."`
	Name                      string           `json:"name" doc:"The name of the organization."`
	MaxInstances              int32            `json:"max_instances" doc:"The largest number of instances of the organization, all clients included. 0 allows none."`
	InstanceCount             int              `json:"instance_count" doc:"The number of instances of the organization, in every state."`
	WhatsAppWebAllowed        bool             `json:"whatsapp_web_allowed" doc:"False when the operator of the server blocks WHATSAPP_WEB for the organization. Then no WHATSAPP_WEB instance can be created or paired."`
	WhatsAppWebAcknowledgedAt *time.Time       `json:"whatsapp_web_acknowledged_at" doc:"When an owner accepted the WhatsApp Web terms, RFC 3339 in UTC. null until then: a new WHATSAPP_WEB instance is refused."`
	MetaWebhook               *metaWebhookJSON `json:"meta_webhook" doc:"The values to set in the Meta app of each META_CLOUD instance. null when the server has no Meta webhook."`
	SMTPConfigured            bool             `json:"smtp_configured" doc:"True when the server has a mail server. When false, Calyx sends no alert mail."`
}

func organizationResource(o tenancy.OrganizationView, cfg config.Config) organizationJSON {
	out := organizationJSON{
		ID:                        o.ID,
		Name:                      o.Name,
		MaxInstances:              o.MaxInstances,
		InstanceCount:             o.InstanceCount,
		WhatsAppWebAllowed:        o.WhatsappWebAllowed,
		WhatsAppWebAcknowledgedAt: utcPtr(o.WhatsappWebAcknowledgedAt),
		SMTPConfigured:            cfg.MailConfigured(),
	}
	if cfg.CoreMetaWebhookURL != "" {
		out.MetaWebhook = &metaWebhookJSON{URL: cfg.CoreMetaWebhookURL, VerifyToken: cfg.CoreMetaVerifyToken}
	}
	return out
}

type organizationSummaryJSON struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

func organizationSummary(o gen.HTTPGetOrganizationSummaryRow) organizationSummaryJSON {
	return organizationSummaryJSON{ID: o.ID, Name: o.Name}
}

// meOrganizationJSON is the organization of getMe, for the document only: an organizationJSON or an
// organizationSummaryJSON. A client user gets only the required fields.
type meOrganizationJSON struct {
	ID                        uuid.UUID        `json:"id" doc:"The id of the organization, a UUID."`
	Name                      string           `json:"name" doc:"The name of the organization."`
	MaxInstances              int32            `json:"max_instances,omitempty" doc:"The largest number of instances of the organization, all clients included; 0 allows none. Absent for a client user."`
	InstanceCount             int              `json:"instance_count,omitempty" doc:"The number of instances of the organization, in every state. Absent for a client user."`
	WhatsAppWebAllowed        bool             `json:"whatsapp_web_allowed,omitempty" doc:"False when the operator of the server blocks WHATSAPP_WEB for the organization. Absent for a client user."`
	WhatsAppWebAcknowledgedAt *time.Time       `json:"whatsapp_web_acknowledged_at,omitempty" nullable:"true" doc:"When an owner accepted the WhatsApp Web terms, RFC 3339 in UTC; null until then. Absent for a client user."`
	MetaWebhook               *metaWebhookJSON `json:"meta_webhook,omitempty" doc:"The values to set in the Meta app of each META_CLOUD instance; null when the server has no Meta webhook. Absent for a client user."`
	SMTPConfigured            bool             `json:"smtp_configured,omitempty" doc:"True when the server has a mail server; when false, Calyx sends no alert mail. Absent for a client user."`
}

type clientJSON struct {
	ID                 uuid.UUID              `json:"id" doc:"The id of the client, a UUID."`
	Name               string                 `json:"name" doc:"The name of the client."`
	IsDefault          bool                   `json:"is_default" doc:"True for the default client of the organization. It has no user and cannot be deleted."`
	CanCreateInstances bool                   `json:"can_create_instances" doc:"True when the user of this client can create instances. It does not limit an owner."`
	MaxInstances       int32                  `json:"max_instances" doc:"The largest number of instances of this client. 0 means no limit of its own: only the limit of the organization applies."`
	InstanceCount      int                    `json:"instance_count" doc:"The number of instances of this client, in every state."`
	UserEmail          *string                `json:"user_email" doc:"The email address of the user of this client. null when the client has no active user."`
	PendingInvitation  *pendingInvitationJSON `json:"pending_invitation" doc:"The invitation that nobody accepted yet, also an expired one. null when there is none."`
}

type pendingInvitation struct {
	Email     string    `json:"email" doc:"The email address of the invited person."`
	ExpiresAt time.Time `json:"expires_at" doc:"When the invitation expires, RFC 3339 in UTC. It can be in the past."`
}

// pendingInvitationJSON is the pending_invitation field: the object or null, documented like
// metaWebhookJSON.
type pendingInvitationJSON pendingInvitation

func (pendingInvitationJSON) Schema(r huma.Registry) *huma.Schema {
	return &huma.Schema{OneOf: []*huma.Schema{
		r.Schema(reflect.TypeFor[pendingInvitation](), true, ""),
		{Type: huma.TypeNull},
	}}
}

func clientResource(c tenancy.ClientView) clientJSON {
	out := clientJSON{
		ID: c.ID, Name: c.Name, IsDefault: c.IsDefault, CanCreateInstances: c.CanCreateInstances,
		MaxInstances: c.MaxInstances, InstanceCount: c.InstanceCount,
	}
	if c.UserEmail != "" {
		e := c.UserEmail
		out.UserEmail = &e
	}
	if inv := c.PendingInvitation; inv != nil {
		out.PendingInvitation = &pendingInvitationJSON{Email: inv.Email, ExpiresAt: utc(inv.ExpiresAt)}
	}
	return out
}

type invitationLookupJSON struct {
	Email            string    `json:"email" doc:"The email address of the invited person. It becomes the email of the login."`
	OrganizationName string    `json:"organization_name" doc:"The name of the organization that sent the invitation."`
	ExpiresAt        time.Time `json:"expires_at" doc:"When the invitation expires, RFC 3339 in UTC."`
}

func invitationLookupResource(i auth.InvitationInfo) invitationLookupJSON {
	return invitationLookupJSON{Email: i.Email, OrganizationName: i.OrganizationName, ExpiresAt: utc(i.ExpiresAt)}
}

type passwordResetLookupJSON struct {
	Email string `json:"email" doc:"The email address of the user whose password the reset changes."`
}

type webhookJSON struct {
	URL       string `json:"url" doc:"The URL that gets each event in a POST request."`
	Event     string `json:"event" doc:"connection.update, message.received, message.status or bulk.finished. Also meta.webhook for META_CLOUD, or whatsmeow.<name>, for example whatsmeow.Receipt, for WHATSAPP_WEB."`
	HasSecret bool   `json:"has_secret" doc:"True when a secret signs the deliveries. The secret is never returned."`
}

type alert struct {
	Kind      string     `json:"kind" doc:"The problem of the instance: needs_auth, error, disconnected or missing."`
	Since     *time.Time `json:"since" doc:"When the alert opened, RFC 3339 in UTC. null when the time is unknown."`
	EmailedAt *time.Time `json:"emailed_at" doc:"When the alert mail was sent to the owner, RFC 3339 in UTC. null when no mail was sent yet."`
}

// alertJSON is the alert field: the object or null, documented like metaWebhookJSON.
type alertJSON alert

func (alertJSON) Schema(r huma.Registry) *huma.Schema {
	return &huma.Schema{OneOf: []*huma.Schema{
		r.Schema(reflect.TypeFor[alert](), true, ""),
		{Type: huma.TypeNull},
	}}
}

type instanceJSON struct {
	ID              uuid.UUID               `json:"id" doc:"The id of the instance, a UUID."`
	ClientID        uuid.UUID               `json:"client_id" doc:"The client that the instance belongs to, a UUID."`
	DisplayName     string                  `json:"display_name" doc:"The name of the instance in Calyx. WhatsApp does not see it."`
	Provider        instanceProvider        `json:"provider" doc:"WHATSAPP_WEB (unofficial, paired with a phone) or META_CLOUD (the official Meta Cloud API)."`
	SendOnly        bool                    `json:"send_only" doc:"True for a WHATSAPP_WEB instance that connects only to send. It receives no messages and has no webhooks."`
	Enabled         bool                    `json:"enabled" doc:"False for a disabled instance: it does not connect and cannot send."`
	State           instanceState           `json:"state" doc:"creating (not ready yet), active, removing (the removal is not finished) or missing (Calyx cannot find it). A missing instance can return to active; if not, remove it and create it again."`
	Status          *nullableInstanceStatus `json:"status" doc:"The last known connection status. NEEDS_AUTH needs a new pairing (META_CLOUD: new meta_credentials), ERROR cannot be paired again, and null means no status yet."`
	StatusChangedAt *time.Time              `json:"status_changed_at" doc:"When Calyx saw status change, RFC 3339 in UTC. null when status is null."`
	StatusStale     bool                    `json:"status_stale" doc:"True when Calyx cannot check the instances now, so status can be out of date."`
	DeviceIdentity  *string                 `json:"device_identity" doc:"What the phone shows in its linked devices: CHROME_WINDOWS, EDGE_WINDOWS, FIREFOX_WINDOWS, FIREFOX_LINUX or SAFARI_MAC_OS. null for META_CLOUD, or when it is not known."`
	Locale          *string                 `json:"locale" doc:"The language and the country that WhatsApp gets, for example es-ES. null for META_CLOUD, or when none is stored."`
	PhoneNumberID   *string                 `json:"phone_number_id" doc:"The phone number id that Meta gave. null for WHATSAPP_WEB."`
	Webhooks        []webhookJSON           `json:"webhooks" doc:"The webhooks of the instance, at most one per event. Empty when there are none."`
	Alert           *alertJSON              `json:"alert" doc:"The open alert of the instance. null when there is none."`
}

// statusPtr converts the stored status, which is nil until the watcher reads one.
func statusPtr(s *string) *nullableInstanceStatus {
	if s == nil {
		return nil
	}
	status := nullableInstanceStatus(*s)
	return &status
}

func instanceResource(v instances.View) instanceJSON {
	out := instanceJSON{
		ID: v.ID, ClientID: v.ClientID, DisplayName: v.DisplayName, Provider: instanceProvider(v.Provider),
		SendOnly: v.SendOnly, Enabled: v.Enabled, State: instanceState(v.State),
		Status: statusPtr(v.LastStatus), StatusChangedAt: utcPtr(v.LastStatusAt), StatusStale: v.StatusStale,
		DeviceIdentity: v.DeviceIdentity, Locale: v.Locale, PhoneNumberID: v.PhoneNumberID,
		Webhooks: make([]webhookJSON, 0, len(v.Webhooks)),
	}
	for _, w := range v.Webhooks {
		out.Webhooks = append(out.Webhooks, webhookJSON{URL: w.URL, Event: w.Event, HasSecret: w.HasSecret})
	}
	if v.AlertKind != nil {
		out.Alert = &alertJSON{Kind: *v.AlertKind, Since: utcPtr(v.AlertOpenedAt), EmailedAt: utcPtr(v.AlertedAt)}
	}
	return out
}

// pairingJSON is the PairingUpdate resource.
type pairingJSON struct {
	Status      instanceStatus `json:"status" doc:"NEEDS_AUTH: use pairing_code or qr_text to pair. CONNECTING means wait for the next update, and CONNECTED means that the pairing is finished."`
	PairingCode *string        `json:"pairing_code" doc:"The code to type on the phone, for example ABCD-2345. null in a QR pairing, or when there is no code."`
	QRText      *string        `json:"qr_text" doc:"The text to draw as a QR code that the phone scans; a newer update replaces it. null in a pairing by code, or when there is no QR code."`
	Message     string         `json:"message" doc:"A sentence for people. It can be empty."`
}

func pairingResource(u instances.PairingUpdate) pairingJSON {
	return pairingJSON{Status: instanceStatus(u.Status), PairingCode: u.PairingCode, QRText: u.QRText, Message: u.Message}
}

type bulkJobJSON struct {
	ID                string        `json:"id" doc:"The id of the job, a UUID."`
	InstanceID        uuid.UUID     `json:"instance_id" doc:"The instance that sends the messages, a UUID."`
	Status            bulkJobStatus `json:"status" doc:"QUEUED means that the job has not started, for example because another job of the instance runs. COMPLETED means that every message was tried, and FAILED that the job stopped, for example because the instance got NEEDS_AUTH."`
	TotalMessages     int32         `json:"total_messages" doc:"The number of messages in the job."`
	SentMessages      int32         `json:"sent_messages" doc:"The number of messages that WhatsApp accepted."`
	FailedMessages    int32         `json:"failed_messages" doc:"The number of messages that failed or have an unknown result. The job does not try them again."`
	ProgressPercent   float32       `json:"progress_percent" doc:"The share of the messages that are sent or failed, from 0 to 100."`
	EstimatedFinishAt *time.Time    `json:"estimated_finish_at" doc:"When the job will probably finish, RFC 3339 in UTC. null when status is not RUNNING."`
	CreatedAt         time.Time     `json:"created_at" doc:"When the job was created, RFC 3339 in UTC."`
	Replayed          bool          `json:"replayed" doc:"True only in the answer to the request that creates the job, when an earlier request with the same Idempotency-Key created it."`
}

func bulkJobResource(j bulk.Job) bulkJobJSON {
	return bulkJobJSON{
		ID: j.ID, InstanceID: j.InstanceID, Status: bulkJobStatus(j.Status), TotalMessages: j.Total,
		SentMessages: j.Sent, FailedMessages: j.Failed, ProgressPercent: j.ProgressPercent,
		EstimatedFinishAt: utcPtr(j.EstimatedFinishAt), CreatedAt: utc(j.CreatedAt),
		Replayed: j.Replayed,
	}
}

type apiKeyJSON struct {
	ID         uuid.UUID  `json:"id" doc:"The id of the API key, a UUID."`
	Name       string     `json:"name" doc:"The name of the API key."`
	Prefix     string     `json:"prefix" doc:"The first 8 characters of the key after cx_, to recognize it."`
	CreatedAt  time.Time  `json:"created_at" doc:"When the key was created, RFC 3339 in UTC."`
	LastUsedAt *time.Time `json:"last_used_at" doc:"When the key was last used, RFC 3339 in UTC, updated at most once per minute. null when it was never used."`
	Token      string     `json:"token,omitempty" doc:"The whole key. Present only in the answer that creates it."`
}

// apiKeyResource maps a key. token is the plain token on creation, and "" otherwise.
func apiKeyResource(k gen.ApiKey, token string) apiKeyJSON {
	return apiKeyJSON{ID: k.ID, Name: k.Name, Prefix: k.Prefix, CreatedAt: utc(k.CreatedAt), LastUsedAt: utcPtr(k.LastUsedAt), Token: token}
}

// itemsJSON is the body of a list answer. The operations never pass a nil list, so the list is
// [] and never null.
type itemsJSON[T any] struct {
	Items []T `json:"items" doc:"The items of the list. Empty when there are none."`
}
