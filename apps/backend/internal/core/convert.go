package core

import (
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	"calyx/backend/internal/apierr"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// Instance and bulk job statuses of the core, without the enum prefix. They are untyped, so a
// typed constant in another package can use them. A test keeps them equal to the enums.
const (
	StatusConnected    = "CONNECTED"
	StatusConnecting   = "CONNECTING"
	StatusNeedsAuth    = "NEEDS_AUTH"
	StatusDisconnected = "DISCONNECTED"
	StatusError        = "ERROR"

	JobQueued    = "QUEUED"
	JobRunning   = "RUNNING"
	JobCompleted = "COMPLETED"
	JobCancelled = "CANCELLED"
	JobFailed    = "FAILED"
)

// statusName returns the instance status without the enum prefix ("CONNECTED").
func statusName(s corev1.InstanceStatus) string {
	return strings.TrimPrefix(s.String(), "INSTANCE_STATUS_")
}

const identityPrefix = "DEVICE_IDENTITY_"

// DeviceIdentities lists the device identities of the core, without the prefix and without
// UNSPECIFIED, in the order of the enum. It reads the generated enum, so no list is copied.
func DeviceIdentities() []string {
	return enumNames(corev1.DeviceIdentity_name, func(n int32) string { return identityName(corev1.DeviceIdentity(n)) })
}

// InstanceStatuses lists the instance statuses of the core, without the prefix and without
// UNSPECIFIED, in the order of the enum.
func InstanceStatuses() []string {
	return enumNames(corev1.InstanceStatus_name, func(n int32) string { return statusName(corev1.InstanceStatus(n)) })
}

// Providers lists the providers of the core, without the prefix and without UNSPECIFIED, in the
// order of the enum.
func Providers() []string {
	return enumNames(corev1.Provider_name, func(n int32) string { return providerName(corev1.Provider(n)) })
}

// BulkJobStatuses lists the bulk job statuses of the core, without the prefix and without
// UNSPECIFIED, in the order of the enum.
func BulkJobStatuses() []string {
	return enumNames(corev1.BulkJobStatus_name, func(n int32) string { return bulkStatusName(corev1.BulkJobStatus(n)) })
}

// enumNames lists the names of a generated enum in the order of its numbers, without number 0
// (UNSPECIFIED). name converts a number to the name without the prefix.
func enumNames(byNumber map[int32]string, name func(int32) string) []string {
	numbers := make([]int32, 0, len(byNumber))
	for n := range byNumber {
		if n != 0 {
			numbers = append(numbers, n)
		}
	}
	slices.Sort(numbers)
	names := make([]string, 0, len(numbers))
	for _, n := range numbers {
		names = append(names, name(n))
	}
	return names
}

// identityName returns the device identity without the prefix ("FIREFOX_LINUX"), or "" for none.
func identityName(d corev1.DeviceIdentity) string {
	if d == corev1.DeviceIdentity_DEVICE_IDENTITY_UNSPECIFIED {
		return ""
	}
	return strings.TrimPrefix(d.String(), identityPrefix)
}

// providerName returns the provider without the enum prefix ("WHATSAPP_WEB").
func providerName(p corev1.Provider) string {
	return strings.TrimPrefix(p.String(), "PROVIDER_")
}

// bulkStatusName returns the bulk job status without the enum prefix ("QUEUED").
func bulkStatusName(s corev1.BulkJobStatus) string {
	return strings.TrimPrefix(s.String(), "BULK_JOB_STATUS_")
}

// webhookConfigs converts webhooks. An empty secret stays absent (no signature).
func webhookConfigs(ws []Webhook) []*corev1.WebhookConfig {
	out := make([]*corev1.WebhookConfig, 0, len(ws))
	for _, w := range ws {
		wc := &corev1.WebhookConfig{Url: w.URL, Event: w.Event}
		if w.Secret != "" {
			secret := w.Secret
			wc.Secret = &secret
		}
		out = append(out, wc)
	}
	return out
}

// updateRequest builds the change request. The core reloads a live instance whenever
// is_used_only_for_send_messages is present, so it is sent only when SendOnly is set.
// The webhook list is always present: an empty list removes every webhook.
func updateRequest(cfg InstanceConfig) *corev1.UpdateInstanceConfigRequest {
	req := &corev1.UpdateInstanceConfigRequest{
		InstanceName:              cfg.CoreName,
		IsEnabled:                 cfg.Enabled,
		IsUsedOnlyForSendMessages: cfg.SendOnly,
		Webhooks:                  &corev1.WebhookList{Webhooks: webhookConfigs(cfg.Webhooks)},
	}
	if cfg.Meta != nil {
		req.MetaCloud = &corev1.MetaCloudSettings{
			PhoneNumberId: cfg.Meta.PhoneNumberID,
			AccessToken:   cfg.Meta.AccessToken,
			AppSecret:     cfg.Meta.AppSecret,
		}
	}
	return req
}

// createRequest builds the pairing request. It never leaves the provider settings out, so the core
// never falls back to its default. The send-only flag is always present, so a re-pair never keeps
// a stored value by accident. The device identity and the locale are sent only when they are set.
func createRequest(cfg InstanceConfig) (*corev1.CreateInstanceRequest, error) {
	req := &corev1.CreateInstanceRequest{
		InstanceName:              cfg.CoreName,
		IsUsedOnlyForSendMessages: proto.Bool(cfg.SendOnly != nil && *cfg.SendOnly),
		Webhooks:                  webhookConfigs(cfg.Webhooks),
	}
	switch cfg.Provider {
	case "WHATSAPP_WEB":
		settings := &corev1.WhatsAppWebSettings{}
		switch cfg.Pairing {
		case "qr":
			settings.PairingMethod = &corev1.WhatsAppWebSettings_PairingQr{PairingQr: true}
		case "code":
			settings.PairingMethod = &corev1.WhatsAppWebSettings_PairingCode{
				PairingCode: &corev1.PairingCodeSettings{PhoneNumber: cfg.Phone},
			}
		default:
			return nil, apierr.Invalid(`pairing must be "qr" or "code".`)
		}
		if cfg.DeviceIdentity != "" {
			number, ok := corev1.DeviceIdentity_value[identityPrefix+cfg.DeviceIdentity]
			if !ok {
				return nil, apierr.Invalid("device_identity must be one of " + strings.Join(DeviceIdentities(), ", ") + ".")
			}
			settings.DeviceIdentity = corev1.DeviceIdentity(number)
		}
		if cfg.Locale != "" {
			settings.Locale = proto.String(cfg.Locale)
		}
		req.ProviderSettings = &corev1.CreateInstanceRequest_WhatsappWeb{WhatsappWeb: settings}
	case "META_CLOUD":
		if cfg.Meta == nil {
			return nil, apierr.Invalid("meta_credentials are required.")
		}
		req.ProviderSettings = &corev1.CreateInstanceRequest_MetaCloud{MetaCloud: &corev1.MetaCloudSettings{
			PhoneNumberId: cfg.Meta.PhoneNumberID,
			AccessToken:   cfg.Meta.AccessToken,
			AppSecret:     cfg.Meta.AppSecret,
		}}
	default:
		return nil, apierr.Invalid("provider must be WHATSAPP_WEB or META_CLOUD.")
	}
	return req, nil
}

func intRange(r *Range) *corev1.IntRange {
	if r == nil {
		return nil
	}
	return &corev1.IntRange{Min: r.Min, Max: r.Max}
}

// bulkPacing passes the pacing as given. Absent ranges stay absent.
func bulkPacing(p *BulkPacing) *corev1.BulkPacing {
	if p == nil {
		return nil
	}
	return &corev1.BulkPacing{Delay: intRange(p.Delay), PauseEvery: intRange(p.PauseEvery), PauseDuration: intRange(p.PauseDuration)}
}

// bulkJob converts a job. The core's instance_name is not copied: the backend knows the instance
// from its own bulk_jobs row.
func bulkJob(j *corev1.BulkJob) BulkJob {
	out := BulkJob{
		ID:              j.GetId(),
		Status:          bulkStatusName(j.GetStatus()),
		Total:           j.GetTotalMessages(),
		Sent:            j.GetSentMessages(),
		Failed:          j.GetFailedMessages(),
		ProgressPercent: j.GetProgressPercent(),
		CreatedAt:       j.GetCreatedAt().AsTime().UTC(),
		Replayed:        j.GetReplayed(),
	}
	if j.EstimatedFinishAt != nil {
		t := j.EstimatedFinishAt.AsTime().UTC()
		out.EstimatedFinishAt = &t
	}
	return out
}
