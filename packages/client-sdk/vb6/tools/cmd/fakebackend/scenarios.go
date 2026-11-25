package main

// The fixed values of scenario 1. TestCases.bas in the VB6 tests uses the same values.
const (
	instanceID    = "00000000-0000-4000-8000-000000000001"
	clientID      = "00000000-0000-4000-8000-0000000000c1"
	jobID         = "00000000-0000-4000-8000-0000000000b1"
	sendMessageID = "3EB0C767D0A1B2C3D4E5"
	timeChanged   = "2026-11-20T10:00:00Z"
	timeCreated   = "2026-11-20T09:00:00Z"
	timeFinish    = "2026-11-20T10:30:00Z"
	timeEmailed   = "2026-11-20T10:05:00Z"
)

// instanceJSON builds an instance as the backend answers it. Scenario 6 sets every
// nullable field to null.
func instanceJSON(id string, nulls bool) map[string]any {
	inst := map[string]any{
		"id":                id,
		"client_id":         clientID,
		"display_name":      "Shop",
		"provider":          "META_CLOUD",
		"send_only":         true,
		"enabled":           true,
		"state":             "active",
		"status":            "CONNECTED",
		"status_changed_at": timeChanged,
		"status_stale":      false,
		"device_identity":   "34600000000:1@s.whatsapp.net",
		"locale":            "es",
		"phone_number_id":   "1234567890",
		"webhooks": []any{map[string]any{
			"url": "https://example.invalid/hook", "event": "message.received", "has_secret": true,
		}},
		"alert": map[string]any{"kind": "disconnected", "since": timeChanged, "emailed_at": timeEmailed},
	}
	if nulls {
		for _, k := range []string{"status", "status_changed_at", "device_identity", "locale", "phone_number_id", "alert"} {
			inst[k] = nil
		}
		inst["webhooks"] = []any{}
		inst["status_stale"] = true
	}
	return inst
}

// jobJSON builds a bulk job. Scenario 6 sets the nullable field to null.
func jobJSON(id string, nulls bool) map[string]any {
	job := map[string]any{
		"id":                  id,
		"instance_id":         instanceID,
		"status":              "RUNNING",
		"total_messages":      10,
		"sent_messages":       4,
		"failed_messages":     1,
		"progress_percent":    50.5,
		"estimated_finish_at": timeFinish,
		"created_at":          timeCreated,
		"replayed":            true,
	}
	if nulls {
		job["estimated_finish_at"] = nil
		job["replayed"] = false
	}
	return job
}
