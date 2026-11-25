package instances

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/core"
)

// fieldsOf returns the field paths of a 400 error, or nil for a nil error.
func fieldsOf(t *testing.T, err error) []string {
	t.Helper()
	if err == nil {
		return nil
	}
	e, ok := apierr.As(err)
	if !ok || e.Status != 400 || e.Code != apierr.CodeInvalidRequest {
		t.Fatalf("want 400 invalid_request, got %v", err)
	}
	out := make([]string, 0, len(e.Errors))
	for _, fe := range e.Errors {
		out = append(out, fe.Field)
	}
	return out
}

func webCreate() CreateInput {
	return CreateInput{DisplayName: "Front desk", Provider: "WHATSAPP_WEB", Locale: ptr("es-ES")}
}

func metaCreate() CreateInput {
	return CreateInput{DisplayName: "Meta line", Provider: "META_CLOUD",
		Meta: &core.MetaCredentials{PhoneNumberID: "1001", AccessToken: "token", AppSecret: "secret"}}
}

// hooks returns n webhooks with distinct supported events while there are enough events.
func hooks(n int) []WebhookInput {
	events := SupportedEvents("WHATSAPP_WEB")
	out := make([]WebhookInput, n)
	for i := range out {
		out[i] = WebhookInput{URL: fmt.Sprintf("https://a.example/%d", i), Event: events[i%len(events)]}
	}
	return out
}

// Each limit is accepted at its value and refused one step past it, with the field path.
func TestCreateLimits(t *testing.T) {
	s := &Service{}
	cases := []struct {
		name string
		in   func() CreateInput
		want string // "" when the input is valid
	}{
		{"display_name 100", func() CreateInput {
			in := webCreate()
			in.DisplayName = strings.Repeat("é", MaxDisplayNameLength)
			return in
		}, ""},
		{"display_name 101", func() CreateInput {
			in := webCreate()
			in.DisplayName = strings.Repeat("é", MaxDisplayNameLength+1)
			return in
		}, "display_name"},
		{"display_name blank", func() CreateInput { in := webCreate(); in.DisplayName = "  "; return in }, "display_name"},
		{"device_identity of the list", func() CreateInput { in := webCreate(); in.DeviceIdentity = ptr("SAFARI_MAC_OS"); return in }, ""},
		{"device_identity unknown", func() CreateInput { in := webCreate(); in.DeviceIdentity = ptr("Front desk"); return in }, "device_identity"},
		{"locale pt-BR", func() CreateInput { in := webCreate(); in.Locale = ptr("pt-BR"); return in }, ""},
		{"locale es-es", func() CreateInput { in := webCreate(); in.Locale = ptr("es-es"); return in }, "locale"},
		{"locale missing", func() CreateInput { in := webCreate(); in.Locale = nil; return in }, "locale"},
		{"device_identity for Meta", func() CreateInput { in := metaCreate(); in.DeviceIdentity = ptr("SAFARI_MAC_OS"); return in }, "device_identity"},
		{"locale for Meta", func() CreateInput { in := metaCreate(); in.Locale = ptr("es-ES"); return in }, "locale"},
		{"phone_number_id 32", func() CreateInput {
			in := metaCreate()
			in.Meta.PhoneNumberID = strings.Repeat("1", MaxPhoneNumberIDLength)
			return in
		}, ""},
		{"phone_number_id 33", func() CreateInput {
			in := metaCreate()
			in.Meta.PhoneNumberID = strings.Repeat("1", MaxPhoneNumberIDLength+1)
			return in
		}, "meta_credentials.phone_number_id"},
		{"phone_number_id letters", func() CreateInput { in := metaCreate(); in.Meta.PhoneNumberID = "P1"; return in }, "meta_credentials.phone_number_id"},
		{"access_token 4096", func() CreateInput {
			in := metaCreate()
			in.Meta.AccessToken = strings.Repeat("t", MaxAccessTokenLength)
			return in
		}, ""},
		{"access_token 4097", func() CreateInput {
			in := metaCreate()
			in.Meta.AccessToken = strings.Repeat("t", MaxAccessTokenLength+1)
			return in
		}, "meta_credentials.access_token"},
		{"app_secret 256", func() CreateInput {
			in := metaCreate()
			in.Meta.AppSecret = strings.Repeat("s", MaxSecretLength)
			return in
		}, ""},
		{"app_secret 257", func() CreateInput {
			in := metaCreate()
			in.Meta.AppSecret = strings.Repeat("s", MaxSecretLength+1)
			return in
		}, "meta_credentials.app_secret"},
		{"app_secret empty", func() CreateInput { in := metaCreate(); in.Meta.AppSecret = ""; return in }, "meta_credentials.app_secret"},
		{"url 2048", func() CreateInput {
			in := webCreate()
			in.Webhooks = []WebhookInput{{URL: "https://a.example/" + strings.Repeat("a", MaxURLLength-len("https://a.example/")), Event: "connection.update"}}
			return in
		}, ""},
		{"url 2049", func() CreateInput {
			in := webCreate()
			in.Webhooks = []WebhookInput{{URL: "https://a.example/" + strings.Repeat("a", MaxURLLength+1-len("https://a.example/")), Event: "connection.update"}}
			return in
		}, "webhooks[0].url"},
		{"secret 256", func() CreateInput {
			in := webCreate()
			in.Webhooks = hooks(1)
			in.Webhooks[0].Secret = ptr(strings.Repeat("s", MaxSecretLength))
			return in
		}, ""},
		{"secret 257", func() CreateInput {
			in := webCreate()
			in.Webhooks = hooks(1)
			in.Webhooks[0].Secret = ptr(strings.Repeat("s", MaxSecretLength+1))
			return in
		}, "webhooks[0].secret"},
		{"secret empty", func() CreateInput {
			in := webCreate()
			in.Webhooks = hooks(1)
			in.Webhooks[0].Secret = ptr("")
			return in
		}, "webhooks[0].secret"},
		{"no url", func() CreateInput { in := webCreate(); in.Webhooks = hooks(2); in.Webhooks[1].URL = ""; return in }, "webhooks[1].url"},
		{"no event", func() CreateInput { in := webCreate(); in.Webhooks = hooks(2); in.Webhooks[1].Event = ""; return in }, "webhooks[1].event"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := s.validateCreate(c.in())
			got := strings.Join(fieldsOf(t, err), ",")
			if got != c.want {
				t.Fatalf("fields = %q, want %q", got, c.want)
			}
		})
	}
}

func TestCreateWebhookCountLimit(t *testing.T) {
	s := &Service{}
	for n, wantCount := range map[int]bool{MaxWebhooks: false, MaxWebhooks + 1: true} {
		in := webCreate()
		in.Webhooks = hooks(n) // more items than events: duplicates are refused too
		_, _, err := s.validateCreate(in)
		hasCount := false
		for _, f := range fieldsOf(t, err) {
			hasCount = hasCount || f == "webhooks"
		}
		if hasCount != wantCount {
			t.Fatalf("%d webhooks: count error %v, want %v (%v)", n, hasCount, wantCount, err)
		}
	}
}

func TestCreateReportsEveryWrongFieldAtOnce(t *testing.T) {
	s := &Service{}
	in := CreateInput{DisplayName: "", Provider: "META_CLOUD", SendOnly: true, Pairing: "code",
		Meta: &core.MetaCredentials{PhoneNumberID: "x", AccessToken: "", AppSecret: "s"}}
	_, _, err := s.validateCreate(in)
	got := strings.Join(fieldsOf(t, err), ",")
	want := "display_name,send_only,pairing,meta_credentials.phone_number_id,meta_credentials.access_token"
	if got != want {
		t.Fatalf("fields = %q, want %q", got, want)
	}
}

// A malformed client id is a field error of the service, next to the other field errors.
func TestCreateReportsABadClientIDWithTheOtherFields(t *testing.T) {
	s := &Service{}
	in := webCreate()
	in.ClientID = "not-a-uuid"
	in.DisplayName = " "
	_, _, err := s.validateCreate(in)
	if got := strings.Join(fieldsOf(t, err), ","); got != "client_id,display_name" {
		t.Fatalf("fields = %q", got)
	}
	e, _ := apierr.As(err)
	for _, fe := range e.Errors {
		if fe.Field == "client_id" && (fe.Message != "must be a UUID." || strings.Contains(fe.Message, "not-a-uuid")) {
			t.Fatalf("message = %q", fe.Message)
		}
	}
	in = webCreate()
	in.ClientID = uuid.NewString()
	if out, _, err := s.validateCreate(in); err != nil || out.clientID.String() != in.ClientID {
		t.Fatalf("valid id: %v %v", out.clientID, err)
	}
}

func TestWebhookMessagesDoNotQuoteTheValue(t *testing.T) {
	s := &Service{}
	in := webCreate()
	in.Webhooks = []WebhookInput{
		{URL: "ftp://user:pw-SECRET@a.example/x", Event: "whatsmeow.SECRET"},
		{URL: "https://a.example/", Event: "connection.update", Secret: ptr(strings.Repeat("S", 300))},
	}
	_, _, err := s.validateCreate(in)
	e, _ := apierr.As(err)
	for _, fe := range e.Errors {
		if strings.Contains(fe.Message, "SECRET") || strings.Contains(fe.Message, "SSS") || strings.Contains(fe.Message, "ftp:") {
			t.Fatalf("%s: message quotes the value: %q", fe.Field, fe.Message)
		}
	}
	if len(e.Errors) != 3 {
		t.Fatalf("errors = %+v", e.Errors)
	}
}

func ptr[T any](v T) *T { return &v }

func TestCheckIdempotencyKey(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   string
		valid bool
	}{
		{"empty is no key", "", true},
		{"one byte", "a", true},
		{"255 bytes", strings.Repeat("k", MaxIdempotencyKeyLength), true},
		{"256 bytes", strings.Repeat("k", MaxIdempotencyKeyLength+1), false},
		{"space 0x20", "a b", false},
		{"delete 0x7F", "a\x7fb", false},
		{"non-ASCII byte", "café", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckIdempotencyKey(tc.key)
			if tc.valid {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			e, ok := apierr.As(err)
			if !ok || e.Status != 400 || e.Code != apierr.CodeInvalidRequest {
				t.Fatalf("want 400 invalid_request, got %v", err)
			}
			if want := "The Idempotency-Key header must have 1 to 255 printable ASCII characters."; e.Detail != want {
				t.Fatalf("detail %q", e.Detail)
			}
		})
	}
}
