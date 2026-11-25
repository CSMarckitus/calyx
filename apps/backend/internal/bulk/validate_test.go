package bulk_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"calyx/backend/internal/bulk"
	"calyx/backend/internal/core"
	"calyx/backend/internal/instances"
)

func i32(v int32) *int32 { return &v }

// Each limit is accepted at its value and refused one step past it, with the field path.
func TestCreateLimits(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	maxText := strings.Repeat("é", instances.MaxTextLength)
	longText := maxText + "é"
	maxTo := strings.Repeat("1", instances.MaxRecipientLength)
	longTo := maxTo + "1"
	cases := []struct {
		name string
		in   bulk.Input
		want string // "" when the input is valid
	}{
		{"text at the limit", bulk.Input{Recipients: []string{maxTo}, Text: ptr(maxText)}, ""},
		{"text past the limit", bulk.Input{Recipients: []string{"34600111222"}, Text: ptr(longText)}, "text"},
		{"recipient past the limit", bulk.Input{Recipients: []string{"34600111222", longTo}, Text: ptr("x")}, "recipients[1]"},
		{"message at the limits", bulk.Input{Messages: []core.BulkMessage{{To: maxTo, Text: maxText, DelayMs: i32(0)}}}, ""},
		{"message text past the limit", bulk.Input{Messages: []core.BulkMessage{{To: "1", Text: "x"}, {To: "1", Text: longText}}}, "messages[1].text"},
		{"message to past the limit", bulk.Input{Messages: []core.BulkMessage{{To: longTo, Text: "x"}}}, "messages[0].to"},
		{"negative delay", bulk.Input{Messages: []core.BulkMessage{{To: "1", Text: "x", DelayMs: i32(-1)}}}, "messages[0].delay_ms"},
		{"pacing at 0", bulk.Input{Recipients: []string{"1"}, Text: ptr("x"), Pacing: &core.BulkPacing{
			Delay: &core.Range{}, PauseEvery: &core.Range{}, PauseDuration: &core.Range{},
		}}, ""},
		{"negative pacing", bulk.Input{Recipients: []string{"1"}, Text: ptr("x"), Pacing: &core.BulkPacing{
			Delay: &core.Range{Min: -1}, PauseEvery: &core.Range{Max: -1}, PauseDuration: &core.Range{Min: -1},
		}}, "pacing.delay.min,pacing.pause_every.max,pacing.pause_duration.min"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := e.svc.Create(context.Background(), e.owner, row.ID, c.in)
			if c.want == "" {
				if err != nil {
					t.Fatalf("valid input: %v", err)
				}
				return
			}
			requireFields(t, err, strings.Split(c.want, ",")...)
		})
	}
}

func TestCreateFormErrors(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	cases := []struct {
		name string
		in   bulk.Input
		want []string
	}{
		{"both forms", bulk.Input{Recipients: []string{"1"}, Text: ptr("x"), Messages: []core.BulkMessage{{To: "1", Text: "x"}}}, []string{"messages"}},
		{"no form", bulk.Input{}, []string{"messages"}},
		{"empty text with messages", bulk.Input{Text: ptr(""), Messages: []core.BulkMessage{{To: "1", Text: "x"}}}, []string{"messages"}},
		{"text without recipients", bulk.Input{Text: ptr("x")}, []string{"recipients"}},
		{"recipients without text", bulk.Input{Recipients: []string{"1"}}, []string{"text"}},
		{"empty recipients", bulk.Input{Recipients: []string{}, Text: ptr("x")}, []string{"recipients"}},
		{"empty messages", bulk.Input{Messages: []core.BulkMessage{}}, []string{"messages"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := e.svc.Create(context.Background(), e.owner, row.ID, c.in)
			requireFields(t, err, c.want...)
		})
	}
	if len(e.fc.BulkSameRequests())+len(e.fc.BulkCustomRequests()) != 0 {
		t.Fatal("a core request was sent")
	}
}

func TestCreateReportsEveryWrongItemAtOnce(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	in := bulk.Input{Messages: []core.BulkMessage{
		{To: "", Text: "secret-text"}, {To: "34600111222", Text: "ok"}, {To: "secret-to", Text: " "},
	}}
	_, err := e.svc.Create(context.Background(), e.owner, row.ID, in)
	requireFields(t, err, "messages[0].to", "messages[2].text")
	ae, _ := apierrAs(err)
	for _, fe := range ae.Errors {
		if strings.Contains(fe.Message, "secret") {
			t.Fatalf("the message quotes the value: %q", fe.Message)
		}
	}
}

// Anti-spam, as in the core: a recipient gets at most bulk.MaxSameTextPerRecipient messages of a
// job with one text, and at most bulk.MaxCustomPerRecipient messages of a custom job. Spaces at
// the start and end do not make another recipient.
func TestCreateRefusesTooManyMessagesToOneRecipient(t *testing.T) {
	e := newEnv(t)
	row := e.instance(t)
	_, err := e.svc.Create(context.Background(), e.owner, row.ID, bulk.Input{Recipients: []string{"1", "2", " 1"}, Text: ptr("x")})
	requireFields(t, err, "recipients[2]")
	if ae, _ := apierrAs(err); !strings.Contains(ae.Errors[0].Message, "spam") {
		t.Fatalf("message = %q", ae.Errors[0].Message)
	}
	two := []core.BulkMessage{{To: "1", Text: "a"}, {To: "2", Text: "b"}, {To: "1", Text: "c"}}
	if _, err := e.svc.Create(context.Background(), e.owner, row.ID, bulk.Input{Messages: two}); err != nil {
		t.Fatalf("two messages to one recipient: %v", err)
	}
	three := slices.Concat(two, []core.BulkMessage{{To: "1 ", Text: "d"}, {To: "1", Text: "e"}})
	_, err = e.svc.Create(context.Background(), e.owner, row.ID, bulk.Input{Messages: three})
	requireFields(t, err, "messages[3].to", "messages[4].to")
	if bulk.MaxSameTextPerRecipient != 1 || bulk.MaxCustomPerRecipient != 2 {
		t.Fatal("the limits differ from the core")
	}
}
