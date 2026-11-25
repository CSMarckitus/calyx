package httpapi

import (
	"context"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/google/uuid"

	"calyx/backend/internal/core"
	"calyx/backend/internal/instances"
	"calyx/backend/internal/tenancy"
)

// sendDeadline bounds a send. SendPlainText has no deadline of its own: it waits for the core's
// pacing. The slowest normal send on WhatsApp Web is 20 x (10 s + 2 s) = 4 minutes, so this value
// must never be shorter. It stops a stuck core, and a client without a key that waits too long.
// When it ends, the core removes a waiting send from the queue.
const sendDeadline = 5 * time.Minute

type metaCredentialsInput struct {
	PhoneNumberID metaPhoneNumberID `json:"phone_number_id"`
	AccessToken   metaAccessToken   `json:"access_token"`
	AppSecret     metaAppSecret     `json:"app_secret"`
}

// credentials returns the core type. The instances service checks the fields.
func (m *metaCredentialsInput) credentials() *core.MetaCredentials {
	if m == nil {
		return nil
	}
	return &core.MetaCredentials{
		PhoneNumberID: string(m.PhoneNumberID), AccessToken: string(m.AccessToken), AppSecret: string(m.AppSecret),
	}
}

type webhookInput struct {
	URL    webhookURL     `json:"url"`
	Event  string         `json:"event" doc:"The event of the webhook. The supported events depend on the provider."`
	Secret *webhookSecret `json:"secret,omitempty" nullable:"true"`
}

// inputs returns the service type of a webhook list. The instances service checks it.
func (l webhookList) inputs() []instances.WebhookInput {
	out := make([]instances.WebhookInput, 0, len(l))
	for _, h := range l {
		w := instances.WebhookInput{URL: string(h.URL), Event: h.Event}
		if h.Secret != nil {
			secret := string(*h.Secret)
			w.Secret = &secret
		}
		out = append(out, w)
	}
	return out
}

type createInstanceInput struct {
	Body struct {
		ClientID    *string               `json:"client_id,omitempty" nullable:"true" doc:"A UUID. A client user creates for its own client; an owner creates for the default client when the field is absent."`
		DisplayName displayName           `json:"display_name"`
		Provider    string                `json:"provider" enum:"WHATSAPP_WEB,META_CLOUD" doc:"WHATSAPP_WEB (unofficial, paired with a phone) or META_CLOUD (the official Meta Cloud API)."`
		SendOnly    *bool                 `json:"send_only,omitempty" nullable:"true" doc:"WHATSAPP_WEB only. The default is false."`
		DeviceID    *deviceIdentity       `json:"device_identity,omitempty" nullable:"true"`
		Locale      *localeTag            `json:"locale,omitempty" nullable:"true"`
		Pairing     *string               `json:"pairing,omitempty" nullable:"true" doc:"WHATSAPP_WEB only: qr or code. The default is qr."`
		Phone       *string               `json:"phone,omitempty" nullable:"true" doc:"Required when pairing is code: 7 to 15 digits, no other characters."`
		Meta        *metaCredentialsInput `json:"meta_credentials,omitempty" doc:"Required for META_CLOUD, and only for META_CLOUD."`
		Webhooks    webhookList           `json:"webhooks,omitempty" nullable:"true"`
	}
}

type updateInstanceInput struct {
	ID   uuid.UUID `path:"id" format:"uuid"`
	Body struct {
		DisplayName *displayName          `json:"display_name,omitempty" nullable:"true"`
		Enabled     *bool                 `json:"enabled,omitempty" nullable:"true" doc:"False disables the instance, so it cannot send; true enables it again. Absent or null keeps the stored value."`
		SendOnly    *bool                 `json:"send_only,omitempty" nullable:"true" doc:"WHATSAPP_WEB only: true needs an instance with no webhooks. Absent or null keeps the stored value."`
		Webhooks    *webhookList          `json:"webhooks,omitempty" nullable:"true"`
		Meta        *metaCredentialsInput `json:"meta_credentials,omitempty" doc:"The three fields together replace the stored credentials."`
	}
}

type pairInstanceInput struct {
	ID   uuid.UUID `path:"id" format:"uuid"`
	Body struct {
		Pairing  *string         `json:"pairing,omitempty" nullable:"true" doc:"qr or code. The default is qr."`
		Phone    *string         `json:"phone,omitempty" nullable:"true" doc:"Required when pairing is code: 7 to 15 digits, no other characters."`
		DeviceID *deviceIdentity `json:"device_identity,omitempty" nullable:"true"`
		Locale   *localeTag      `json:"locale,omitempty" nullable:"true"`
	}
}

type sendMessageInput struct {
	ID             uuid.UUID      `path:"id" format:"uuid"`
	IdempotencyKey idempotencyKey `header:"Idempotency-Key"`
	Body           struct {
		To   recipient   `json:"to"`
		Text messageText `json:"text"`
	}
}

type createInstanceResult struct {
	Instance instanceJSON `json:"instance" doc:"The new instance."`
	Pairing  pairingJSON  `json:"pairing" doc:"The first update of the pairing. When its status is not CONNECTED, get the next ones from the pairing stream."`
}

type pairResult struct {
	Pairing pairingJSON `json:"pairing" doc:"The first update of the pairing. When its status is not CONNECTED, get the next ones from the pairing stream."`
}

type sendMessageResult struct {
	MessageID string `json:"message_id" doc:"The id of the message in WhatsApp. The message.status events have the same id."`
	Replayed  bool   `json:"replayed" doc:"True when an earlier request with the same Idempotency-Key sent the message, so this request sent nothing."`
}

type instanceListOutput struct{ Body itemsJSON[instanceJSON] }

type instanceOutput struct{ Body instanceJSON }

type createInstanceOutput struct{ Body createInstanceResult }

type pairOutput struct{ Body pairResult }

type sendMessageOutput struct{ Body sendMessageResult }

// removeInstanceOutput has no body: the status is 204 or 202.
type removeInstanceOutput struct{ Status int }

// pairingStreamDescription is the description of the 200 answer of the pairing stream.
const pairingStreamDescription = "Events: update (data: a PairingUpdate), done (data: {\"status\":\"CONNECTED\"}), " +
	"failed (data: {\"code\":\"pairing_expired|pairing_failed|pairing_cancelled\",\"detail\":\"...\"}). " +
	"A comment line \": ping\" comes every 15 seconds. The stream ends after done or failed. A stream that ends " +
	"without one of them means the server stopped: reconnect, or read the instance with GET /instances/{id}."

// createInstanceExamples are the named request examples of createInstance, one for each provider.
var createInstanceExamples = map[string]*huma.Example{
	"WhatsApp Web": {Value: map[string]any{
		"display_name": "Shop", "provider": "WHATSAPP_WEB", "locale": "es-ES", "pairing": "qr",
	}},
	"Meta Cloud API": {Value: map[string]any{
		"display_name": "Support", "provider": "META_CLOUD", "meta_credentials": map[string]any{
			"phone_number_id": "123456789012345", "access_token": "<access token>", "app_secret": "<app secret>",
		},
	}},
}

// pairingEvents maps each event name of the pairing stream to the type of its data.
var pairingEvents = map[string]any{"update": pairingJSON{}, "done": doneJSON{}, "failed": failedJSON{}}

// withEventStream documents the 200 answer of op as text/event-stream with one schema per event,
// in the form of addEventStreamResponse of Huma's sse package. sse.Register is not used: it
// writes its own headers and events, and the stream must keep the ones of startSSE.
func withEventStream(api huma.API, op huma.Operation, description string, events map[string]any) huma.Operation {
	var schemas []*huma.Schema
	for name, v := range events {
		schemas = append(schemas, &huma.Schema{
			Title: "Event " + name,
			Type:  huma.TypeObject,
			Properties: map[string]*huma.Schema{
				"event": {Type: huma.TypeString, Description: "The event name.", Extensions: map[string]any{"const": name}},
				"data":  api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(v), true, name),
			},
			Required: []string{"data", "event"},
		})
	}
	slices.SortFunc(schemas, func(a, b *huma.Schema) int { return strings.Compare(a.Title, b.Title) })
	op.Responses = map[string]*huma.Response{"200": {
		Description: description,
		Content: map[string]*huma.MediaType{"text/event-stream": {Schema: &huma.Schema{
			Title: "Server Sent Events",
			Description: "Each oneOf object in the array represents one possible Server Sent Events (SSE) message, " +
				"serialized as UTF-8 text according to the SSE specification.",
			Type:  huma.TypeArray,
			Items: &huma.Schema{Extensions: map[string]any{"oneOf": schemas}},
		}}},
	}}
	return op
}

func (s *server) registerInstanceOps(api huma.API) {
	const v1 = "/api/v1"
	huma.Register(api, withAccess(asTool(huma.Operation{
		OperationID: "listInstances", Tags: []string{"Instances"}, Method: http.MethodGet, Path: v1 + "/instances",
		Summary:     "List the instances in the scope of the caller.",
		Description: "Errors: 401 unauthenticated.",
	}, "list_instances", toolListInstances), accessLoggedIn, tenancy.UseInstances), s.listInstances)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "createInstance", Tags: []string{"Instances"}, Method: http.MethodPost, Path: v1 + "/instances",
		Summary: "Create an instance and start its pairing.", DefaultStatus: http.StatusCreated,
		Description: "A client user can create an instance only when the owner allows it for the client. " +
			"Errors: 400 invalid_request, 401 unauthenticated, 403 instance_creation_not_allowed, " +
			"403 whatsapp_web_not_allowed, 403 whatsapp_web_not_acknowledged, 403 csrf_check_failed, 404 not_found, " +
			"409 quota_exceeded, 409 phone_number_id_in_use.",
		// Huma adds the schema to this media type and keeps the examples.
		RequestBody: &huma.RequestBody{Content: map[string]*huma.MediaType{"application/json": {
			Examples: createInstanceExamples,
		}}},
		Responses: map[string]*huma.Response{"201": {Description: "The instance and the first pairing update. " +
			"The pairing goes on after the answer: follow it with GET /instances/{id}/pairing."}},
	}, accessLoggedIn, tenancy.CreateInstance), s.createInstance)
	huma.Register(api, withAccess(asTool(huma.Operation{
		OperationID: "getInstance", Tags: []string{"Instances"}, Method: http.MethodGet, Path: v1 + "/instances/{id}",
		Summary:     "Read an instance.",
		Description: "Errors: 401 unauthenticated, 404 not_found.",
	}, "get_instance", toolGetInstance), accessLoggedIn, tenancy.UseInstances), s.getInstance)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "updateInstance", Tags: []string{"Instances"}, Method: http.MethodPatch, Path: v1 + "/instances/{id}",
		Summary: "Change an instance. Every field is optional.",
		Description: "Errors: 400 invalid_request, 401 unauthenticated, 403 csrf_check_failed, 404 not_found, " +
			"409 conflict (another change of the instance is running), 409 pairing_in_progress, 504 change_outcome_unknown.",
	}, accessLoggedIn, tenancy.UseInstances), s.updateInstance)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "removeInstance", Tags: []string{"Instances"}, Method: http.MethodDelete, Path: v1 + "/instances/{id}",
		Summary: "Remove an instance.", DefaultStatus: http.StatusNoContent,
		Description: "Errors: 401 unauthenticated, 403 csrf_check_failed, 404 not_found, 409 pairing_in_progress.",
		Responses: map[string]*huma.Response{
			"204": {Description: "The instance is removed."},
			"202": {Description: "The core did not confirm. The instance stays in the state removing and the " +
				"backend retries. There is no body."},
		},
	}, accessLoggedIn, tenancy.UseInstances), s.removeInstance)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "pairInstance", Tags: []string{"Instances"}, Method: http.MethodPost, Path: v1 + "/instances/{id}/pair",
		Summary: "Start a new pairing session for an instance that needs it.", DefaultStatus: http.StatusAccepted,
		Description: "Errors: 400 invalid_request, 401 unauthenticated, 403 whatsapp_web_not_allowed, " +
			"403 csrf_check_failed, 404 not_found, 409 conflict (the instance is disabled or another change of it is " +
			"running), 409 pairing_not_supported, 409 instance_in_error, 409 instance_connected, 409 pairing_in_progress.",
		Responses: map[string]*huma.Response{"202": {Description: "The first update of the session."}},
	}, accessLoggedIn, tenancy.UseInstances), s.pairInstance)
	huma.Register(api, withAccess(withEventStream(api, huma.Operation{
		OperationID: "followPairing", Tags: []string{"Instances"}, Method: http.MethodGet, Path: v1 + "/instances/{id}/pairing",
		Summary: "Follow the pairing session of an instance as a stream of server-sent events.",
		Description: "Errors: 401 unauthenticated, 404 not_found (no session runs and the backend keeps no failure). " +
			"For 5 minutes after a pairing failed the stream gives the event failed and ends. The 404 answer is a " +
			"problem, sent before any stream header.",
	}, pairingStreamDescription, pairingEvents), accessLoggedIn, tenancy.UseInstances), s.followPairing)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "cancelPairing", Tags: []string{"Instances"}, Method: http.MethodDelete, Path: v1 + "/instances/{id}/pairing",
		Summary: "Cancel the running pairing session.", DefaultStatus: http.StatusNoContent,
		Description: "Errors: 401 unauthenticated, 403 csrf_check_failed, 404 not_found.",
	}, accessLoggedIn, tenancy.UseInstances), s.cancelPairing)
	huma.Register(api, withAccess(asTool(huma.Operation{
		OperationID: "sendMessage", Tags: []string{"Messages"}, Method: http.MethodPost, Path: v1 + "/instances/{id}/messages",
		Summary: "Send one text message.",
		Description: "Errors: 400 invalid_request, 401 unauthenticated, 403 csrf_check_failed, 404 not_found, " +
			"409 instance_not_connected, 409 instance_not_ready, 409 idempotency_key_conflict, 409 send_in_progress, " +
			"409 idempotency_key_outcome_unknown, 409 duplicate_message, 429 instance_queue_full, 502 core_rejected_token, " +
			"504 send_outcome_unknown. A 504 send_outcome_unknown means the message may or may not have been " +
			"sent. Send an Idempotency-Key header. After a 504, retry with the same key: the core sends the " +
			"message at most once. After 429 instance_queue_full or 409 send_in_progress, wait for the time in " +
			"Retry-After, then retry with the same key. Without a key, do not retry automatically. " +
			"A resend with a known Idempotency-Key gets its answer also while the instance is not connected.",
		Responses: map[string]*huma.Response{"200": {Description: "Sent."}},
	}, "send_message", toolSendMessage), accessLoggedIn, tenancy.UseInstances), s.sendMessage)
}

// GET /instances
func (s *server) listInstances(ctx context.Context, _ *struct{}) (*instanceListOutput, error) {
	views, err := s.Instances.List(ctx, principalOf(ctx))
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	items := make([]instanceJSON, 0, len(views))
	for _, v := range views {
		items = append(items, instanceResource(v))
	}
	return &instanceListOutput{Body: itemsJSON[instanceJSON]{Items: items}}, nil
}

// POST /instances. client_id has no UUID format in the schema: the service checks it.
func (s *server) createInstance(ctx context.Context, in *createInstanceInput) (*createInstanceOutput, error) {
	b := in.Body
	ci := instances.CreateInput{
		DisplayName: string(b.DisplayName), Provider: b.Provider, Meta: b.Meta.credentials(), Webhooks: b.Webhooks.inputs(),
	}
	if b.ClientID != nil {
		ci.ClientID = *b.ClientID
	}
	if b.SendOnly != nil {
		ci.SendOnly = *b.SendOnly
	}
	ci.DeviceIdentity, ci.Locale = optionalString(b.DeviceID), optionalString(b.Locale)
	if b.Pairing != nil {
		ci.Pairing = *b.Pairing
	}
	if b.Phone != nil {
		ci.Phone = *b.Phone
	}
	view, upd, err := s.Instances.Create(ctx, principalOf(ctx), ci)
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	return &createInstanceOutput{Body: createInstanceResult{Instance: instanceResource(view), Pairing: pairingResource(upd)}}, nil
}

// GET /instances/{id}
func (s *server) getInstance(ctx context.Context, in *idInput) (*instanceOutput, error) {
	v, err := s.Instances.Get(ctx, principalOf(ctx), in.ID)
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	return &instanceOutput{Body: instanceResource(v)}, nil
}

// PATCH /instances/{id}. A webhook list is the whole new list: a secret that is absent keeps the
// stored one, "" removes it.
func (s *server) updateInstance(ctx context.Context, in *updateInstanceInput) (*instanceOutput, error) {
	b := in.Body
	ch := instances.Change{Enabled: b.Enabled, SendOnly: b.SendOnly, Meta: b.Meta.credentials()}
	if b.DisplayName != nil {
		d := string(*b.DisplayName)
		ch.DisplayName = &d
	}
	if b.Webhooks != nil {
		hooks := b.Webhooks.inputs()
		ch.Webhooks = &hooks
	}
	v, err := s.Instances.Update(ctx, principalOf(ctx), in.ID, ch)
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	return &instanceOutput{Body: instanceResource(v)}, nil
}

// DELETE /instances/{id}: 204 when removed, 202 with no body while the removal is retried.
func (s *server) removeInstance(ctx context.Context, in *idInput) (*removeInstanceOutput, error) {
	removed, err := s.Instances.Remove(ctx, principalOf(ctx), in.ID)
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	if removed {
		return &removeInstanceOutput{Status: http.StatusNoContent}, nil
	}
	return &removeInstanceOutput{Status: http.StatusAccepted}, nil
}

// POST /instances/{id}/pair
func (s *server) pairInstance(ctx context.Context, in *pairInstanceInput) (*pairOutput, error) {
	pin := instances.PairInput{
		DeviceIdentity: optionalString(in.Body.DeviceID), Locale: optionalString(in.Body.Locale),
	}
	if in.Body.Pairing != nil {
		pin.Pairing = *in.Body.Pairing
	}
	if in.Body.Phone != nil {
		pin.Phone = *in.Body.Phone
	}
	upd, err := s.Instances.Pair(ctx, principalOf(ctx), in.ID, pin)
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	return &pairOutput{Body: pairResult{Pairing: pairingResource(upd)}}, nil
}

// GET /instances/{id}/pairing: the SSE stream. FollowPairing runs before the stream starts, so
// its error is still a problem answer, before any SSE header.
func (s *server) followPairing(ctx context.Context, in *idInput) (*huma.StreamResponse, error) {
	allowed := s.streamAccess(ctx)
	events, err := s.Instances.FollowPairing(ctx, principalOf(ctx), in.ID)
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	return &huma.StreamResponse{Body: func(hctx huma.Context) {
		_, w := humago.Unwrap(hctx)
		sse, err := startSSE(w)
		if err != nil {
			s.log.Error("The response writer cannot flush.", "request_id", requestIDOf(hctx.Context()), "error", err)
			return
		}
		streamPairing(hctx.Context(), sse, events, allowed, s.Clock)
	}}, nil
}

// DELETE /instances/{id}/pairing
func (s *server) cancelPairing(ctx context.Context, in *idInput) (*struct{}, error) {
	return nil, serviceError(ctx, s.Instances.CancelPairing(ctx, principalOf(ctx), in.ID))
}

// POST /instances/{id}/messages. Nothing of the request goes to the log.
func (s *server) sendMessage(ctx context.Context, in *sendMessageInput) (*sendMessageOutput, error) {
	key := string(in.IdempotencyKey)
	parent := ctx
	if key != "" {
		// A client with a key can learn the result of a send that it left, so the core call
		// goes on when the client stops waiting. Without a key, the call stops with the client.
		parent = context.WithoutCancel(ctx)
	}
	sendCtx, cancel := context.WithTimeout(parent, sendDeadline)
	defer cancel()
	res, err := s.Instances.Send(sendCtx, principalOf(ctx), in.ID, string(in.Body.To), string(in.Body.Text), key)
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	return &sendMessageOutput{Body: sendMessageResult{MessageID: res.MessageID, Replayed: res.Replayed}}, nil
}

// optionalString converts an optional field of a string type to a *string.
func optionalString[T ~string](v *T) *string {
	if v == nil {
		return nil
	}
	s := string(*v)
	return &s
}
