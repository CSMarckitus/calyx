package tenancy

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/database/gen"
)

type NewOrganization struct {
	Name               string
	MaxInstances       int
	WhatsAppWebAllowed bool
}

type OrganizationChange struct {
	Name               *string
	MaxInstances       *int
	WhatsAppWebAllowed *bool
}

type OrganizationView struct {
	gen.Organization
	InstanceCount int
}

func viewOf(o gen.Organization, count int32) OrganizationView {
	return OrganizationView{Organization: o, InstanceCount: int(count)}
}

// CreateOrganization creates an organization and its default client in one transaction.
func (s *Service) CreateOrganization(ctx context.Context, in NewOrganization) (gen.Organization, gen.Client, error) {
	var f apierr.Fields
	name := validName(&f, in.Name)
	validMaxInstances(&f, in.MaxInstances)
	if err := f.Err(); err != nil {
		return gen.Organization{}, gen.Client{}, err
	}
	var org gen.Organization
	var client gen.Client
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := gen.New(tx)
		var err error
		org, err = q.InsertOrganization(ctx, gen.InsertOrganizationParams{
			ID: uuid.New(), Name: name, MaxInstances: int32(in.MaxInstances), WhatsappWebAllowed: in.WhatsAppWebAllowed,
		})
		if err != nil {
			return err
		}
		client, err = q.CreateClient(ctx, gen.CreateClientParams{
			ID: uuid.New(), OrganizationID: org.ID, Name: DefaultClientName, IsDefault: true,
		})
		return err
	})
	if err != nil {
		return gen.Organization{}, gen.Client{}, toAPIError(err)
	}
	return org, client, nil
}

// UpdateOrganization is for the operator. Lowering max_instances keeps the existing instances.
func (s *Service) UpdateOrganization(ctx context.Context, id uuid.UUID, ch OrganizationChange) (gen.Organization, error) {
	arg := gen.UpdateOrganizationParams{ID: id, WhatsappWebAllowed: ch.WhatsAppWebAllowed}
	var f apierr.Fields
	if ch.Name != nil {
		name := validName(&f, *ch.Name)
		arg.Name = &name
	}
	if ch.MaxInstances != nil {
		validMaxInstances(&f, *ch.MaxInstances)
		n := int32(*ch.MaxInstances)
		arg.MaxInstances = &n
	}
	if err := f.Err(); err != nil {
		return gen.Organization{}, err
	}
	q := gen.New(s.pool)
	if arg.Name == nil && arg.MaxInstances == nil && arg.WhatsappWebAllowed == nil {
		org, err := q.GetOrganization(ctx, id)
		return org, toAPIError(err)
	}
	org, err := q.UpdateOrganization(ctx, arg)
	return org, toAPIError(err)
}

// AcknowledgeWhatsAppWeb stores the first acknowledgement. It works while WhatsApp Web is blocked.
// accept must be true: the owner states that they accept the terms.
func (s *Service) AcknowledgeWhatsAppWeb(ctx context.Context, p Principal, accept bool) (gen.Organization, error) {
	if err := Allow(p, ManageOrganization); err != nil {
		return gen.Organization{}, err
	}
	if !accept {
		e := apierr.Invalid("accept must be true.")
		e.Errors = []apierr.FieldError{{Field: "accept", Message: "must be true."}}
		return gen.Organization{}, e
	}
	org, err := gen.New(s.pool).AcknowledgeWhatsAppWeb(ctx, gen.AcknowledgeWhatsAppWebParams{
		ID: p.OrganizationID, AcknowledgedAt: s.clk.Now(), UserID: p.UserID,
	})
	return org, toAPIError(err)
}

func (s *Service) GetOrganization(ctx context.Context, p Principal) (OrganizationView, error) {
	if err := Allow(p, ManageOrganization); err != nil {
		return OrganizationView{}, err
	}
	row, err := gen.New(s.pool).GetOrganizationView(ctx, p.OrganizationID)
	if err != nil {
		return OrganizationView{}, toAPIError(err)
	}
	return viewOf(row.Organization, row.InstanceCount), nil
}

func (s *Service) RenameOrganization(ctx context.Context, p Principal, name string) (OrganizationView, error) {
	if err := Allow(p, ManageOrganization); err != nil {
		return OrganizationView{}, err
	}
	var f apierr.Fields
	name = validName(&f, name)
	if err := f.Err(); err != nil {
		return OrganizationView{}, err
	}
	q := gen.New(s.pool)
	if _, err := q.UpdateOrganization(ctx, gen.UpdateOrganizationParams{ID: p.OrganizationID, Name: &name}); err != nil {
		return OrganizationView{}, toAPIError(err)
	}
	row, err := q.GetOrganizationView(ctx, p.OrganizationID)
	if err != nil {
		return OrganizationView{}, toAPIError(err)
	}
	return viewOf(row.Organization, row.InstanceCount), nil
}

// ListOrganizations is for the operator.
func (s *Service) ListOrganizations(ctx context.Context) ([]OrganizationView, error) {
	rows, err := gen.New(s.pool).ListOrganizationViews(ctx)
	if err != nil {
		return nil, toAPIError(err)
	}
	out := make([]OrganizationView, 0, len(rows))
	for _, r := range rows {
		out = append(out, viewOf(r.Organization, r.InstanceCount))
	}
	return out, nil
}
