package tenancy

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/database/gen"
)

type ClientInput struct {
	Name               string
	CanCreateInstances bool
	MaxInstances       int
}

type ClientChange struct {
	Name               *string
	CanCreateInstances *bool
	MaxInstances       *int
}

// PendingInvitation is the invitation of a client that nobody accepted yet. It can be expired.
type PendingInvitation struct {
	Email     string
	ExpiresAt time.Time
}

type ClientView struct {
	gen.Client
	InstanceCount     int
	UserEmail         string
	PendingInvitation *PendingInvitation // nil: none
}

func pendingInvitation(email *string, expiresAt *time.Time) *PendingInvitation {
	if email == nil || expiresAt == nil {
		return nil
	}
	return &PendingInvitation{Email: *email, ExpiresAt: *expiresAt}
}

func (s *Service) CreateClient(ctx context.Context, p Principal, in ClientInput) (gen.Client, error) {
	if err := Allow(p, ManageClients); err != nil {
		return gen.Client{}, err
	}
	var f apierr.Fields
	name := validName(&f, in.Name)
	validMaxInstances(&f, in.MaxInstances)
	if err := f.Err(); err != nil {
		return gen.Client{}, err
	}
	c, err := gen.New(s.pool).CreateClient(ctx, gen.CreateClientParams{
		ID: uuid.New(), OrganizationID: p.OrganizationID, Name: name,
		CanCreateInstances: in.CanCreateInstances, MaxInstances: int32(in.MaxInstances),
	})
	return c, toAPIError(err)
}

func (s *Service) UpdateClient(ctx context.Context, p Principal, id uuid.UUID, ch ClientChange) (gen.Client, error) {
	if err := Allow(p, ManageClients); err != nil {
		return gen.Client{}, err
	}
	arg := gen.UpdateClientParams{ID: id, OrganizationID: p.OrganizationID, CanCreateInstances: ch.CanCreateInstances}
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
		return gen.Client{}, err
	}
	q := gen.New(s.pool)
	if arg.Name == nil && arg.MaxInstances == nil && arg.CanCreateInstances == nil {
		v, err := q.GetClientView(ctx, gen.GetClientViewParams{ID: id, OrganizationID: p.OrganizationID})
		return v.Client, toAPIError(err)
	}
	c, err := q.UpdateClient(ctx, arg)
	return c, toAPIError(err)
}

// DeleteClient deletes a client that is not the default and has no instance row. The database
// deletes the client's user, invitations, sessions, API keys and password resets by cascade.
func (s *Service) DeleteClient(ctx context.Context, p Principal, id uuid.UUID) error {
	if err := Allow(p, ManageClients); err != nil {
		return err
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := gen.New(tx)
		c, err := q.LockClient(ctx, gen.LockClientParams{ID: id, OrganizationID: p.OrganizationID})
		if err != nil {
			return err
		}
		if c.IsDefault {
			return apierr.Conflict(apierr.CodeDefaultClient, "The default client cannot be deleted.")
		}
		n, err := q.CountInstancesOfClient(ctx, id)
		if err != nil {
			return err
		}
		if n > 0 {
			return apierr.Conflict(apierr.CodeClientHasInstances, "Remove the instances of this client first.")
		}
		return q.DeleteClient(ctx, gen.DeleteClientParams{ID: id, OrganizationID: p.OrganizationID})
	})
	return toAPIError(err)
}

func (s *Service) ListClients(ctx context.Context, p Principal) ([]ClientView, error) {
	if err := Allow(p, ManageClients); err != nil {
		return nil, err
	}
	rows, err := gen.New(s.pool).ListClientViews(ctx, p.OrganizationID)
	if err != nil {
		return nil, toAPIError(err)
	}
	out := make([]ClientView, 0, len(rows))
	for _, r := range rows {
		out = append(out, ClientView{Client: r.Client, InstanceCount: int(r.InstanceCount), UserEmail: r.UserEmail,
			PendingInvitation: pendingInvitation(r.PendingEmail, r.PendingExpiresAt)})
	}
	return out, nil
}

func (s *Service) GetClient(ctx context.Context, p Principal, id uuid.UUID) (ClientView, error) {
	if err := Allow(p, ManageClients); err != nil {
		return ClientView{}, err
	}
	r, err := gen.New(s.pool).GetClientView(ctx, gen.GetClientViewParams{ID: id, OrganizationID: p.OrganizationID})
	if err != nil {
		return ClientView{}, toAPIError(err)
	}
	return ClientView{
		Client: r.Client, InstanceCount: int(r.InstanceCount), UserEmail: r.UserEmail,
		PendingInvitation: pendingInvitation(r.PendingEmail, r.PendingExpiresAt),
	}, nil
}
