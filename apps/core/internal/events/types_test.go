package events_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"calyx/core/internal/engine"
	"calyx/core/internal/events"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// The event.proto messages describe the JSON payloads of the Go structs. This test keeps both
// sides equal: the same JSON names, and omitempty exactly where the field is optional.
func TestPayloadStructsMatchProtoMessages(t *testing.T) {
	pairs := []struct {
		goStruct any
		message  proto.Message
	}{
		{events.ConnectionUpdate{}, &corev1.ConnectionUpdate{}},
		{events.MessageReceived{}, &corev1.MessageReceived{}},
		{events.MessageStatus{}, &corev1.MessageStatus{}},
		{events.BulkFinished{}, &corev1.BulkFinished{}},
	}
	for _, pair := range pairs {
		goType := reflect.TypeOf(pair.goStruct)
		fields := pair.message.ProtoReflect().Descriptor().Fields()
		t.Run(goType.Name(), func(t *testing.T) {
			goFields := map[string]bool{} // JSON name -> omitempty
			for i := range goType.NumField() {
				name, options, _ := strings.Cut(goType.Field(i).Tag.Get("json"), ",")
				goFields[name] = options == "omitempty"
			}
			for name, omitEmpty := range goFields {
				field := fields.ByJSONName(name)
				if field == nil {
					t.Errorf("Go field %q has no field in the proto message", name)
					continue
				}
				if omitEmpty != field.HasPresence() {
					t.Errorf("field %q: omitempty is %v in Go, optional is %v in the proto", name, omitEmpty, field.HasPresence())
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

func TestJobStatusesAreBulkJobStatusNames(t *testing.T) {
	statuses := []engine.JobStatus{
		engine.JobQueued, engine.JobRunning, engine.JobCompleted, engine.JobCancelled, engine.JobFailed,
	}
	for _, status := range statuses {
		if _, ok := corev1.BulkJobStatus_value["BULK_JOB_STATUS_"+string(status)]; !ok {
			t.Errorf("JobStatus %q is not a BulkJobStatus value", status)
		}
	}
}

func TestDeliveryConstantsMatchMessageDeliveryStatus(t *testing.T) {
	var fromConstants []string
	for _, delivery := range []string{events.DeliverySent, events.DeliveryDelivered, events.DeliveryRead, events.DeliveryFailed} {
		fromConstants = append(fromConstants, "MESSAGE_DELIVERY_STATUS_"+strings.ToUpper(delivery))
	}
	var fromEnum []string
	for name := range corev1.MessageDeliveryStatus_value {
		if name != "MESSAGE_DELIVERY_STATUS_UNSPECIFIED" {
			fromEnum = append(fromEnum, name)
		}
	}
	slices.Sort(fromConstants)
	slices.Sort(fromEnum)
	if !slices.Equal(fromConstants, fromEnum) {
		t.Errorf("Delivery constants give %v, the enum has %v", fromConstants, fromEnum)
	}
}
