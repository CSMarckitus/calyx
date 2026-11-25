package httpapi

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/mcp"
)

// mcpPath is the route of the MCP transport. apiMiddleware authenticates it with an API key only.
const mcpPath = "/mcp"

const mcpInstructions = "Calyx sends WhatsApp messages through the instances of your organization. " +
	"One message to one recipient: send_message. Several recipients: create_bulk_job. " +
	"Send an Idempotency-Key and reuse it only for a retry. " +
	"Calyx refuses the same text to the same recipient within 10 minutes."

// mcpRequestIDHeader carries the request id of /mcp to each tool call. outer sets it for POST /mcp.
const mcpRequestIDHeader = "X-Calyx-Mcp-Request-Id"

// mcpRequestIDKey is the private context key of that id. Only outerMux.ServeHTTP sets it, so a
// header on a network request cannot reach the log.
type mcpRequestIDKeyType struct{}

var mcpRequestIDKey = mcpRequestIDKeyType{}

// attachMCP mounts the MCP server on api and returns the API to register the operations on.
// The options leave out AllowedOrigins and PublicURL: /mcp needs a Bearer API key and Huma never
// forwards a cookie, so a web page cannot use the visitor's session. Both come back with OAuth 2.1.
func attachMCP(api huma.API) huma.API {
	api, _ = mcp.Attach(api, mcp.Options{
		Name:           "calyx",
		Path:           mcpPath,
		ForwardHeaders: []string{"Authorization", mcpRequestIDHeader},
		Instructions:   mcpInstructions,
	})
	return api
}

// asTool exposes op as the MCP tool name. The mark is operation metadata, not OpenAPI.
func asTool(op huma.Operation, name, description string) huma.Operation {
	return mcp.WithTool(op, mcp.WithName(name), mcp.WithDescription(description))
}

// The tool descriptions are the text that an agent reads. Each one lists only the codes that an
// agent can act on, each with its action.
const (
	toolListInstances = "The instances that you can use, with their status. A message can go out when " +
		"`state` is `active`, `status` is `CONNECTED` and `status_stale` is false."

	toolGetInstance = "One instance and its status, without a list of all instances of a large " +
		"organization. Error `not_found`: the instance does not exist for this API key. Do not retry."

	toolSendMessage = "Send one text to one recipient (international format, digits only, for example " +
		"`34600111222`). For several recipients, use `create_bulk_job`. Send an `Idempotency-Key` and " +
		"reuse it only for a retry. Calyx refuses the same text to the same recipient within 10 minutes. " +
		"Error codes and actions: " +
		"`not_found`: the instance does not exist for this API key. Do not retry. " +
		"`instance_not_connected`, `instance_not_ready`: the instance cannot send now. Tell a person. " +
		"`instance_queue_full`: the queue is full. Wait for the seconds that the error text gives, then retry with the same key. " +
		"`send_in_progress`: wait for the seconds that the error text gives, then retry with the same key. " +
		"`duplicate_message`: the core refused a repeat. Do not send it again. " +
		"`idempotency_key_conflict`: use a new key for a new message. " +
		"`idempotency_key_outcome_unknown`: check the chat with a person before a new send. " +
		"`send_outcome_unknown`: retry with the same key."

	toolCreateBulkJob = "Send to many recipients with the pacing of the core. Send an `Idempotency-Key` " +
		"and reuse it only for a retry. Error codes and actions: " +
		"`not_found`: the instance does not exist for this API key. Do not retry. " +
		"`instance_not_connected`, `instance_not_ready`: the instance cannot send now. Tell a person. " +
		"`idempotency_key_conflict`: use a new key for a new job. " +
		"`bulk_outcome_unknown`: retry with the same key."

	toolGetBulkJob = "The state and the counts of a bulk job. Error codes and actions: " +
		"`not_found`: the job does not exist for this API key. Do not retry. " +
		"`core_unavailable`: the core is not reachable. Retry later."

	toolCancelBulkJob = "Stop a bulk job. Messages that the core already sent stay sent. A finished job is returned unchanged. " +
		"Error codes and actions: " +
		"`not_found`: the job does not exist for this API key. Do not retry. " +
		"`core_unavailable`: the core is not reachable. Retry later."
)
