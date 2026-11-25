package whatsapp

import (
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"

	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// deviceEntry is one device identity: what the phone shows in its list of linked devices.
type deviceEntry struct {
	Text     string // given to PairPhone, for example "Firefox (Linux)"
	PairType whatsmeow.PairClientType
	Platform waCompanionReg.DeviceProps_PlatformType
	OS       string
}

// deviceEntries maps each entry of the proto enum to its values. A test checks that the two lists
// match.
var deviceEntries = map[corev1.DeviceIdentity]deviceEntry{
	corev1.DeviceIdentity_DEVICE_IDENTITY_CHROME_WINDOWS:  {"Chrome (Windows)", whatsmeow.PairClientChrome, waCompanionReg.DeviceProps_CHROME, "Windows"},
	corev1.DeviceIdentity_DEVICE_IDENTITY_EDGE_WINDOWS:    {"Edge (Windows)", whatsmeow.PairClientEdge, waCompanionReg.DeviceProps_EDGE, "Windows"},
	corev1.DeviceIdentity_DEVICE_IDENTITY_FIREFOX_WINDOWS: {"Firefox (Windows)", whatsmeow.PairClientFirefox, waCompanionReg.DeviceProps_FIREFOX, "Windows"},
	corev1.DeviceIdentity_DEVICE_IDENTITY_FIREFOX_LINUX:   {"Firefox (Linux)", whatsmeow.PairClientFirefox, waCompanionReg.DeviceProps_FIREFOX, "Linux"},
	corev1.DeviceIdentity_DEVICE_IDENTITY_SAFARI_MAC_OS:   {"Safari (Mac OS)", whatsmeow.PairClientSafari, waCompanionReg.DeviceProps_SAFARI, "Mac OS"},
}
