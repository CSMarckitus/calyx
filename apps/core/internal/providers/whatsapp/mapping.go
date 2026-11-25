// Package whatsapp is the WhatsApp Web provider, on top of whatsmeow.
package whatsapp

import (
	"regexp"

	"go.mau.fi/whatsmeow/types"
	waevents "go.mau.fi/whatsmeow/types/events"
	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/events"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// The pure translations between whatsmeow and Calyx, kept apart from the client so they can be
// tested without a connection.

var phoneNumber = regexp.MustCompile(`^\d{7,15}$`)

// NormalizeRecipient accepts a phone number with country code, or a user, LID or group JID.
func NormalizeRecipient(to string) (string, error) {
	if phoneNumber.MatchString(to) {
		return types.NewJID(to, types.DefaultUserServer).String(), nil
	}
	jid, err := types.ParseJID(to)
	if err == nil && jid.User != "" {
		switch jid.Server {
		case types.DefaultUserServer, types.HiddenUserServer, types.GroupServer:
			return jid.ToNonAD().String(), nil
		}
	}
	return "", calyxerr.New(codes.InvalidArgument,
		"'%s' is not a valid recipient. Use a phone number with country code (34600111222) or a JID "+
			"(34600111222@s.whatsapp.net, ...@lid, ...@g.us).", to)
}

// AltLookup returns the other address of a user (phone number <-> LID), or an empty JID.
type AltLookup func(types.JID) types.JID

// senderAlt prefers the alternative address WhatsApp sent with the message over the local map.
func senderAlt(src types.MessageSource, alt AltLookup) types.JID {
	if !src.SenderAlt.IsEmpty() {
		return src.SenderAlt.ToNonAD()
	}
	return alt(src.Sender.ToNonAD())
}

// chatAlt is only known for one-to-one chats; a group has no phone number or LID.
func chatAlt(src types.MessageSource, alt AltLookup) types.JID {
	if src.IsGroup {
		return types.EmptyJID
	}
	if src.Chat.User == src.Sender.User && !src.SenderAlt.IsEmpty() {
		return src.SenderAlt.ToNonAD()
	}
	return alt(src.Chat.ToNonAD())
}

func jidString(jid types.JID) string {
	if jid.IsEmpty() {
		return ""
	}
	return jid.String()
}

// MessageReceived builds the Calyx event of an incoming message. ok is false for messages sent
// from this account, which are not "received".
func MessageReceived(evt *waevents.Message, alt AltLookup) (payload events.MessageReceived, ok bool) {
	info := evt.Info
	if info.IsFromMe {
		return payload, false
	}
	msg := evt.Message
	text := msg.GetConversation()
	for _, candidate := range []string{
		msg.GetExtendedTextMessage().GetText(),
		msg.GetImageMessage().GetCaption(),
		msg.GetVideoMessage().GetCaption(),
		msg.GetDocumentMessage().GetCaption(),
	} {
		if text == "" {
			text = candidate
		}
	}
	messageType := info.Type
	if info.MediaType != "" {
		messageType = info.MediaType
	}

	return events.MessageReceived{
		MessageID: info.ID,
		ChatID:    info.Chat.ToNonAD().String(),
		ChatAltID: jidString(chatAlt(info.MessageSource, alt)),
		From:      info.Sender.ToNonAD().String(),
		FromAltID: jidString(senderAlt(info.MessageSource, alt)),
		IsGroup:   info.IsGroup,
		PushName:  info.PushName,
		Timestamp: info.Timestamp.Unix(),
		Type:      messageType,
		Text:      text,
	}, true
}

var receiptStatus = map[types.ReceiptType]string{
	types.ReceiptTypeDelivered:   events.DeliveryDelivered,
	types.ReceiptTypeRead:        events.DeliveryRead,
	types.ReceiptTypeReadSelf:    events.DeliveryRead,
	types.ReceiptTypePlayed:      events.DeliveryRead,
	types.ReceiptTypeServerError: events.DeliveryFailed,
}

// MessageStatuses builds one Calyx event per message of a receipt. Receipt types that say nothing
// about delivery (retry, sender, ...) give none.
func MessageStatuses(evt *waevents.Receipt, alt AltLookup) []events.MessageStatus {
	status, known := receiptStatus[evt.Type]
	if !known {
		return nil
	}
	chat := evt.Chat.ToNonAD().String()
	altID := jidString(chatAlt(evt.MessageSource, alt))
	statuses := make([]events.MessageStatus, 0, len(evt.MessageIDs))
	for _, id := range evt.MessageIDs {
		statuses = append(statuses, events.MessageStatus{MessageID: id, ChatID: chat, ChatAltID: altID, Status: status})
	}
	return statuses
}

// DisconnectStatus says what a whatsmeow event means for the instance: its new status and whether
// to reconnect. ok is false for events that are not about losing the connection.
func DisconnectStatus(evt any) (status corev1.InstanceStatus, reconnect bool, ok bool) {
	switch evt.(type) {
	case *waevents.LoggedOut:
		// Unlinked from the phone: only a new pairing helps.
		return corev1.InstanceStatus_INSTANCE_STATUS_NEEDS_AUTH, false, true
	case *waevents.StreamReplaced, *waevents.TemporaryBan, *waevents.ClientOutdated:
		// Another client took the session, the account is banned, or this client is too old:
		// reconnecting would only make things worse.
		return corev1.InstanceStatus_INSTANCE_STATUS_ERROR, false, true
	case *waevents.Disconnected, *waevents.ConnectFailure:
		return corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED, true, true
	}
	return 0, false, false
}
