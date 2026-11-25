package whatsapp_test

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	waevents "go.mau.fi/whatsmeow/types/events"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/providers/whatsapp"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

var (
	phoneJID = types.NewJID("34600111222", types.DefaultUserServer)
	lidJID   = types.NewJID("123456789012345", types.HiddenUserServer)
	groupJID = types.NewJID("120363000000000000", types.GroupServer)
)

// knownAlts plays whatsmeow's LID map: it knows the pair phoneJID <-> lidJID.
func knownAlts(jid types.JID) types.JID {
	switch jid {
	case phoneJID:
		return lidJID
	case lidJID:
		return phoneJID
	}
	return types.EmptyJID
}

func TestNormalizeRecipient(t *testing.T) {
	valid := map[string]string{
		"34600111222":                  "34600111222@s.whatsapp.net",
		"34600111222@s.whatsapp.net":   "34600111222@s.whatsapp.net",
		"34600111222:5@s.whatsapp.net": "34600111222@s.whatsapp.net",
		"123456789012345@lid":          "123456789012345@lid",
		"120363000000000000@g.us":      "120363000000000000@g.us",
	}
	for in, want := range valid {
		got, err := whatsapp.NormalizeRecipient(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "+34 600", "123", "34600111222@broadcast", "status@broadcast", "hello"} {
		if _, err := whatsapp.NormalizeRecipient(in); calyxerr.CodeOf(err) != codes.InvalidArgument {
			t.Errorf("%q: got %v", in, err)
		}
	}
}

func incoming(chat, sender, senderAlt types.JID, message *waE2E.Message) *waevents.Message {
	return &waevents.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: sender, SenderAlt: senderAlt, IsGroup: chat.Server == types.GroupServer},
			ID:            "MSG1", Type: "text", PushName: "Ana", Timestamp: time.Unix(1700000000, 0),
		},
		Message: message,
	}
}

func TestAReplyFromALIDCarriesThePhoneNumber(t *testing.T) {
	evt := incoming(lidJID, lidJID, types.EmptyJID, &waE2E.Message{Conversation: proto.String("YES")})
	got, ok := whatsapp.MessageReceived(evt, knownAlts)
	if !ok {
		t.Fatal("skipped")
	}
	if got.ChatID != "123456789012345@lid" || got.ChatAltID != "34600111222@s.whatsapp.net" ||
		got.From != "123456789012345@lid" || got.FromAltID != "34600111222@s.whatsapp.net" {
		t.Fatalf("got %+v", got)
	}
	if got.Text != "YES" || got.MessageID != "MSG1" || got.PushName != "Ana" || got.Timestamp != 1700000000 || got.IsGroup {
		t.Fatalf("got %+v", got)
	}
}

func TestSenderAltFromWhatsAppWinsOverTheLookup(t *testing.T) {
	other := types.NewJID("34699999999", types.DefaultUserServer)
	evt := incoming(lidJID, lidJID, other, &waE2E.Message{Conversation: proto.String("hi")})
	got, _ := whatsapp.MessageReceived(evt, knownAlts)
	if got.FromAltID != "34699999999@s.whatsapp.net" || got.ChatAltID != "34699999999@s.whatsapp.net" {
		t.Fatalf("got %+v", got)
	}
}

func TestAGroupMessageHasNoChatAlt(t *testing.T) {
	evt := incoming(groupJID, phoneJID, types.EmptyJID, &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("hello all")},
	})
	got, _ := whatsapp.MessageReceived(evt, knownAlts)
	if !got.IsGroup || got.ChatAltID != "" || got.FromAltID != "123456789012345@lid" || got.Text != "hello all" {
		t.Fatalf("got %+v", got)
	}
}

func TestACaptionIsTheTextAndTheMediaTypeIsTheType(t *testing.T) {
	evt := incoming(phoneJID, phoneJID, types.EmptyJID, &waE2E.Message{
		ImageMessage: &waE2E.ImageMessage{Caption: proto.String("look")},
	})
	evt.Info.Type, evt.Info.MediaType = "media", "image"
	got, _ := whatsapp.MessageReceived(evt, knownAlts)
	if got.Text != "look" || got.Type != "image" {
		t.Fatalf("got %+v", got)
	}
}

func TestOwnMessagesAreNotReceived(t *testing.T) {
	evt := incoming(phoneJID, phoneJID, types.EmptyJID, &waE2E.Message{Conversation: proto.String("x")})
	evt.Info.IsFromMe = true
	if _, ok := whatsapp.MessageReceived(evt, knownAlts); ok {
		t.Fatal("an own message was reported as received")
	}
}

func TestReceiptsBecomeDeliveryStatuses(t *testing.T) {
	cases := map[types.ReceiptType]string{
		types.ReceiptTypeDelivered:   "delivered",
		types.ReceiptTypeRead:        "read",
		types.ReceiptTypeReadSelf:    "read",
		types.ReceiptTypePlayed:      "read",
		types.ReceiptTypeServerError: "failed",
		types.ReceiptTypeRetry:       "",
		types.ReceiptTypeSender:      "",
	}
	for receiptType, want := range cases {
		evt := &waevents.Receipt{
			MessageSource: types.MessageSource{Chat: phoneJID, Sender: phoneJID},
			MessageIDs:    []types.MessageID{"A", "B"},
			Type:          receiptType,
		}
		got := whatsapp.MessageStatuses(evt, knownAlts)
		if want == "" {
			if len(got) != 0 {
				t.Errorf("%q: got %+v", receiptType, got)
			}
			continue
		}
		if len(got) != 2 || got[0].Status != want || got[1].MessageID != "B" || got[0].ChatAltID != "123456789012345@lid" {
			t.Errorf("%q: got %+v", receiptType, got)
		}
	}
}

func TestDisconnectionReasons(t *testing.T) {
	cases := []struct {
		evt       any
		status    corev1.InstanceStatus
		reconnect bool
	}{
		{&waevents.LoggedOut{}, corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH, false},
		{&waevents.StreamReplaced{}, corev1.InstanceStatus_INSTANCE_STATUS_ERROR, false},
		{&waevents.TemporaryBan{}, corev1.InstanceStatus_INSTANCE_STATUS_ERROR, false},
		{&waevents.ClientOutdated{}, corev1.InstanceStatus_INSTANCE_STATUS_ERROR, false},
		{&waevents.Disconnected{}, corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED, true},
		{&waevents.ConnectFailure{Reason: waevents.ConnectFailureGeneric}, corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED, true},
	}
	for _, c := range cases {
		status, reconnect, ok := whatsapp.DisconnectStatus(c.evt)
		if !ok || status != c.status || reconnect != c.reconnect {
			t.Errorf("%T: got %v, %v, %v", c.evt, status, reconnect, ok)
		}
	}
	if _, _, ok := whatsapp.DisconnectStatus(&waevents.Connected{}); ok {
		t.Error("Connected is not a disconnection")
	}
}
