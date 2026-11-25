package engine

import (
	"crypto/sha256"
	"encoding/binary"
	"strings"
)

// The fingerprint is a SHA-256 over a fixed byte encoding, never over proto bytes: those are not
// fixed across versions, and a stored fingerprint must keep its meaning after an upgrade.
// Each value is a field: its length as an unsigned varint, then its bytes.

func singleFingerprint(recipient, text string) []byte {
	var b []byte
	b = appendString(b, "single")
	b = appendString(b, recipient)
	b = appendString(b, strings.TrimSpace(text))
	return digest(b)
}

// bulkFingerprint expects normalized recipients. For BulkSame the message text comes from the
// first message, because the job sends one text to all.
func bulkFingerprint(kind BulkKind, messages []BulkMessageInput, pacing BulkPacingOptions) []byte {
	var b []byte
	if kind == BulkSame {
		b = appendString(b, "bulk-same")
		b = appendInt(b, len(messages))
		text := ""
		for i, m := range messages {
			b = appendString(b, m.To)
			if i == 0 {
				text = m.Text
			}
		}
		b = appendString(b, strings.TrimSpace(text))
	} else {
		b = appendString(b, "bulk-custom")
		b = appendInt(b, len(messages))
		for _, m := range messages {
			b = appendString(b, m.To)
			b = appendString(b, strings.TrimSpace(m.Text))
			b = appendPresence(b, m.DelayMs != nil)
			if m.DelayMs != nil {
				b = appendInt(b, *m.DelayMs)
			}
		}
	}
	for _, r := range []*Range{pacing.Delay, pacing.PauseEvery, pacing.PauseDuration} {
		b = appendPresence(b, r != nil)
		if r != nil {
			b = appendInt(b, r.Min)
			b = appendInt(b, r.Max)
		}
	}
	return digest(b)
}

func appendField(b, value []byte) []byte {
	b = binary.AppendUvarint(b, uint64(len(value)))
	return append(b, value...)
}

func appendString(b []byte, s string) []byte {
	return appendField(b, []byte(s))
}

func appendInt(b []byte, n int) []byte {
	return appendField(b, binary.BigEndian.AppendUint64(nil, uint64(int64(n))))
}

func appendPresence(b []byte, present bool) []byte {
	if present {
		return appendField(b, []byte{1})
	}
	return appendField(b, []byte{0})
}

func digest(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}
