package events

import "encoding/json"

// CalyxEventTypes are produced by every provider, with the same names and payloads in webhooks
// and in the EventService.Subscribe stream.
// The payload messages of event.proto describe these payloads.
var CalyxEventTypes = []string{"connection.update", "message.received", "message.status", "bulk.finished"}

// WhatsmeowRawEvents can be forwarded untouched as "whatsmeow.<Type>" for clients that need more
// than the Calyx events. The names are the struct names in go.mau.fi/whatsmeow/types/events: to
// support a new one, add it here and in the WhatsApp Web provider.
var WhatsmeowRawEvents = []string{
	"Message", "Receipt", "UndecryptableMessage", "Presence", "ChatPresence",
	"Contact", "PushName", "JoinedGroup", "GroupInfo", "Picture",
}

// MetaRawEvent carries the untouched "value" object of every change Meta sends.
const MetaRawEvent = "meta.webhook"

// SupportedEventTypes lists what a webhook of an instance of this provider can subscribe to.
func SupportedEventTypes(provider string) []string {
	supported := append([]string(nil), CalyxEventTypes...)
	if provider == "META_CLOUD" {
		return append(supported, MetaRawEvent)
	}
	for _, raw := range WhatsmeowRawEvents {
		supported = append(supported, "whatsmeow."+raw)
	}
	return supported
}

// SerializePayload turns a payload into the JSON placed in webhooks and gRPC events.
func SerializePayload(payload any) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type ConnectionUpdate struct {
	// CONNECTED, CONNECTING, NEEDS_AUTH, DISCONNECTED or ERROR.
	Status string `json:"status"`
}

type MessageReceived struct {
	MessageID string `json:"messageId"`
	// Conversation the message belongs to: a phone number, a JID or a group id.
	ChatID string `json:"chatId"`
	// The phone number JID when ChatID is a LID, or the LID when it is a phone number. Empty when unknown.
	ChatAltID string `json:"chatAltId,omitempty"`
	// Who wrote it. Different from ChatID only in groups.
	From      string `json:"from"`
	FromAltID string `json:"fromAltId,omitempty"`
	IsGroup   bool   `json:"isGroup"`
	PushName  string `json:"pushName,omitempty"`
	// Unix time in seconds.
	Timestamp int64 `json:"timestamp"`
	// Provider specific content type, for example "text" or "image".
	Type string `json:"type"`
	// Text or caption, when the message has one.
	Text string `json:"text,omitempty"`
}

// The values of MessageStatus.Status. They are the MessageDeliveryStatus names of event.proto,
// in lower case and without the prefix.
const (
	DeliverySent      = "sent"
	DeliveryDelivered = "delivered"
	DeliveryRead      = "read"
	DeliveryFailed    = "failed"
)

type MessageStatus struct {
	MessageID string `json:"messageId"`
	ChatID    string `json:"chatId"`
	ChatAltID string `json:"chatAltId,omitempty"`
	// One of the Delivery constants.
	Status string `json:"status"`
}

type BulkFinished struct {
	JobID          string `json:"jobId"`
	Status         string `json:"status"`
	TotalMessages  int    `json:"totalMessages"`
	SentMessages   int    `json:"sentMessages"`
	FailedMessages int    `json:"failedMessages"`
	Error          string `json:"error,omitempty"`
}
