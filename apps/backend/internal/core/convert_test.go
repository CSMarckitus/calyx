package core

import (
	"slices"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

func TestStatusNames(t *testing.T) {
	cases := map[corev1.InstanceStatus]string{
		corev1.InstanceStatus_INSTANCE_STATUS_CONNECTED:    "CONNECTED",
		corev1.InstanceStatus_INSTANCE_STATUS_CONNECTING:   "CONNECTING",
		corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH:   "NEEDS_AUTH",
		corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED: "DISCONNECTED",
		corev1.InstanceStatus_INSTANCE_STATUS_ERROR:        "ERROR",
		corev1.InstanceStatus_INSTANCE_STATUS_UNSPECIFIED:  "UNSPECIFIED",
	}
	for in, want := range cases {
		if got := statusName(in); got != want {
			t.Errorf("%v: got %q, want %q", in, got, want)
		}
	}
}

func TestProviderNames(t *testing.T) {
	if got := providerName(corev1.Provider_PROVIDER_WHATSAPP_WEB); got != "WHATSAPP_WEB" {
		t.Errorf("got %q", got)
	}
	if got := providerName(corev1.Provider_PROVIDER_META_CLOUD); got != "META_CLOUD" {
		t.Errorf("got %q", got)
	}
	if got := providerName(corev1.Provider_PROVIDER_UNSPECIFIED); got != "UNSPECIFIED" {
		t.Errorf("got %q", got)
	}
}

func TestBulkStatusNames(t *testing.T) {
	cases := map[corev1.BulkJobStatus]string{
		corev1.BulkJobStatus_BULK_JOB_STATUS_QUEUED:    "QUEUED",
		corev1.BulkJobStatus_BULK_JOB_STATUS_RUNNING:   "RUNNING",
		corev1.BulkJobStatus_BULK_JOB_STATUS_COMPLETED: "COMPLETED",
		corev1.BulkJobStatus_BULK_JOB_STATUS_CANCELLED: "CANCELLED",
		corev1.BulkJobStatus_BULK_JOB_STATUS_FAILED:    "FAILED",
	}
	for in, want := range cases {
		if got := bulkStatusName(in); got != want {
			t.Errorf("%v: got %q, want %q", in, got, want)
		}
	}
}

func TestWebhookConfigsLeaveAnEmptySecretOut(t *testing.T) {
	got := webhookConfigs([]Webhook{{"u", "e", ""}, {"u2", "e2", "s"}})
	if got[0].Secret != nil || got[1].Secret == nil || *got[1].Secret != "s" {
		t.Errorf("got %v", got)
	}
	if empty := webhookConfigs(nil); empty == nil || len(empty) != 0 {
		t.Errorf("nil input gave %v", empty)
	}
}

func TestCreateRequestSendsTheIdentityAndTheLocale(t *testing.T) {
	req, err := createRequest(InstanceConfig{CoreName: "i_a", Provider: "WHATSAPP_WEB", Pairing: "qr",
		DeviceIdentity: "FIREFOX_LINUX", Locale: "es-ES"})
	if err != nil {
		t.Fatal(err)
	}
	web := req.GetWhatsappWeb()
	if web.GetDeviceIdentity() != corev1.DeviceIdentity_DEVICE_IDENTITY_FIREFOX_LINUX || web.Locale == nil || web.GetLocale() != "es-ES" {
		t.Errorf("got %v", web)
	}

	req, err = createRequest(InstanceConfig{CoreName: "i_a", Provider: "WHATSAPP_WEB", Pairing: "qr"})
	if err != nil {
		t.Fatal(err)
	}
	if web := req.GetWhatsappWeb(); web.GetDeviceIdentity() != corev1.DeviceIdentity_DEVICE_IDENTITY_UNSPECIFIED || web.Locale != nil {
		t.Errorf("values that the user did not give were sent: %v", web)
	}
}

func TestCreateRequestRefusesAnUnknownIdentity(t *testing.T) {
	_, err := createRequest(InstanceConfig{CoreName: "i_a", Provider: "WHATSAPP_WEB", Pairing: "qr", DeviceIdentity: "NETSCAPE_BEOS"})
	if err == nil {
		t.Fatal("accepted")
	}
}

func TestDeviceIdentitiesMatchTheEnum(t *testing.T) {
	want := []string{"CHROME_WINDOWS", "EDGE_WINDOWS", "FIREFOX_WINDOWS", "FIREFOX_LINUX", "SAFARI_MAC_OS"}
	if got := DeviceIdentities(); !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}
	if len(corev1.DeviceIdentity_name) != len(want)+1 {
		t.Errorf("the enum has %d values; update this test", len(corev1.DeviceIdentity_name))
	}
}

func TestCreateRequestAlwaysCarriesTheSendOnlyFlag(t *testing.T) {
	off := false
	req, err := createRequest(InstanceConfig{CoreName: "i_a", Provider: "WHATSAPP_WEB", Pairing: "qr", SendOnly: &off})
	if err != nil {
		t.Fatal(err)
	}
	if req.IsUsedOnlyForSendMessages == nil || *req.IsUsedOnlyForSendMessages {
		t.Errorf("got %v, want a present false", req.IsUsedOnlyForSendMessages)
	}
	req, err = createRequest(InstanceConfig{CoreName: "i_a", Provider: "WHATSAPP_WEB", Pairing: "qr"})
	if err != nil {
		t.Fatal(err)
	}
	if req.IsUsedOnlyForSendMessages == nil || *req.IsUsedOnlyForSendMessages {
		t.Errorf("nil SendOnly: got %v, want a present false", req.IsUsedOnlyForSendMessages)
	}
}

func TestBulkPacingKeepsAbsentRanges(t *testing.T) {
	if bulkPacing(nil) != nil {
		t.Error("nil should stay nil")
	}
	p := bulkPacing(&BulkPacing{Delay: &Range{Min: 3000, Max: 9000}})
	if p.GetDelay().GetMin() != 3000 || p.GetDelay().GetMax() != 9000 || p.PauseEvery != nil || p.PauseDuration != nil {
		t.Errorf("got %v", p)
	}
}

func TestBulkJobConversion(t *testing.T) {
	if j := bulkJob(&corev1.BulkJob{CreatedAt: timestamppb.Now()}); j.EstimatedFinishAt != nil {
		t.Error("estimated finish should be nil")
	}
	for s, want := range map[corev1.BulkJobStatus]string{
		corev1.BulkJobStatus_BULK_JOB_STATUS_QUEUED:    "QUEUED",
		corev1.BulkJobStatus_BULK_JOB_STATUS_RUNNING:   "RUNNING",
		corev1.BulkJobStatus_BULK_JOB_STATUS_COMPLETED: "COMPLETED",
		corev1.BulkJobStatus_BULK_JOB_STATUS_CANCELLED: "CANCELLED",
		corev1.BulkJobStatus_BULK_JOB_STATUS_FAILED:    "FAILED",
	} {
		if got := bulkJob(&corev1.BulkJob{Status: s, CreatedAt: timestamppb.Now()}).Status; got != want {
			t.Errorf("%v: %q", s, got)
		}
	}
}

func TestStatusConstantsMatchEnums(t *testing.T) {
	instance := []string{StatusConnected, StatusConnecting, StatusNeedsAuth, StatusDisconnected, StatusError}
	job := []string{JobQueued, JobRunning, JobCompleted, JobCancelled, JobFailed}
	for name, pair := range map[string][2][]string{
		"instance": {instance, InstanceStatuses()},
		"bulk job": {job, BulkJobStatuses()},
	} {
		fromConstants := slices.Sorted(slices.Values(pair[0]))
		fromEnum := slices.Sorted(slices.Values(pair[1]))
		if !slices.Equal(fromConstants, fromEnum) {
			t.Errorf("%s constants give %v, the enum has %v", name, fromConstants, fromEnum)
		}
	}
}
