package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"calyx/backend/internal/tenancy"
)

type createClientInput struct {
	Body struct {
		Name               tenantName    `json:"name"`
		CanCreateInstances *bool         `json:"can_create_instances,omitempty" nullable:"true" doc:"The default is false."`
		MaxInstances       *maxInstances `json:"max_instances,omitempty" nullable:"true"`
	}
}

type updateClientInput struct {
	ID   uuid.UUID `path:"id" format:"uuid"`
	Body struct {
		Name               *tenantName   `json:"name,omitempty" nullable:"true"`
		CanCreateInstances *bool         `json:"can_create_instances,omitempty" nullable:"true" doc:"True lets the user of this client create instances. Absent or null keeps the stored value."`
		MaxInstances       *maxInstances `json:"max_instances,omitempty" nullable:"true"`
	}
}

type inviteClientUserInput struct {
	ID   uuid.UUID `path:"id" format:"uuid"`
	Body struct {
		Email emailAddress `json:"email"`
	}
}

type invitationJSON struct {
	Email      string    `json:"email" doc:"The email address of the invited person."`
	ExpiresAt  time.Time `json:"expires_at" doc:"When the invitation expires, RFC 3339 in UTC."`
	MailSent   bool      `json:"mail_sent" doc:"True when the invitation mail was sent. When false, send invite_link to the person."`
	InviteLink *string   `json:"invite_link" doc:"The invitation link. null when the mail was sent."`
}

type clientListOutput struct{ Body itemsJSON[clientJSON] }

type clientOutput struct{ Body clientJSON }

type invitationOutput struct{ Body invitationJSON }

func (s *server) registerClientOps(api huma.API) {
	const v1 = "/api/v1"
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "listClients", Tags: []string{"Clients"}, Method: http.MethodGet, Path: v1 + "/clients",
		Summary:     "List the clients, the default client included. Owners only.",
		Description: "Errors: 401 unauthenticated, 403 owner_only.",
	}, accessLoggedIn, tenancy.ManageClients), s.listClients)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "createClient", Tags: []string{"Clients"}, Method: http.MethodPost, Path: v1 + "/clients",
		Summary: "Create a client. Owners only.", DefaultStatus: http.StatusCreated,
		Description: "Errors: 400 invalid_request, 401 unauthenticated, 403 owner_only, 403 csrf_check_failed.",
	}, accessLoggedIn, tenancy.ManageClients), s.createClient)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "getClient", Tags: []string{"Clients"}, Method: http.MethodGet, Path: v1 + "/clients/{id}",
		Summary:     "Read a client. Owners only.",
		Description: "Errors: 401 unauthenticated, 403 owner_only, 404 not_found.",
	}, accessLoggedIn, tenancy.ManageClients), s.getClient)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "updateClient", Tags: []string{"Clients"}, Method: http.MethodPatch, Path: v1 + "/clients/{id}",
		Summary: "Change a client. Owners only.",
		Description: "Errors: 400 invalid_request, 401 unauthenticated, 403 owner_only, 403 csrf_check_failed, " +
			"404 not_found.",
	}, accessLoggedIn, tenancy.ManageClients), s.updateClient)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "deleteClient", Tags: []string{"Clients"}, Method: http.MethodDelete, Path: v1 + "/clients/{id}",
		Summary: "Delete a client, its user, its sessions, its API keys and its pending invitations. Owners only.",
		Description: "Errors: 401 unauthenticated, 403 owner_only, 403 csrf_check_failed, 404 not_found, " +
			"409 client_has_instances, 409 default_client.",
	}, accessLoggedIn, tenancy.ManageClients), s.deleteClient)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "inviteClientUser", Tags: []string{"Clients"}, Method: http.MethodPost, Path: v1 + "/clients/{id}/invitations",
		Summary: "Invite the user of a client. Owners only.", DefaultStatus: http.StatusCreated,
		Description: "An email whose access was removed from this client can return. " +
			"Errors: 400 invalid_request, 401 unauthenticated, 403 owner_only, 403 csrf_check_failed, " +
			"403 api_key_not_allowed, 404 not_found, 409 conflict (the client has a user), 409 default_client.",
	}, accessLoggedInCookie, tenancy.ManageClients), s.inviteClientUser)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "cancelInvitation", Tags: []string{"Clients"}, Method: http.MethodDelete, Path: v1 + "/clients/{id}/invitations",
		Summary: "Cancel the pending invitation of a client. Owners only.",
		Description: "It also deletes an expired invitation. Errors: 401 unauthenticated, 403 owner_only, " +
			"403 csrf_check_failed, 403 api_key_not_allowed, 404 not_found (the client has no pending invitation).",
	}, accessLoggedInCookie, tenancy.ManageClients), s.cancelInvitation)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "removeClientAccess", Tags: []string{"Clients"}, Method: http.MethodDelete, Path: v1 + "/clients/{id}/user",
		Summary: "Remove the access of the user of a client. Owners only.",
		Description: "The user is disabled: its sessions end and its API keys are revoked. The instances, the bulk " +
			"jobs and the webhooks do not change. A new invitation of the same email to this client gives the " +
			"access back. Errors: 401 unauthenticated, 403 owner_only, 403 csrf_check_failed, " +
			"403 api_key_not_allowed, 404 not_found (the client has no active user).",
	}, accessLoggedInCookie, tenancy.ManageClients), s.removeClientAccess)
}

// DELETE /clients/{id}/invitations
func (s *server) cancelInvitation(ctx context.Context, in *idInput) (*struct{}, error) {
	return nil, serviceError(ctx, s.Auth.CancelInvitation(ctx, principalOf(ctx), in.ID))
}

// DELETE /clients/{id}/user
func (s *server) removeClientAccess(ctx context.Context, in *idInput) (*struct{}, error) {
	return nil, serviceError(ctx, s.Auth.RemoveClientAccess(ctx, principalOf(ctx), in.ID))
}

// GET /clients
func (s *server) listClients(ctx context.Context, _ *struct{}) (*clientListOutput, error) {
	views, err := s.Tenancy.ListClients(ctx, principalOf(ctx))
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	items := make([]clientJSON, 0, len(views))
	for _, v := range views {
		items = append(items, clientResource(v))
	}
	return &clientListOutput{Body: itemsJSON[clientJSON]{Items: items}}, nil
}

// POST /clients. An absent field gets its zero value: no instances, no limit of its own.
func (s *server) createClient(ctx context.Context, in *createClientInput) (*clientOutput, error) {
	ci := tenancy.ClientInput{Name: string(in.Body.Name)}
	if in.Body.CanCreateInstances != nil {
		ci.CanCreateInstances = *in.Body.CanCreateInstances
	}
	if in.Body.MaxInstances != nil {
		ci.MaxInstances = int(*in.Body.MaxInstances)
	}
	c, err := s.Tenancy.CreateClient(ctx, principalOf(ctx), ci)
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	return &clientOutput{Body: clientResource(tenancy.ClientView{Client: c})}, nil
}

// GET /clients/{id}
func (s *server) getClient(ctx context.Context, in *idInput) (*clientOutput, error) {
	v, err := s.Tenancy.GetClient(ctx, principalOf(ctx), in.ID)
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	return &clientOutput{Body: clientResource(v)}, nil
}

// PATCH /clients/{id}. The answer reads the client again: it holds the user email and the
// instance count.
func (s *server) updateClient(ctx context.Context, in *updateClientInput) (*clientOutput, error) {
	p := principalOf(ctx)
	ch := tenancy.ClientChange{CanCreateInstances: in.Body.CanCreateInstances}
	if in.Body.Name != nil {
		name := string(*in.Body.Name)
		ch.Name = &name
	}
	if in.Body.MaxInstances != nil {
		n := int(*in.Body.MaxInstances)
		ch.MaxInstances = &n
	}
	if _, err := s.Tenancy.UpdateClient(ctx, p, in.ID, ch); err != nil {
		return nil, serviceError(ctx, err)
	}
	v, err := s.Tenancy.GetClient(ctx, p, in.ID)
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	return &clientOutput{Body: clientResource(v)}, nil
}

// DELETE /clients/{id}. The service also deletes the user, the sessions, the API keys and the
// pending invitations of the client.
func (s *server) deleteClient(ctx context.Context, in *idInput) (*struct{}, error) {
	return nil, serviceError(ctx, s.Tenancy.DeleteClient(ctx, principalOf(ctx), in.ID))
}

// POST /clients/{id}/invitations. The link is in the answer only when no mail went out.
func (s *server) inviteClientUser(ctx context.Context, in *inviteClientUserInput) (*invitationOutput, error) {
	res, err := s.Auth.InviteClientUser(ctx, principalOf(ctx), in.ID, string(in.Body.Email))
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	out := invitationJSON{Email: res.Email, ExpiresAt: utc(res.ExpiresAt), MailSent: res.MailSent}
	if !res.MailSent {
		link := res.Link
		out.InviteLink = &link
	}
	return &invitationOutput{Body: out}, nil
}
