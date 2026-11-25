package core

import (
	"time"

	"google.golang.org/grpc"

	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// Deadlines of the calls. context.WithTimeout keeps an earlier deadline of
// the caller. SendText and CreateInstance add no deadline of their own.
const (
	readTimeout   = 10 * time.Second // ListInstances, GetInstanceStatus, GetBulkJob, GetBulkJobs, CancelBulkJob
	changeTimeout = 30 * time.Second // UpdateInstanceConfig
	removeTimeout = 60 * time.Second // RemoveInstance
	bulkTimeout   = 30 * time.Second // SendBulkSameMessage, SendBulkCustomMessages
)

// Client calls the core. The token is already on the connection (see TokenInterceptors).
type Client struct {
	instances corev1.InstanceServiceClient
	messages  corev1.MessageServiceClient
	events    corev1.EventServiceClient
}

func NewClient(conn *grpc.ClientConn) *Client {
	return &Client{
		instances: corev1.NewInstanceServiceClient(conn),
		messages:  corev1.NewMessageServiceClient(conn),
		events:    corev1.NewEventServiceClient(conn),
	}
}

type InstanceInfo struct {
	CoreName, Provider, Status string
	Enabled, SendOnly          bool
	DeviceIdentity, Locale     string // WhatsApp Web; "" when the core has none
}

// Webhook is one webhook of an instance. An empty Secret means no signature.
type Webhook struct{ URL, Event, Secret string }

// InstanceConfig is the configuration of an instance for CreateInstance and UpdateInstanceConfig.
type InstanceConfig struct {
	CoreName string
	Provider string // always explicit
	SendOnly *bool  // UpdateInstanceConfig: nil = unchanged; CreateInstance: nil = false
	Enabled  *bool  // nil = unchanged (UpdateInstanceConfig only)
	Webhooks []Webhook
	Pairing  string // "qr" or "code" (WhatsApp Web)
	Phone    string // for "code"
	// CreateInstance of WhatsApp Web only. DeviceIdentity is a name of DeviceIdentities(); "" lets
	// the core pick one at random. Locale is "es-ES"; "" lets a re-pair keep the stored one.
	DeviceIdentity string
	Locale         string
	Meta           *MetaCredentials
}

// MetaCredentials are the Meta Cloud API credentials of an instance.
type MetaCredentials struct{ PhoneNumberID, AccessToken, AppSecret string }
