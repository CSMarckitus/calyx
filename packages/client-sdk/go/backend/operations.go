package backend

import (
	"context"
	"net/http"
	"time"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
)

const (
	// The backend waits up to 5 minutes for the core to send a message (sendDeadline). The
	// limit is longer, so the SDK does not stop a call that the backend still serves.
	sendMessageLimit = 6 * time.Minute
	defaultLimit     = 2 * time.Minute
	// The backend waits up to 60 s to read the body and 60 s for the core in a bulk create.
	createBulkJobLimit = 150 * time.Second
)

// operation describes one HTTP operation of the backend.
type operation struct {
	method string
	path   string
	// limit is the default time limit of one call. Zero means no default limit.
	limit time.Duration
}

// operations maps each operationId to its route. The browser, account and cookie-only operations
// are not in the table.
var operations = map[string]operation{
	"listAPIKeys":        {http.MethodGet, "/api/v1/api-keys", defaultLimit},
	"revokeAPIKey":       {http.MethodDelete, "/api/v1/api-keys/{id}", defaultLimit},
	"listClients":        {http.MethodGet, "/api/v1/clients", defaultLimit},
	"createClient":       {http.MethodPost, "/api/v1/clients", defaultLimit},
	"getClient":          {http.MethodGet, "/api/v1/clients/{id}", defaultLimit},
	"updateClient":       {http.MethodPatch, "/api/v1/clients/{id}", defaultLimit},
	"deleteClient":       {http.MethodDelete, "/api/v1/clients/{id}", defaultLimit},
	"listInstances":      {http.MethodGet, "/api/v1/instances", defaultLimit},
	"createInstance":     {http.MethodPost, "/api/v1/instances", defaultLimit},
	"getInstance":        {http.MethodGet, "/api/v1/instances/{id}", defaultLimit},
	"updateInstance":     {http.MethodPatch, "/api/v1/instances/{id}", defaultLimit},
	"removeInstance":     {http.MethodDelete, "/api/v1/instances/{id}", defaultLimit},
	"pairInstance":       {http.MethodPost, "/api/v1/instances/{id}/pair", defaultLimit},
	"cancelPairing":      {http.MethodDelete, "/api/v1/instances/{id}/pairing", defaultLimit},
	"followPairing":      {http.MethodGet, "/api/v1/instances/{id}/pairing", 0},
	"sendMessage":        {http.MethodPost, "/api/v1/instances/{id}/messages", sendMessageLimit},
	"createBulkJob":      {http.MethodPost, "/api/v1/instances/{id}/bulk-jobs", createBulkJobLimit},
	"listBulkJobs":       {http.MethodGet, "/api/v1/instances/{id}/bulk-jobs", defaultLimit},
	"getBulkJob":         {http.MethodGet, "/api/v1/bulk-jobs/{id}", defaultLimit},
	"cancelBulkJob":      {http.MethodPost, "/api/v1/bulk-jobs/{id}/cancel", defaultLimit},
	"getMe":              {http.MethodGet, "/api/v1/me", defaultLimit},
	"getOrganization":    {http.MethodGet, "/api/v1/organization", defaultLimit},
	"updateOrganization": {http.MethodPatch, "/api/v1/organization", defaultLimit},
}

// call runs one operation and returns its JSON result as T.
func call[T any](ctx context.Context, c *Client, r request) (T, error) {
	var out T
	r.result = &out
	_, err := c.do(ctx, r)
	return out, err
}

// send runs a send operation. It makes the idempotency key and returns it with the result.
func send[T any](ctx context.Context, c *Client, r request, opts SendOptions) (calyx.Sent[T], error) {
	r.key = sendKey(opts)
	out, err := call[T](ctx, c, r)
	return calyx.Sent[T]{Data: out, IdempotencyKey: r.key}, err
}

// ListAPIKeys lists the API keys of the organization.
func (c *Client) ListAPIKeys(ctx context.Context) (ApiKeyList, error) {
	return call[ApiKeyList](ctx, c, request{op: "listAPIKeys"})
}

// RevokeAPIKey revokes an API key.
func (c *Client) RevokeAPIKey(ctx context.Context, id string) error {
	_, err := c.do(ctx, request{op: "revokeAPIKey", id: id})
	return err
}

// ListClients lists the clients of the organization.
func (c *Client) ListClients(ctx context.Context) (ClientList, error) {
	return call[ClientList](ctx, c, request{op: "listClients"})
}

// CreateClient creates a client.
//
// The call has no idempotency key. After a client error (timeout, network_error,
// invalid_response or aborted), the advice is unknown: the backend can have made the client.
// List the clients before you call again, because a second call can make a duplicate.
func (c *Client) CreateClient(ctx context.Context, req CreateClientRequest) (OrgClient, error) {
	return call[OrgClient](ctx, c, request{op: "createClient", body: req})
}

// GetClient gets one client.
func (c *Client) GetClient(ctx context.Context, id string) (OrgClient, error) {
	return call[OrgClient](ctx, c, request{op: "getClient", id: id})
}

// UpdateClient changes a client. A nil field stays as it is.
func (c *Client) UpdateClient(ctx context.Context, id string, req UpdateClientRequest) (OrgClient, error) {
	return call[OrgClient](ctx, c, request{op: "updateClient", id: id, body: req})
}

// DeleteClient deletes a client.
func (c *Client) DeleteClient(ctx context.Context, id string) error {
	_, err := c.do(ctx, request{op: "deleteClient", id: id})
	return err
}

// ListInstances lists the instances that the caller can see.
func (c *Client) ListInstances(ctx context.Context) (InstanceList, error) {
	return call[InstanceList](ctx, c, request{op: "listInstances"})
}

// CreateInstance creates an instance and starts its pairing.
//
// The call has no idempotency key. After a client error (timeout, network_error,
// invalid_response or aborted), the advice is unknown: the backend can have made the instance.
// List the instances before you call again, because a second call can make a duplicate.
func (c *Client) CreateInstance(ctx context.Context, req CreateInstanceRequest) (CreateInstanceResult, error) {
	return call[CreateInstanceResult](ctx, c, request{op: "createInstance", body: req})
}

// GetInstance gets one instance.
func (c *Client) GetInstance(ctx context.Context, id string) (Instance, error) {
	return call[Instance](ctx, c, request{op: "getInstance", id: id})
}

// UpdateInstance changes an instance. A nil field stays as it is.
func (c *Client) UpdateInstance(ctx context.Context, id string, req UpdateInstanceRequest) (Instance, error) {
	return call[Instance](ctx, c, request{op: "updateInstance", id: id, body: req})
}

// RemoveInstance removes an instance. It returns the HTTP status. 202 means that the core did
// not confirm the removal and the instance stays in the state "removing". 204 means that the
// instance is removed.
func (c *Client) RemoveInstance(ctx context.Context, id string) (int, error) {
	return c.do(ctx, request{op: "removeInstance", id: id})
}

// PairInstance starts the pairing of an instance.
func (c *Client) PairInstance(ctx context.Context, id string, req PairInstanceRequest) (PairResult, error) {
	return call[PairResult](ctx, c, request{op: "pairInstance", id: id, body: req})
}

// CancelPairing cancels the pairing of an instance.
func (c *Client) CancelPairing(ctx context.Context, id string) error {
	_, err := c.do(ctx, request{op: "cancelPairing", id: id})
	return err
}

// SendMessage sends one message. The result and every error carry the idempotency key.
func (c *Client) SendMessage(ctx context.Context, id string, req SendMessageRequest, opts SendOptions) (calyx.Sent[SendMessageResult], error) {
	return send[SendMessageResult](ctx, c, request{op: "sendMessage", id: id, body: req}, opts)
}

// CreateBulkJob creates a bulk job. The result and every error carry the idempotency key.
func (c *Client) CreateBulkJob(ctx context.Context, id string, req CreateBulkJobRequest, opts SendOptions) (calyx.Sent[BulkJob], error) {
	return send[BulkJob](ctx, c, request{op: "createBulkJob", id: id, body: req}, opts)
}

// ListBulkJobs lists the bulk jobs of an instance.
func (c *Client) ListBulkJobs(ctx context.Context, id string) (BulkJobList, error) {
	return call[BulkJobList](ctx, c, request{op: "listBulkJobs", id: id})
}

// GetBulkJob gets one bulk job.
func (c *Client) GetBulkJob(ctx context.Context, id string) (BulkJob, error) {
	return call[BulkJob](ctx, c, request{op: "getBulkJob", id: id})
}

// CancelBulkJob cancels a bulk job.
func (c *Client) CancelBulkJob(ctx context.Context, id string) (BulkJob, error) {
	return call[BulkJob](ctx, c, request{op: "cancelBulkJob", id: id})
}

// GetMe gets the caller: the user and the organization.
func (c *Client) GetMe(ctx context.Context) (Me, error) {
	return call[Me](ctx, c, request{op: "getMe"})
}

// GetOrganization gets the organization of the caller.
func (c *Client) GetOrganization(ctx context.Context) (Organization, error) {
	return call[Organization](ctx, c, request{op: "getOrganization"})
}

// UpdateOrganization changes the organization. A nil field stays as it is.
func (c *Client) UpdateOrganization(ctx context.Context, req UpdateOrganizationRequest) (Organization, error) {
	return call[Organization](ctx, c, request{op: "updateOrganization", body: req})
}
