package bulk

import (
	"fmt"
	"strings"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/core"
	"calyx/backend/internal/instances"
)

// The anti-spam limits of one bulk job: the most messages that one recipient gets. The core
// (engine.MaxSameTextPerRecipient and engine.MaxCustomPerRecipient) enforces them after it
// normalizes each recipient; the backend repeats them to give a field error first.
const (
	MaxSameTextPerRecipient = 1
	MaxCustomPerRecipient   = 2
)

// Input is a bulk job request: either Recipients with one Text, or Messages. Text is nil when
// the request has no text, so a text next to messages is refused even when it is empty.
type Input struct {
	Recipients []string
	Text       *string
	Messages   []core.BulkMessage
	Pacing     *core.BulkPacing
	// Key is the optional Idempotency-Key. The core replays a call with a key that it knows.
	Key string
}

// coreInput returns the input of the core call.
func (in Input) coreInput() core.BulkInput {
	out := core.BulkInput{Recipients: in.Recipients, Messages: in.Messages, Pacing: in.Pacing, Key: in.Key}
	if in.Text != nil {
		out.Text = *in.Text
	}
	return out
}

// validateInput applies the rules of a bulk input. It returns nil, 413 too_many_messages, or a
// 400 error with every wrong field. A list above MaxMessages is refused before its items are read.
// No message holds a recipient or a text.
func validateInput(in Input) error {
	if err := instances.CheckIdempotencyKey(in.Key); err != nil {
		return err
	}
	var f apierr.Fields
	same := in.Recipients != nil || in.Text != nil
	custom := in.Messages != nil
	switch {
	case same && custom:
		f.Add("messages", "must not be used with recipients and text.")
	case !same && !custom:
		f.Add("messages", "is required when there are no recipients and no text.")
	case same && in.Recipients == nil:
		f.Add("recipients", "is required with text.")
	case same && in.Text == nil:
		f.Add("text", "is required with recipients.")
	}
	if err := f.Err(); err != nil {
		return err
	}
	count, list := len(in.Recipients), "recipients"
	if custom {
		count, list = len(in.Messages), "messages"
	}
	if count == 0 {
		f.Add(list, "must have at least 1 item.")
		return f.Err()
	}
	if count > MaxMessages {
		return apierr.New(413, apierr.CodeTooManyMessages, fmt.Sprintf("A bulk job has at most %d messages.", MaxMessages))
	}
	perRecipient := map[string]int{}
	if same {
		instances.CheckText(&f, "text", *in.Text)
		for i, to := range in.Recipients {
			path := fmt.Sprintf("recipients[%d]", i)
			instances.CheckRecipient(&f, path, to)
			checkRepeat(&f, path, to, perRecipient, MaxSameTextPerRecipient)
		}
	}
	for i, m := range in.Messages {
		path := fmt.Sprintf("messages[%d]", i)
		instances.CheckRecipient(&f, path+".to", m.To)
		checkRepeat(&f, path+".to", m.To, perRecipient, MaxCustomPerRecipient)
		instances.CheckText(&f, path+".text", m.Text)
		if m.DelayMs != nil && *m.DelayMs < 0 {
			f.Add(path+".delay_ms", "must be 0 or more.")
		}
	}
	if p := in.Pacing; p != nil {
		checkRange(&f, "pacing.delay", p.Delay)
		checkRange(&f, "pacing.pause_every", p.PauseEvery)
		checkRange(&f, "pacing.pause_duration", p.PauseDuration)
	}
	return f.Err()
}

// checkRepeat counts the messages to one recipient and refuses the ones above limit. It compares
// the trimmed text; the core also finds two forms of one number.
func checkRepeat(f *apierr.Fields, field, to string, count map[string]int, limit int) {
	to = strings.TrimSpace(to)
	if to == "" {
		return
	}
	if count[to]++; count[to] > limit {
		f.Add(field, fmt.Sprintf("is a recipient with more than %d messages in this job. Calyx does not allow it: "+
			"it looks like spam.", limit))
	}
}

// checkRange refuses a negative bound. The core enforces its own minimums.
func checkRange(f *apierr.Fields, field string, r *core.Range) {
	if r == nil {
		return
	}
	if r.Min < 0 {
		f.Add(field+".min", "must be 0 or more.")
	}
	if r.Max < 0 {
		f.Add(field+".max", "must be 0 or more.")
	}
}
