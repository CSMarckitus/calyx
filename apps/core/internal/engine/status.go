package engine

import (
	"strings"

	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// StatusName is the short name of a status ("CONNECTED"), used in the database and in events.
func StatusName(s corev1.InstanceStatus) string {
	return strings.TrimPrefix(s.String(), "INSTANCE_STATUS_")
}

// StatusFromName is the inverse of StatusName. Unknown names are DISCONNECTED.
func StatusFromName(name string) corev1.InstanceStatus {
	if value, ok := corev1.InstanceStatus_value["INSTANCE_STATUS_"+name]; ok {
		return corev1.InstanceStatus(value)
	}
	return corev1.InstanceStatus_INSTANCE_STATUS_DISCONNECTED
}
