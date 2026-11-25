package whatsapp

import (
	"log/slog"

	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/proto/waWa6"
	"go.mau.fi/whatsmeow/store"
	"google.golang.org/protobuf/proto"

	"calyx/core/internal/locale"
)

// clientPayload is the payload hook of an instance (whatsmeow's Client.GetClientPayload). whatsmeow
// keeps one label and one locale for the whole process, so each instance changes its own copy of
// the payload. It changes only the locale and, for a registration, the label of the device.
func clientPayload(device *store.Device, entry deviceEntry, loc locale.Locale, log *slog.Logger) *waWa6.ClientPayload {
	payload := device.GetClientPayload() // a copy, with whatsmeow's own version and settings
	payload.UserAgent.LocaleLanguageIso6391 = proto.String(loc.Language)
	payload.UserAgent.LocaleCountryIso31661Alpha2 = proto.String(loc.Country)

	registration := payload.GetDevicePairingData()
	if registration == nil {
		return payload // a login: the label was fixed at the pairing
	}
	props := &waCompanionReg.DeviceProps{}
	if err := proto.Unmarshal(registration.GetDeviceProps(), props); err != nil {
		log.Error("Could not read the device label. The phone shows the default label of whatsmeow.", "error", err)
		return payload
	}
	props.Os = proto.String(entry.OS)
	props.PlatformType = entry.Platform.Enum()
	encoded, err := proto.Marshal(props)
	if err != nil {
		log.Error("Could not write the device label. The phone shows the default label of whatsmeow.", "error", err)
		return payload
	}
	registration.DeviceProps = encoded
	return payload
}
