package httpapi

import (
	"fmt"
	"math"

	"github.com/danielgtaylor/huma/v2"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/auth"
	"calyx/backend/internal/bulk"
	"calyx/backend/internal/core"
	"calyx/backend/internal/instances"
	"calyx/backend/internal/tenancy"
)

// The limited fields of the request bodies. Each type writes its limit into the description of
// its schema, from the constant of the service that checks it. The schema sets no maxLength or
// minLength: Huma would refuse the value first, with its own message. A field of one of these
// types must not have a doc tag: the tag replaces the description that the type wrote.

// describeLimit sets the description of s to the limit of the field.
func describeLimit(s *huma.Schema, limit string) *huma.Schema {
	s.Description = limit
	return s
}

// apiKeyName is the name of an API key.
type apiKeyName string

func (apiKeyName) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return describeLimit(s, fmt.Sprintf("1 to %d characters; spaces at the start and end are not counted.",
		auth.MaxAPIKeyNameLength))
}

// emailAddress is an email address that the service checks.
type emailAddress string

func (emailAddress) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return describeLimit(s, fmt.Sprintf("An email address. At most %d bytes; spaces at the start and end are not counted.",
		auth.MaxEmailLength))
}

// newPassword is a password that a user sets.
type newPassword string

func (newPassword) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return describeLimit(s, fmt.Sprintf("%d to %d characters.", auth.MinPasswordLength, auth.MaxPasswordLength))
}

// tenantName is the name of an organization or of a client.
type tenantName string

func (tenantName) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return describeLimit(s, fmt.Sprintf("1 to %d characters; spaces at the start and end are not counted.",
		tenancy.MaxNameLength))
}

// maxInstances is the instance limit of a client. Huma gives an int the format int64, which is
// not a shape keyword, so the type removes it.
type maxInstances int

func (maxInstances) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Format = ""
	return describeLimit(s, fmt.Sprintf("0 to %d. 0 means no limit of its own.", tenancy.MaxInstancesLimit))
}

// displayName is the display name of an instance.
type displayName string

func (displayName) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return describeLimit(s, fmt.Sprintf("1 to %d characters; spaces at the start and end are not counted.",
		instances.MaxDisplayNameLength))
}

// deviceIdentity is the browser and the system that the phone shows for a linked device. The
// list comes from the enum of the core, so no copy of it can go out of date.
type deviceIdentity string

func (deviceIdentity) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Enum = nil
	for _, name := range core.DeviceIdentities() {
		s.Enum = append(s.Enum, name)
	}
	return describeLimit(s, "WHATSAPP_WEB only. What the phone shows in its list of linked devices. "+
		"Without it, the core picks one at random, also at each new pairing. It does not change the risk of a ban.")
}

// localeTag is the locale that a WhatsApp Web instance sends to WhatsApp.
type localeTag string

func (localeTag) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return describeLimit(s, "WHATSAPP_WEB only. 5 characters: two lower-case letters, a hyphen, two upper-case "+
		"letters, for example es-ES. A new WHATSAPP_WEB instance needs it; a new pairing without it keeps "+
		"the stored one. It does not change the risk of a ban.")
}

// metaPhoneNumberID is the phone number id of a Meta Cloud API number.
type metaPhoneNumberID string

func (metaPhoneNumberID) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return describeLimit(s, fmt.Sprintf("1 to %d digits, no other characters; spaces at the start and end are not counted.",
		instances.MaxPhoneNumberIDLength))
}

// metaAccessToken is the access token of a Meta Cloud API number.
type metaAccessToken string

func (metaAccessToken) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return describeLimit(s, fmt.Sprintf("1 to %d bytes; spaces at the start and end are not counted.",
		instances.MaxAccessTokenLength))
}

// metaAppSecret is the app secret of a Meta Cloud API number.
type metaAppSecret string

func (metaAppSecret) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return describeLimit(s, fmt.Sprintf("1 to %d bytes; spaces at the start and end are not counted.",
		instances.MaxSecretLength))
}

// webhookURL is the URL of a webhook.
type webhookURL string

func (webhookURL) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return describeLimit(s, fmt.Sprintf("At most %d bytes. It starts with http:// or https:// and has a host.",
		instances.MaxURLLength))
}

// webhookSecret is the secret that signs the deliveries of a webhook.
type webhookSecret string

func (webhookSecret) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return describeLimit(s, fmt.Sprintf("At most %d bytes. On create: 1 to %d bytes, absent or null. On PATCH: absent "+
		"or null keeps the stored secret only when the event and the URL do not change, an empty string removes "+
		"it, another value replaces it.",
		instances.MaxSecretLength, instances.MaxSecretLength))
}

// webhookList is the webhook list of an instance. The count is a service rule, so the schema
// sets no maxItems.
type webhookList []webhookInput

func (webhookList) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return describeLimit(s, fmt.Sprintf("At most %d webhooks, one per event. An instance used only for sending "+
		"has none.", instances.MaxWebhooks))
}

// idempotencyKey is the Idempotency-Key header. Its description is the only text that a client or
// an agent reads for the key, so it holds the whole guidance.
type idempotencyKey string

func (idempotencyKey) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return describeLimit(s, fmt.Sprintf("1 to %d printable ASCII characters. Make a new key for each new "+
		"message, for example a UUID. Use the same key only to retry the same request after an answer "+
		"that did not arrive. When an answer has a `Retry-After` header, wait for that time, then retry with "+
		"the same key. After a 504, retry with the same key. The key is kept for 24 hours. All callers of one instance share its keys.",
		instances.MaxIdempotencyKeyLength))
}

// recipient is the recipient of a message.
type recipient string

func (recipient) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return describeLimit(s, fmt.Sprintf("Not empty. At most %d bytes. The core checks the format.",
		instances.MaxRecipientLength))
}

// messageText is the text of a message. The service sends it unchanged.
type messageText string

func (messageText) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return describeLimit(s, fmt.Sprintf("At most %d characters, and not only spaces. The text is sent unchanged.",
		instances.MaxTextLength))
}

// recipientList is the recipient list of a bulk job with one text. The count is a service rule,
// so the schema sets no minItems or maxItems.
type recipientList []recipient

func (recipientList) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return describeLimit(s, fmt.Sprintf("1 to %d recipients. Use it with text, and without messages. More than "+
		"%d gives 413 too_many_messages.", bulk.MaxMessages, bulk.MaxMessages))
}

// bulkMessageList is the message list of a bulk job with one text per message.
type bulkMessageList []bulkMessageInput

func (bulkMessageList) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return describeLimit(s, fmt.Sprintf("1 to %d messages. Use it without recipients and text. More than %d "+
		"gives 413 too_many_messages.", bulk.MaxMessages, bulk.MaxMessages))
}

// delayMs is the delay before one message of a bulk job. It is an int64 so that every JSON
// integer decodes, and the operation refuses a value outside the int32 range at the path of the
// field: Huma reports a decode error at "body" only. Huma gives an int64 the format int64, which
// is not a shape keyword, so the type removes it.
type delayMs int64

func (delayMs) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Format = ""
	return describeLimit(s, fmt.Sprintf("0 to %d milliseconds. A fixed time to wait before this message, instead of a random delay of the pacing. The core refuses a value under its minimum.", math.MaxInt32))
}

// pacingBound is a bound of a pacing range: milliseconds for delay and pause_duration, a count
// of messages for pause_every. It is an int64 for the reason of delayMs.
type pacingBound int64

func (pacingBound) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Format = ""
	return describeLimit(s, fmt.Sprintf("0 to %d. Milliseconds for delay and pause_duration, a count of messages "+
		"for pause_every. The core refuses a value under its minimum.", math.MaxInt32))
}

// enumOf sets the enum of s to values and returns s. The type of the field keeps its own shape.
func enumOf(s *huma.Schema, values []string) *huma.Schema {
	s.Enum = nil
	for _, v := range values {
		s.Enum = append(s.Enum, v)
	}
	return s
}

// The closed fields of the answers. Each type takes its list from the core or the code that
// writes the value, so no copy of it can go out of date. They are not used in request bodies.

// instanceProvider is the provider of an instance.
type instanceProvider string

func (instanceProvider) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return enumOf(s, core.Providers())
}

// instanceStates are the values of the state of an instance record. They equal the check of the
// column in 0001_initial.sql.
var instanceStates = []string{"creating", "active", "removing", "missing"}

// instanceState is the state of an instance record.
type instanceState string

func (instanceState) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return enumOf(s, instanceStates)
}

// instanceStatus is the connection status of an instance.
type instanceStatus string

func (instanceStatus) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return enumOf(s, core.InstanceStatuses())
}

// nullableInstanceStatus is the status of an instance that the watcher has not read yet. The
// pointer makes the schema nullable, so the enum needs the null value too.
type nullableInstanceStatus string

func (nullableInstanceStatus) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	enumOf(s, core.InstanceStatuses())
	s.Enum = append(s.Enum, nil)
	return s
}

// bulkJobStatus is the status of a bulk job.
type bulkJobStatus string

func (bulkJobStatus) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return enumOf(s, core.BulkJobStatuses())
}

// pairingFailureCode is the code of the SSE "failed" event.
type pairingFailureCode string

func (pairingFailureCode) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	return enumOf(s, apierr.PairingFailureCodes())
}
