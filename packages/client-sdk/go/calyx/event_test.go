package calyx_test

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// Each Go struct and its proto message must have the same JSON names, and omitempty exactly
// where the proto field is optional.
func TestPayloadStructsMatchProtoMessages(t *testing.T) {
	pairs := []struct {
		goStruct any
		message  proto.Message
	}{
		{calyx.ConnectionUpdate{}, &corev1.ConnectionUpdate{}},
		{calyx.MessageReceived{}, &corev1.MessageReceived{}},
		{calyx.MessageStatus{}, &corev1.MessageStatus{}},
		{calyx.BulkFinished{}, &corev1.BulkFinished{}},
	}
	for _, pair := range pairs {
		goType := reflect.TypeOf(pair.goStruct)
		fields := pair.message.ProtoReflect().Descriptor().Fields()
		t.Run(goType.Name(), func(t *testing.T) {
			goFields := map[string]bool{} // JSON name -> omitempty
			for i := range goType.NumField() {
				name, options, _ := strings.Cut(goType.Field(i).Tag.Get("json"), ",")
				goFields[name] = options == "omitempty"
				field := fields.ByJSONName(name)
				if field == nil {
					t.Errorf("Go field %q has no field in the proto message", name)
					continue
				}
				if goFields[name] != field.HasPresence() {
					t.Errorf("field %q: omitempty is %v in Go, optional is %v in the proto", name, goFields[name], field.HasPresence())
				}
				isPointer := goType.Field(i).Type.Kind() == reflect.Pointer
				if isPointer != field.HasOptionalKeyword() {
					t.Errorf("field %q: pointer is %v in Go, optional is %v in the proto", name, isPointer, field.HasOptionalKeyword())
				}
			}
			for i := range fields.Len() {
				name := fields.Get(i).JSONName()
				if _, ok := goFields[name]; !ok {
					t.Errorf("proto field %q has no field in the Go struct", name)
				}
			}
		})
	}
}

func enumNames(values protoreflect.EnumValueDescriptors, prefix string, lower bool) []string {
	var names []string
	for i := range values.Len() {
		name := string(values.Get(i).Name())
		if strings.HasSuffix(name, "_UNSPECIFIED") {
			continue
		}
		name = strings.TrimPrefix(name, prefix)
		if lower {
			name = strings.ToLower(name)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func TestStatusConstantsMatchEnums(t *testing.T) {
	tests := []struct {
		name      string
		constants []string
		enum      []string
	}{
		{"ConnectionStatus", []string{
			string(calyx.ConnectionConnected), string(calyx.ConnectionConnecting), string(calyx.ConnectionNeedsAuth),
			string(calyx.ConnectionDisconnected), string(calyx.ConnectionError),
		}, enumNames(corev1.InstanceStatus_INSTANCE_STATUS_UNSPECIFIED.Descriptor().Values(), "INSTANCE_STATUS_", false)},
		{"BulkJobStatus", []string{
			string(calyx.BulkJobQueued), string(calyx.BulkJobRunning), string(calyx.BulkJobCompleted),
			string(calyx.BulkJobCancelled), string(calyx.BulkJobFailed),
		}, enumNames(corev1.BulkJobStatus_BULK_JOB_STATUS_UNSPECIFIED.Descriptor().Values(), "BULK_JOB_STATUS_", false)},
		{"DeliveryStatus", []string{
			string(calyx.DeliverySent), string(calyx.DeliveryDelivered), string(calyx.DeliveryRead), string(calyx.DeliveryFailed),
		}, enumNames(corev1.MessageDeliveryStatus_MESSAGE_DELIVERY_STATUS_UNSPECIFIED.Descriptor().Values(), "MESSAGE_DELIVERY_STATUS_", true)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slices.Sort(tt.constants)
			if !slices.Equal(tt.constants, tt.enum) {
				t.Errorf("constants give %v, the enum has %v", tt.constants, tt.enum)
			}
		})
	}
}

type eventCase struct {
	Name     string `json:"name"`
	Body     string `json:"body"`
	Expected struct {
		Event   string          `json:"event"`
		Payload json.RawMessage `json:"payload"`
		Error   string          `json:"error"`
	} `json:"expected"`
}

func decodeEventCase(e calyx.Event, name string) (any, error) {
	switch name {
	case "connection.update":
		return e.ConnectionUpdate()
	case "message.received":
		return e.MessageReceived()
	case "message.status":
		return e.MessageStatus()
	default:
		return e.BulkFinished()
	}
}

func jsonEqual(t *testing.T, got any, want json.RawMessage) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	if err := json.Unmarshal(gotJSON, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("payload %s, want %s", gotJSON, want)
	}
}

func TestSharedEventCases(t *testing.T) {
	data, err := os.ReadFile("../../testdata/events.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []eventCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	for _, c := range file.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var e calyx.Event
			if err := json.Unmarshal([]byte(c.Body), &e); err != nil {
				t.Fatal(err)
			}
			name := c.Expected.Event
			if c.Expected.Error != "" {
				// The envelope check runs before the event name, so any one method shows the error.
				name = "connection.update"
			}
			got, err := decodeEventCase(e, name)
			if c.Expected.Error != "" {
				if !calyx.HasCode(err, calyx.Code(c.Expected.Error)) {
					t.Fatalf("error %v, want code %s", err, c.Expected.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if e.Event != c.Expected.Event {
				t.Errorf("event %q, want %q", e.Event, c.Expected.Event)
			}
			jsonEqual(t, got, c.Expected.Payload)
		})
	}
}

func TestTimestampKeepsFullPrecision(t *testing.T) {
	body := `{"id":"e","event":"connection.update","instance":"main","timestamp":"2026-11-20T10:00:00.123456789Z","data":{}}`
	var e calyx.Event
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 11, 20, 10, 0, 0, 123456789, time.UTC)
	if !e.Timestamp.Equal(want) {
		t.Errorf("timestamp %v, want %v", e.Timestamp, want)
	}
}

func TestEventMethodsGiveInvalidEvent(t *testing.T) {
	valid := calyx.Event{
		ID: "e", Event: "connection.update", Instance: "main",
		Timestamp: time.Now(), Data: json.RawMessage(`{"status":"CONNECTED"}`),
	}
	tests := []struct {
		name   string
		change func(*calyx.Event)
	}{
		{"wrong event name", func(e *calyx.Event) { e.Event = "message.status" }},
		{"empty id", func(e *calyx.Event) { e.ID = "" }},
		{"empty instance", func(e *calyx.Event) { e.Instance = "" }},
		{"zero timestamp", func(e *calyx.Event) { e.Timestamp = time.Time{} }},
		{"no data", func(e *calyx.Event) { e.Data = nil }},
		{"data of the wrong type", func(e *calyx.Event) { e.Data = json.RawMessage(`"x"`) }},
		{"data that is not JSON", func(e *calyx.Event) { e.Data = json.RawMessage(`{`) }},
	}
	if _, err := valid.ConnectionUpdate(); err != nil {
		t.Fatalf("valid event: %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := valid
			tt.change(&e)
			_, err := e.ConnectionUpdate()
			var ce *calyx.Error
			if !errors.As(err, &ce) || ce.Code != calyx.CodeInvalidEvent {
				t.Fatalf("error %v, want invalid_event", err)
			}
			if ce.Retry != calyx.RetryDoNotRetry || ce.Source != calyx.SourceClient || ce.Status != 0 {
				t.Errorf("retry %q, source %q, status %d; want do_not_retry, client, 0", ce.Retry, ce.Source, ce.Status)
			}
		})
	}
}
