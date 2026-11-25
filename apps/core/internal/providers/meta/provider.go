// Package meta is the official WhatsApp Business Cloud API provider. There is no socket to keep
// alive: sending is an HTTPS request to Meta, and incoming events arrive at the WebhookServer,
// which hands them to the provider of their phone number.
//
// Meta only delivers free-form text inside the 24 hour window opened by a message from the
// customer. Outside of it Meta requires approved templates, which are not supported yet.
package meta

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/engine"
	"calyx/core/internal/events"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

const (
	// Graph API error code of an expired or revoked access token.
	invalidTokenCode = 190
	requestTimeout   = 15 * time.Second
	// While Meta cannot be reached, how often Connect is tried again in the background.
	retryConnect = 30 * time.Second
)

type Options struct {
	GraphURL string // default https://graph.facebook.com (tests only; not an environment variable)
	Version  string // default v23.0 (META_GRAPH_API_VERSION)
	Client   *http.Client
}

type Provider struct {
	name        string
	credentials engine.MetaCredentials
	hooks       engine.ProviderHooks
	baseURL     string
	client      *http.Client

	// reportMu holds a status report from its check of destroyed to the end of the hook. Destroy
	// takes it to set destroyed: no report starts after that, and none is still in flight.
	// Order: reportMu, then mu.
	reportMu sync.Mutex

	mu        sync.Mutex
	status    corev1.InstanceStatus
	destroyed bool // after Destroy: nothing connects or reports any more
	retry     *time.Timer
}

func New(row engine.InstanceRow, hooks engine.ProviderHooks, opts Options) (*Provider, error) {
	var credentials engine.MetaCredentials
	if err := json.Unmarshal([]byte(row.ProviderConfig), &credentials); err != nil || credentials.PhoneNumberID == "" {
		return nil, calyxerr.New(codes.FailedPrecondition, "Instance %s has no valid Meta Cloud credentials.", row.Name)
	}
	if opts.GraphURL == "" {
		opts.GraphURL = "https://graph.facebook.com"
	}
	if opts.Version == "" {
		opts.Version = "v23.0"
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: requestTimeout}
	}
	return &Provider{
		name:        row.Name,
		credentials: credentials,
		hooks:       hooks,
		baseURL:     strings.TrimSuffix(opts.GraphURL, "/") + "/" + opts.Version,
		client:      opts.Client,
		status:      corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED,
	}, nil
}

func (p *Provider) InstanceName() string        { return p.name }
func (p *Provider) Pacing() engine.PacingPolicy { return engine.OfficialAPIPacing }
func (p *Provider) PhoneNumberID() string       { return p.credentials.PhoneNumberID }

func (p *Provider) Status() corev1.InstanceStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// setStatus records and reports a new status. After Destroy it does nothing: the row of a closed
// instance keeps its status.
func (p *Provider) setStatus(status corev1.InstanceStatus) {
	p.reportMu.Lock()
	defer p.reportMu.Unlock()
	p.mu.Lock()
	if p.destroyed {
		p.mu.Unlock()
		return
	}
	changed := p.status != status
	p.status = status
	p.mu.Unlock()
	if changed && p.hooks.OnStatus != nil {
		p.hooks.OnStatus(status)
	}
}

// Connect checks that the credentials can read the phone number. If Meta cannot be reached it
// answers UNAVAILABLE and keeps checking in the background. After Destroy it does nothing: the retry
// timer can call it just after Destroy.
func (p *Provider) Connect(ctx context.Context) error {
	p.mu.Lock()
	destroyed := p.destroyed
	p.mu.Unlock()
	if destroyed {
		return calyxerr.New(codes.Unavailable, "Instance %s is closed.", p.name)
	}
	p.setStatus(corev1.InstanceStatus_INSTANCE_STATUS_CONNECTING)

	err := p.call(ctx, http.MethodGet, "/"+p.credentials.PhoneNumberID+"?fields=id", nil, nil)
	if err != nil {
		err = p.classify(err, "Could not validate the Meta Cloud credentials")
		if calyxerr.CodeOf(err) == codes.Unavailable {
			p.setStatus(corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED)
			p.retryLater()
		}
		return err
	}
	p.setStatus(corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED)
	return nil
}

func (p *Provider) retryLater() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.destroyed || p.retry != nil {
		return
	}
	p.retry = time.AfterFunc(retryConnect, func() {
		p.mu.Lock()
		p.retry = nil
		p.mu.Unlock()
		_ = p.Connect(context.Background())
	})
}

var (
	nonDigits   = regexp.MustCompile(`[\s()+-]`)
	phoneNumber = regexp.MustCompile(`^\d{7,15}$`)
)

// NormalizeRecipient checks and normalizes a recipient with the function of the package.
func (p *Provider) NormalizeRecipient(to string) (string, error) { return NormalizeRecipient(to) }

// NormalizeRecipient: Meta expects the phone number with country code, digits only. No groups.
func NormalizeRecipient(to string) (string, error) {
	digits := nonDigits.ReplaceAllString(strings.TrimSuffix(strings.TrimSpace(to), "@s.whatsapp.net"), "")
	if phoneNumber.MatchString(digits) {
		return digits, nil
	}
	return "", calyxerr.New(codes.InvalidArgument,
		"'%s' is not a valid recipient for the Meta Cloud API. Use a phone number with country code.", to)
}

func (p *Provider) SendText(ctx context.Context, recipient, text string) (string, error) {
	request := map[string]any{
		"messaging_product": "whatsapp", "recipient_type": "individual", "to": recipient,
		"type": "text", "text": map[string]any{"body": text},
	}
	var response struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := p.call(ctx, http.MethodPost, "/"+p.credentials.PhoneNumberID+"/messages", request, &response); err != nil {
		return "", sendFailure(err, p.classify(err, "Meta rejected the message"))
	}
	if len(response.Messages) == 0 || response.Messages[0].ID == "" {
		return "", errors.New("meta did not return an id for the message")
	}
	return response.Messages[0].ID, nil
}

// sendFailure keeps UNAVAILABLE ("not sent") only when the connection to Meta did not open. Any
// other failure may have reached Meta, so it becomes a plain error: an unknown outcome.
func sendFailure(raw, classified error) error {
	if calyxerr.CodeOf(classified) != codes.Unavailable {
		return classified
	}
	var opErr *net.OpError
	if errors.As(raw, &opErr) && opErr.Op == "dial" {
		return classified
	}
	return errors.New(classified.Error())
}

func (p *Provider) ShowTyping(ctx context.Context, recipient string, d time.Duration) error {
	return nil
}
func (p *Provider) SetOnline(ctx context.Context, online bool) error { return nil }

func (p *Provider) Destroy(ctx context.Context) error {
	p.reportMu.Lock()
	defer p.reportMu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.destroyed = true
	if p.retry != nil {
		p.retry.Stop()
		p.retry = nil
	}
	return nil
}

// Logout: there is no session to unlink, the number stays registered in the Meta app.
func (p *Provider) Logout(ctx context.Context) error { return p.Destroy(ctx) }

// IsValidSignature checks X-Hub-Signature-256: "sha256=" + HMAC-SHA256(app secret, raw body).
func (p *Provider) IsValidSignature(body []byte, header string) bool {
	given, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	givenBytes, err := hex.DecodeString(given)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(p.credentials.AppSecret))
	mac.Write(body)
	return hmac.Equal(givenBytes, mac.Sum(nil))
}

// webhookValue is the part of a webhook change this provider reads. The whole value travels
// untouched in the raw meta.webhook event.
type webhookValue struct {
	Contacts []struct {
		WaID    string `json:"wa_id"`
		Profile struct {
			Name string `json:"name"`
		} `json:"profile"`
	} `json:"contacts"`
	Messages []struct {
		ID        string `json:"id"`
		From      string `json:"from"`
		Timestamp string `json:"timestamp"`
		Type      string `json:"type"`
		Text      struct {
			Body string `json:"body"`
		} `json:"text"`
	} `json:"messages"`
	Statuses []struct {
		ID          string `json:"id"`
		RecipientID string `json:"recipient_id"`
		Status      string `json:"status"`
	} `json:"statuses"`
}

var deliveryStatuses = map[string]bool{
	events.DeliverySent: true, events.DeliveryDelivered: true, events.DeliveryRead: true, events.DeliveryFailed: true,
}

// HandleWebhookValue publishes one "messages" change that belongs to this phone number.
func (p *Provider) HandleWebhookValue(raw json.RawMessage) {
	if p.hooks.OnEvent == nil {
		return
	}
	p.hooks.OnEvent(events.MetaRawEvent, raw)

	var value webhookValue
	if err := json.Unmarshal(raw, &value); err != nil {
		return
	}
	for _, m := range value.Messages {
		pushName := ""
		for _, contact := range value.Contacts {
			if contact.WaID == m.From {
				pushName = contact.Profile.Name
			}
		}
		var timestamp int64
		_, _ = fmt.Sscan(m.Timestamp, &timestamp) // a bad timestamp stays 0
		p.hooks.OnEvent("message.received", events.MessageReceived{
			MessageID: m.ID, ChatID: m.From, From: m.From, PushName: pushName,
			Timestamp: timestamp, Type: m.Type, Text: m.Text.Body,
		})
	}
	for _, s := range value.Statuses {
		if deliveryStatuses[s.Status] {
			p.hooks.OnEvent("message.status", events.MessageStatus{MessageID: s.ID, ChatID: s.RecipientID, Status: s.Status})
		}
	}
}

// graphError is an answer from Meta that was not 2xx.
type graphError struct {
	status  int
	message string
	code    int
}

func (e *graphError) Error() string {
	if e.message != "" {
		return e.message
	}
	return fmt.Sprintf("HTTP %d", e.status)
}

// call sends one request with the access token. The token goes only in the header, so it never
// appears in an error message.
func (p *Provider) call(ctx context.Context, method, path string, body, result any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.credentials.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var parsed struct {
			Error struct {
				Message string `json:"message"`
				Code    int    `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(answer, &parsed)
		return &graphError{status: resp.StatusCode, message: parsed.Error.Message, code: parsed.Error.Code}
	}
	if result != nil {
		return json.Unmarshal(answer, result)
	}
	return nil
}

// classify decides what a failed call means for the caller:
//   - the request never reached Meta: UNAVAILABLE, the outbox can try again later;
//   - no answer in time: a plain error, Meta may have processed it, so it must not be retried;
//   - expired token: NEEDS_AUTH and FAILED_PRECONDITION (a running job fails once, not per message);
//   - 5xx: UNAVAILABLE; any other refusal: a plain error with Meta's message.
func (p *Provider) classify(err error, action string) error {
	var refused *graphError
	if !errors.As(err, &refused) {
		var netErr net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
			return fmt.Errorf("%s: Meta did not answer in time, the result is unknown", action)
		}
		return calyxerr.New(codes.Unavailable, "%s: Meta could not be reached.", action)
	}
	detail := action + ": " + refused.Error()
	if refused.code == invalidTokenCode || refused.status == http.StatusUnauthorized {
		p.setStatus(corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH)
		return calyxerr.New(codes.FailedPrecondition, "%s. Update the credentials of the instance.", detail)
	}
	if refused.status >= 500 {
		return calyxerr.New(codes.Unavailable, "%s", detail)
	}
	return errors.New(detail)
}

var _ engine.Provider = (*Provider)(nil)
