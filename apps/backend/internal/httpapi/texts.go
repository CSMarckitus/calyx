package httpapi

import (
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// The texts of the document that no doc tag can hold. The client SDKs show them in the IDE.

// pathTexts holds the text of the path parameter by the first part of the path: one Go type serves
// many operations.
var pathTexts = map[string]string{
	"instances": "The id of the instance, a UUID. Any other value gives 404 not_found.",
	"clients":   "The id of the client, a UUID. Any other value gives 404 not_found.",
	"bulk-jobs": "The id of the bulk job, a UUID. Any other value gives 404 not_found.",
	"api-keys":  "The id of the API key, a UUID, not the key itself. Any other value gives 404 not_found.",
}

// describePathParams writes the text of the path parameters of op.
func describePathParams(_ *huma.OpenAPI, op *huma.Operation) {
	part, _, _ := strings.Cut(strings.TrimPrefix(op.Path, "/api/v1/"), "/")
	for _, p := range op.Parameters {
		if p.In == "path" && p.Name == "id" {
			p.Description = pathTexts[part]
		}
	}
}

// schemaTexts holds the text of each named schema.
var schemaTexts = map[string]string{
	"AcknowledgementRequest":     "The acceptance of the WhatsApp Web terms.",
	"Alert":                      "A problem of an instance that needs attention.",
	"ApiKey":                     "An API key of a user. A program sends it as a Bearer token.",
	"ApiKeyList":                 "The active API keys of the user, newest first.",
	"BulkJob":                    "A bulk job: many messages that one instance sends in the background, with delays between them.",
	"BulkJobList":                "The latest bulk jobs of an instance, newest first, at most 50.",
	"BulkMessageInput":           "One message of a bulk job.",
	"BulkPacingInput":            "The pacing of a bulk job: an absent field gets the default of the provider, and a value under its minimum is refused. META_CLOUD takes no pauses and ignores pause_every and pause_duration.",
	"ChangePasswordRequest":      "The current password and the new one.",
	"Changed":                    "The data of the event changed: always empty. Read the screen again.",
	"Client":                     "A client of the organization. Its instances are apart from those of the other clients.",
	"ClientList":                 "The clients of the organization: the default client, then the others, oldest first.",
	"CreateAPIKeyRequest":        "A new API key.",
	"CreateBulkJobRequest":       "A new bulk job: recipients with one text, or messages with their own texts.",
	"CreateClientRequest":        "A new client.",
	"CreateInstanceRequest":      "A new instance. WHATSAPP_WEB needs locale, and META_CLOUD needs meta_credentials.",
	"CreateInstanceResult":       "A new instance and the first update of its pairing.",
	"Done":                       "The data of the event done: the pairing is finished and the instance is connected.",
	"EmailRequest":               "The email address of the user.",
	"Failed":                     "The data of the event failed: the pairing ended and the instance is not connected.",
	"Instance":                   "One WhatsApp number that Calyx manages.",
	"InstanceList":               "The instances that you can use, oldest first.",
	"Invitation":                 "An invitation to the user of a client.",
	"InvitationLookup":           "The invitation of a token: the email and the name of the organization. The token stays valid.",
	"InviteClientUserRequest":    "The person to invite as the user of a client.",
	"LoginRequest":               "The email address and the password of a user.",
	"LookupInvitationRequest":    "The token of an invitation link.",
	"LookupPasswordResetRequest": "The token of a reset link.",
	"Me":                         "The caller: a user and its organization.",
	"MeOrganization":             "The organization of the caller. An owner gets every field; a client user gets only id and name.",
	"MetaCredentialsInput":       "The credentials of a META_CLOUD instance, from the Meta app. All three are required.",
	"MetaWebhook":                "The values that the Meta app needs to deliver the events of a META_CLOUD instance.",
	"Organization":               "The organization: the account that holds the clients and the instances.",
	"PairInstanceRequest":        "How to pair a WHATSAPP_WEB instance again.",
	"PairResult":                 "The start of a new pairing.",
	"Pairing":                    "One update of a pairing. In a QR pairing, use the newest update, because the QR code changes.",
	"PasswordResetLookup":        "The user of a reset token: its email. The token stays valid.",
	"PendingInvitation":          "The invitation of a client that nobody accepted yet. It can be expired.",
	"Problem":                    "An error answer (RFC 9457). In a program, use code.",
	"ProblemField":               "One wrong field of a request.",
	"RangeInput":                 "A range in milliseconds, or in messages for pause_every: Calyx takes random values from min to max. WHATSAPP_WEB refuses equal values, and META_CLOUD ignores pause_every and pause_duration.",
	"Replaced":                   "The data of the event replaced: always empty. Close the stream.",
	"SendMessageRequest":         "One text message to one recipient.",
	"SendMessageResult":          "A message that WhatsApp accepted.",
	"TokenPasswordRequest":       "The token of an invitation link or a reset link, and the new password.",
	"UpdateClientRequest":        "The changes of a client. An absent or null field keeps its value.",
	"UpdateInstanceRequest":      "The changes of an instance. An absent or null field keeps its value.",
	"UpdateOrganizationRequest":  "The changes of the organization. An absent or null field keeps its value.",
	"User":                       "A user: the owner of an organization or the user of a client.",
	"Webhook":                    "A webhook of an instance: the URL that gets one type of event.",
	"WebhookInput":               "A webhook: the URL that gets one type of event.",
}

// describeSchemas writes the schema texts. It runs after the registration of the operations, which
// makes the schemas. A name with no schema is a mistake in the map.
func describeSchemas(api huma.API) {
	schemas := api.OpenAPI().Components.Schemas.Map()
	for name, text := range schemaTexts {
		s, ok := schemas[name]
		if !ok {
			panic("httpapi: schemaTexts has no schema " + name)
		}
		s.Description = text
	}
}
