package calyx

import (
	"encoding/json"
	"time"
)

// Event is one event of the core, as a webhook delivers it. Decode a webhook body into an
// Event with json.Unmarshal, then call the method that fits the event name.
type Event struct {
	// ID identifies the event. A retry delivers the same ID again.
	ID string `json:"id"`
	// Event is the event name, for example "message.received".
	Event string `json:"event"`
	// Instance is the core name of the instance.
	Instance string `json:"instance"`
	// Timestamp is the time of the event. It keeps the full precision (RFC 3339 with nanoseconds).
	Timestamp time.Time `json:"timestamp"`
	// Data is the payload. Events other than the four typed ones keep it as raw JSON.
	Data json.RawMessage `json:"data"`
}

// ConnectionStatus is the status of an instance. The type is open: a newer core can send a
// value that the constants do not list.
type ConnectionStatus string

// The values of ConnectionStatus. They are the InstanceStatus names without the prefix.
const (
	ConnectionConnected    ConnectionStatus = "CONNECTED"
	ConnectionConnecting   ConnectionStatus = "CONNECTING"
	ConnectionNeedsAuth    ConnectionStatus = "NEEDS_AUTH"
	ConnectionDisconnected ConnectionStatus = "DISCONNECTED"
	ConnectionError        ConnectionStatus = "ERROR"
)

// BulkJobStatus is the status of a bulk job. The type is open.
type BulkJobStatus string

// The values of BulkJobStatus. They are the BulkJobStatus names without the prefix.
const (
	BulkJobQueued    BulkJobStatus = "QUEUED"
	BulkJobRunning   BulkJobStatus = "RUNNING"
	BulkJobCompleted BulkJobStatus = "COMPLETED"
	BulkJobCancelled BulkJobStatus = "CANCELLED"
	BulkJobFailed    BulkJobStatus = "FAILED"
)

// DeliveryStatus is the delivery status of a message. The type is open.
type DeliveryStatus string

// The values of DeliveryStatus. They are the MessageDeliveryStatus names without the prefix,
// in lower case.
const (
	DeliverySent      DeliveryStatus = "sent"
	DeliveryDelivered DeliveryStatus = "delivered"
	DeliveryRead      DeliveryStatus = "read"
	DeliveryFailed    DeliveryStatus = "failed"
)

// ConnectionUpdate is the payload of "connection.update".
type ConnectionUpdate struct {
	// Status is the new status of the instance.
	Status ConnectionStatus `json:"status"`
}

// MessageReceived is the payload of "message.received".
type MessageReceived struct {
	// MessageID identifies the message.
	MessageID string `json:"messageId"`
	// ChatID is the conversation of the message: a phone number JID, a LID or a group ID.
	ChatID string `json:"chatId"`
	// ChatAltID is the other form of ChatID (phone number JID or LID). It is nil when unknown.
	ChatAltID *string `json:"chatAltId,omitempty"`
	// From is the sender. It differs from ChatID only in groups.
	From string `json:"from"`
	// FromAltID is the other form of From. It is nil when unknown.
	FromAltID *string `json:"fromAltId,omitempty"`
	// IsGroup is true when the chat is a group.
	IsGroup bool `json:"isGroup"`
	// PushName is the display name of the sender. It is nil when unknown.
	PushName *string `json:"pushName,omitempty"`
	// Timestamp is the Unix time of the message, in seconds.
	Timestamp int64 `json:"timestamp"`
	// Type is the content type of the provider, for example "text" or "image".
	Type string `json:"type"`
	// Text is the text or the caption. It is nil when the message has none.
	Text *string `json:"text,omitempty"`
}

// MessageStatus is the payload of "message.status".
type MessageStatus struct {
	// MessageID identifies the message.
	MessageID string `json:"messageId"`
	// ChatID is the conversation of the message.
	ChatID string `json:"chatId"`
	// ChatAltID is the other form of ChatID. It is nil when unknown.
	ChatAltID *string `json:"chatAltId,omitempty"`
	// Status is the new delivery status.
	Status DeliveryStatus `json:"status"`
}

// BulkFinished is the payload of "bulk.finished".
type BulkFinished struct {
	// JobID identifies the bulk job.
	JobID string `json:"jobId"`
	// Status is the final status of the job.
	Status BulkJobStatus `json:"status"`
	// TotalMessages is the number of messages in the job.
	TotalMessages int32 `json:"totalMessages"`
	// SentMessages is the number of messages that the core sent.
	SentMessages int32 `json:"sentMessages"`
	// FailedMessages is the number of messages that failed.
	FailedMessages int32 `json:"failedMessages"`
	// Error is the reason of a failed job. It is nil when the job has no error.
	Error *string `json:"error,omitempty"`
}

// ConnectionUpdate decodes the payload of a "connection.update" event.
func (e Event) ConnectionUpdate() (ConnectionUpdate, error) {
	return decodeData[ConnectionUpdate](e, "connection.update")
}

// MessageReceived decodes the payload of a "message.received" event.
func (e Event) MessageReceived() (MessageReceived, error) {
	return decodeData[MessageReceived](e, "message.received")
}

// MessageStatus decodes the payload of a "message.status" event.
func (e Event) MessageStatus() (MessageStatus, error) {
	return decodeData[MessageStatus](e, "message.status")
}

// BulkFinished decodes the payload of a "bulk.finished" event.
func (e Event) BulkFinished() (BulkFinished, error) {
	return decodeData[BulkFinished](e, "bulk.finished")
}

// decodeData checks the envelope and the event name, then decodes Data. Unknown fields stay
// ignored, so an older SDK reads the events of a newer core.
func decodeData[T any](e Event, name string) (T, error) {
	var payload T
	switch {
	case e.ID == "" || e.Instance == "" || e.Timestamp.IsZero() || len(e.Data) == 0:
		return payload, invalidEvent("The event has no id, instance, timestamp or data.")
	case e.Event != name:
		return payload, invalidEvent("The event is " + e.Event + ", not " + name + ".")
	}
	if err := json.Unmarshal(e.Data, &payload); err != nil {
		return payload, invalidEvent("The data of " + name + " is not valid.")
	}
	return payload, nil
}

func invalidEvent(detail string) *Error {
	return &Error{Source: SourceClient, Code: CodeInvalidEvent, Retry: AdviceFor(SourceClient, CodeInvalidEvent), Detail: detail}
}
