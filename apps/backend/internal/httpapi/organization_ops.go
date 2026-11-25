package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"calyx/backend/internal/tenancy"
)

// updateOrganizationInput has only the name: the limits and the WhatsApp Web permission are
// operator settings, so they are unknown fields here.
type updateOrganizationInput struct {
	Body struct {
		Name *tenantName `json:"name,omitempty" nullable:"true"`
	}
}

type acknowledgementInput struct {
	Body struct {
		Accept bool `json:"accept" doc:"Must be true."`
	}
}

type organizationOutput struct{ Body organizationJSON }

func (s *server) registerOrganizationOps(api huma.API) {
	const v1 = "/api/v1"
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "getOrganization", Tags: []string{"Organization"}, Method: http.MethodGet, Path: v1 + "/organization",
		Summary:     "Read the organization. Owners only.",
		Description: "Errors: 401 unauthenticated, 403 owner_only.",
	}, accessLoggedIn, tenancy.ManageOrganization), s.getOrganization)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "updateOrganization", Tags: []string{"Organization"}, Method: http.MethodPatch, Path: v1 + "/organization",
		Summary:     "Rename the organization. Owners only.",
		Description: "Errors: 400 invalid_request, 401 unauthenticated, 403 owner_only, 403 csrf_check_failed.",
	}, accessLoggedIn, tenancy.ManageOrganization), s.updateOrganization)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "acknowledgeWhatsAppWeb", Tags: []string{"Organization"}, Method: http.MethodPost,
		Path:    v1 + "/organization/whatsapp-web-acknowledgement",
		Summary: "Accept the WhatsApp Web terms. Owners only.",
		Description: "Errors: 400 invalid_request (accept must be true), 401 unauthenticated, 403 owner_only, " +
			"403 csrf_check_failed, 403 api_key_not_allowed.",
	}, accessLoggedInCookie, tenancy.ManageOrganization), s.acknowledgeWhatsAppWeb)
}

// GET /organization
func (s *server) getOrganization(ctx context.Context, _ *struct{}) (*organizationOutput, error) {
	org, err := s.Tenancy.GetOrganization(ctx, principalOf(ctx))
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	return &organizationOutput{Body: organizationResource(org, s.Config)}, nil
}

// PATCH /organization. A body without a name changes nothing and returns the organization.
func (s *server) updateOrganization(ctx context.Context, in *updateOrganizationInput) (*organizationOutput, error) {
	p := principalOf(ctx)
	var (
		org tenancy.OrganizationView
		err error
	)
	if in.Body.Name == nil {
		org, err = s.Tenancy.GetOrganization(ctx, p)
	} else {
		org, err = s.Tenancy.RenameOrganization(ctx, p, string(*in.Body.Name))
	}
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	return &organizationOutput{Body: organizationResource(org, s.Config)}, nil
}

// POST /organization/whatsapp-web-acknowledgement. The schema has no enum for accept: Huma
// would refuse false with its own detail; the service refuses it with the detail of the old route.
func (s *server) acknowledgeWhatsAppWeb(ctx context.Context, in *acknowledgementInput) (*organizationOutput, error) {
	p := principalOf(ctx)
	if _, err := s.Tenancy.AcknowledgeWhatsAppWeb(ctx, p, in.Body.Accept); err != nil {
		return nil, serviceError(ctx, err)
	}
	org, err := s.Tenancy.GetOrganization(ctx, p)
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	return &organizationOutput{Body: organizationResource(org, s.Config)}, nil
}
