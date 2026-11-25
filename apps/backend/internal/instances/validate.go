package instances

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/core"
)

// The limits of the instance fields. The messages of the checks below never hold the value.
const (
	MaxDisplayNameLength    = 100  // runes, after strings.TrimSpace
	MaxTextLength           = 4096 // runes; the WhatsApp limit for one text
	MaxRecipientLength      = 64   // bytes; the core checks the format
	MaxIdempotencyKeyLength = 255  // bytes, each from 0x21 to 0x7E; the core checks the same rule
	MaxWebhooks             = 20   // webhooks of one instance
	MaxURLLength            = 2048 // bytes of a webhook URL
	MaxSecretLength         = 256  // bytes of a webhook secret or a Meta app secret
	MaxAccessTokenLength    = 4096 // bytes of a Meta access token
	MaxPhoneNumberIDLength  = 32   // digits of a Meta phone number id
)

// phonePattern is the pattern of the core.
var phonePattern = regexp.MustCompile(`^\d{7,15}$`)

// localePattern is the pattern of the core: two lower-case letters, a hyphen, two upper-case letters.
var localePattern = regexp.MustCompile(`^[a-z]{2}-[A-Z]{2}$`)

// checkIdentityAndLocale adds the errors of a device identity and a locale that the user gave.
func checkIdentityAndLocale(f *apierr.Fields, identity, locale *string) {
	if identity != nil && !slices.Contains(core.DeviceIdentities(), *identity) {
		f.Add("device_identity", "must be one of "+strings.Join(core.DeviceIdentities(), ", ")+".")
	}
	if locale != nil && !localePattern.MatchString(*locale) {
		f.Add("locale", "must be a locale like es-ES: two lower-case letters, a hyphen, two upper-case letters.")
	}
}

var phoneNumberIDPattern = regexp.MustCompile(fmt.Sprintf(`^\d{1,%d}$`, MaxPhoneNumberIDLength))

// CheckText adds the errors of one message text: not empty after TrimSpace, at most
// MaxTextLength runes. The text itself is sent unchanged.
func CheckText(f *apierr.Fields, field, text string) {
	if strings.TrimSpace(text) == "" {
		f.Add(field, "is required.")
		return
	}
	if utf8.RuneCountInString(text) > MaxTextLength {
		f.Add(field, fmt.Sprintf("must have at most %d characters.", MaxTextLength))
	}
}

// CheckRecipient adds the errors of one recipient: not empty after TrimSpace, at most
// MaxRecipientLength bytes.
func CheckRecipient(f *apierr.Fields, field, to string) {
	if strings.TrimSpace(to) == "" {
		f.Add(field, "is required.")
		return
	}
	if len(to) > MaxRecipientLength {
		f.Add(field, fmt.Sprintf("must have at most %d bytes.", MaxRecipientLength))
	}
}

// CheckIdempotencyKey checks the Idempotency-Key header, with the rule of the core. An empty
// key means no key. It uses no field path: the key is a header, not a body field. The detail
// never repeats the value.
func CheckIdempotencyKey(key string) error {
	ok := len(key) <= MaxIdempotencyKeyLength
	for i := 0; ok && i < len(key); i++ {
		ok = key[i] >= 0x21 && key[i] <= 0x7E
	}
	if !ok {
		return apierr.Invalid(fmt.Sprintf("The Idempotency-Key header must have 1 to %d printable ASCII characters.",
			MaxIdempotencyKeyLength))
	}
	return nil
}

// trimmedName trims a name and adds the errors of its rules: no control character, 1 to max runes.
func trimmedName(f *apierr.Fields, field, s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	f.NoControl(field, s)
	if n := utf8.RuneCountInString(s); n < 1 || n > maxRunes {
		f.Add(field, fmt.Sprintf("must have 1 to %d characters.", maxRunes))
	}
	return s
}

// validPairing checks the pairing mode and the phone of a WhatsApp Web request. An empty mode
// is "qr". It returns the mode and the phone.
func validPairing(f *apierr.Fields, pairing, phone string) (string, string) {
	phone = strings.TrimSpace(phone)
	switch pairing {
	case "", "qr":
		if phone != "" {
			f.Add("phone", "must be empty when pairing is qr.")
		}
		return "qr", ""
	case "code":
		if !phonePattern.MatchString(phone) {
			f.Add("phone", "is required for pairing by code: 7 to 15 digits, no other characters.")
		}
		return "code", phone
	}
	f.Add("pairing", `must be "qr" or "code".`)
	return "", ""
}

// validMeta trims the Meta credentials and checks each field.
func validMeta(f *apierr.Fields, m *core.MetaCredentials) *core.MetaCredentials {
	if m == nil {
		f.Add("meta_credentials", "is required for META_CLOUD.")
		return nil
	}
	out := core.MetaCredentials{
		PhoneNumberID: strings.TrimSpace(m.PhoneNumberID),
		AccessToken:   strings.TrimSpace(m.AccessToken),
		AppSecret:     strings.TrimSpace(m.AppSecret),
	}
	if !phoneNumberIDPattern.MatchString(out.PhoneNumberID) {
		f.Add("meta_credentials.phone_number_id", fmt.Sprintf("must have 1 to %d digits, no other characters.", MaxPhoneNumberIDLength))
	}
	if n := len(out.AccessToken); n < 1 || n > MaxAccessTokenLength {
		f.Add("meta_credentials.access_token", fmt.Sprintf("must have 1 to %d bytes.", MaxAccessTokenLength))
	}
	if n := len(out.AppSecret); n < 1 || n > MaxSecretLength {
		f.Add("meta_credentials.app_secret", fmt.Sprintf("must have 1 to %d bytes.", MaxSecretLength))
	}
	return &out
}

// validateCreate checks a new instance or a re-pair and returns the cleaned input and the pairing mode
// ("qr", "code", or "" for Meta). It reports every wrong field at once.
func (s *Service) validateCreate(in CreateInput) (CreateInput, string, error) {
	var f apierr.Fields
	if in.ClientID != "" {
		id, err := uuid.Parse(in.ClientID)
		if err != nil {
			f.Add("client_id", "must be a UUID.")
		}
		in.clientID = id
	}
	in.DisplayName = trimmedName(&f, "display_name", in.DisplayName, MaxDisplayNameLength)
	var mode string
	switch in.Provider {
	case "WHATSAPP_WEB":
		if in.Meta != nil {
			f.Add("meta_credentials", "is only for META_CLOUD.")
		}
		mode, in.Phone = validPairing(&f, in.Pairing, in.Phone)
		checkIdentityAndLocale(&f, in.DeviceIdentity, in.Locale)
		if in.Locale == nil {
			f.Add("locale", "is required for a new WHATSAPP_WEB instance, for example es-ES.")
		}
	case "META_CLOUD":
		if in.SendOnly {
			f.Add("send_only", "is not supported for META_CLOUD.")
		}
		if in.Pairing != "" {
			f.Add("pairing", "is only for WHATSAPP_WEB.")
		}
		if in.Phone != "" {
			f.Add("phone", "is only for WHATSAPP_WEB.")
		}
		if in.DeviceIdentity != nil {
			f.Add("device_identity", "is only for WHATSAPP_WEB.")
		}
		if in.Locale != nil {
			f.Add("locale", "is only for WHATSAPP_WEB.")
		}
		in.Meta = validMeta(&f, in.Meta)
	case "":
		f.Add("provider", "is required.")
	default:
		f.Add("provider", `must be "WHATSAPP_WEB" or "META_CLOUD".`)
	}
	if in.Provider == "WHATSAPP_WEB" || in.Provider == "META_CLOUD" {
		ValidateWebhooks(&f, in.Provider, in.SendOnly, in.Webhooks, false)
	}
	return in, mode, f.Err()
}
