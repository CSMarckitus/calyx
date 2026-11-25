package engine

import (
	"slices"

	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/locale"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// resolveIdentityAndLocale picks the device identity and the locale of a pairing. It returns the
// device identity, the locale, and where the identity comes from ("requested" or "random").
// A Meta Cloud instance has neither.
func (o *Orchestrator) resolveIdentityAndLocale(params CreateInstanceParams, existing InstanceRow, found bool) (corev1.DeviceIdentity, string, string, error) {
	unspecified := corev1.DeviceIdentity_DEVICE_IDENTITY_UNSPECIFIED
	if params.Provider != "WHATSAPP_WEB" {
		if params.DeviceIdentity != unspecified || params.Locale != "" {
			return unspecified, "", "", calyxerr.New(codes.InvalidArgument, "device_identity and locale apply only to WhatsApp Web instances.")
		}
		return unspecified, "", "", nil
	}

	var loc string
	switch {
	case params.Locale != "":
		parsed, err := locale.Parse(params.Locale)
		if err != nil {
			return unspecified, "", "", calyxerr.New(codes.InvalidArgument, "locale %v.", err)
		}
		loc = parsed.String()
	case found && existing.Locale != "":
		// The locale belongs to the user, so a re-pair keeps it.
		loc = existing.Locale
	default:
		return unspecified, "", "", calyxerr.New(codes.InvalidArgument, "locale is required for a new pairing, for example es-ES.")
	}

	if params.DeviceIdentity != unspecified {
		if _, known := corev1.DeviceIdentity_name[int32(params.DeviceIdentity)]; !known {
			return unspecified, "", "", calyxerr.New(codes.InvalidArgument, "device_identity %d is not in the list of the core.", params.DeviceIdentity)
		}
		return params.DeviceIdentity, loc, "requested", nil
	}
	// The identity belongs to the linked device, so a re-pair picks a new one.
	entries := deviceIdentities()
	return entries[o.opts.Intn(len(entries))], loc, "random", nil
}

// deviceIdentities lists the entries of the enum, sorted by number, without UNSPECIFIED.
func deviceIdentities() []corev1.DeviceIdentity {
	var all []corev1.DeviceIdentity
	for number := range corev1.DeviceIdentity_name {
		if number != 0 {
			all = append(all, corev1.DeviceIdentity(number))
		}
	}
	slices.Sort(all)
	return all
}

// identityName is the form stored in the database: the enum name, or "" for none.
func identityName(identity corev1.DeviceIdentity) string {
	if identity == corev1.DeviceIdentity_DEVICE_IDENTITY_UNSPECIFIED {
		return ""
	}
	return identity.String()
}

// identityFromName is the reverse of identityName. An unknown name gives UNSPECIFIED.
func identityFromName(name string) corev1.DeviceIdentity {
	return corev1.DeviceIdentity(corev1.DeviceIdentity_value[name])
}
