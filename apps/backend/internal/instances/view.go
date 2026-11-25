package instances

import "calyx/backend/internal/database/gen"

// View is an instance row with its webhooks. The HTTP API maps it to its resource.
type View struct {
	gen.Instance
	Webhooks    []WebhookView
	StatusStale bool // the core has been unreachable, so the stored status may be old
}

// WebhookView is a webhook as the API shows it: the secret never leaves the backend.
type WebhookView struct {
	URL, Event string
	HasSecret  bool
}
