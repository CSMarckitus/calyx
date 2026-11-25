package tenancy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/database/gen"
)

// phoneNumberIDKey is the unique constraint of instances.phone_number_id (migration 0001).
const phoneNumberIDKey = "instances_phone_number_id_key"

type NewInstance struct {
	ClientID    uuid.UUID
	CoreID      string
	DisplayName string
	Provider    string // "WHATSAPP_WEB" or "META_CLOUD"
	SendOnly    bool
	// WhatsApp Web only: a name of core.DeviceIdentities() and a locale ("es-ES"). nil: none yet.
	DeviceIdentity *string
	Locale         *string
	PhoneNumberID  *string
}

// CoreName returns the name of the instance in the core: "i_" and the id without dashes.
func CoreName(id uuid.UUID) string {
	return "i_" + strings.ReplaceAll(id.String(), "-", "")
}

// ReserveInstance checks the rules of instance creation in a fixed order and inserts the
// row in state "creating" inside tx. It locks the organization, then the client, so reservations
// of one organization run one after the other and the counts are exact. It never commits or rolls
// back: the caller owns tx and returns the error to pgx.BeginFunc.
func ReserveInstance(ctx context.Context, tx pgx.Tx, p Principal, in NewInstance) (gen.Instance, error) {
	if err := Allow(p, CreateInstance); err != nil {
		return gen.Instance{}, err
	}
	// The instances service checks these fields first; this check keeps the row valid.
	var f apierr.Fields
	switch {
	case in.Provider != "WHATSAPP_WEB" && in.Provider != "META_CLOUD":
		f.Add("provider", `must be "WHATSAPP_WEB" or "META_CLOUD".`)
	case in.Provider == "META_CLOUD" && (in.PhoneNumberID == nil || *in.PhoneNumberID == ""):
		f.Add("meta_credentials.phone_number_id", "is required.")
	case in.Provider == "WHATSAPP_WEB" && in.PhoneNumberID != nil:
		f.Add("meta_credentials", "is only for META_CLOUD.")
	}
	if err := f.Err(); err != nil {
		return gen.Instance{}, err
	}
	if p.Role == RoleClient && (p.ClientID == nil || in.ClientID != *p.ClientID) {
		return gen.Instance{}, apierr.NotFound()
	}
	q := gen.New(tx)
	org, err := q.LockOrganization(ctx, p.OrganizationID)
	if err != nil {
		return gen.Instance{}, toAPIError(err)
	}
	client, err := q.LockClient(ctx, gen.LockClientParams{ID: in.ClientID, OrganizationID: p.OrganizationID})
	if err != nil {
		return gen.Instance{}, toAPIError(err)
	}
	orgCount, err := q.CountInstancesOfOrganization(ctx, org.ID)
	if err != nil {
		return gen.Instance{}, toAPIError(err)
	}
	clientCount, err := q.CountInstancesOfClient(ctx, client.ID)
	if err != nil {
		return gen.Instance{}, toAPIError(err)
	}
	if problem := creationProblem(p, in.Provider == "WHATSAPP_WEB", org, client, orgCount, clientCount); problem != nil {
		return gen.Instance{}, problem
	}
	if in.PhoneNumberID != nil {
		inUse, err := q.PhoneNumberIDInUse(ctx, *in.PhoneNumberID)
		if err != nil {
			return gen.Instance{}, toAPIError(err)
		}
		if inUse {
			return gen.Instance{}, errPhoneNumberInUse()
		}
	}
	id := uuid.New()
	row, err := q.InsertInstance(ctx, gen.InsertInstanceParams{
		ID: id, OrganizationID: p.OrganizationID, ClientID: client.ID, CoreID: in.CoreID, CoreName: CoreName(id),
		DisplayName: in.DisplayName, Provider: in.Provider, SendOnly: in.SendOnly,
		PhoneNumberID: in.PhoneNumberID, DeviceIdentity: in.DeviceIdentity, Locale: in.Locale,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == phoneNumberIDKey {
			return gen.Instance{}, errPhoneNumberInUse()
		}
		return gen.Instance{}, apierr.Internal(err)
	}
	return row, nil
}

// creationProblem holds the rules that refuse a new instance, in a fixed order. It returns
// nil when none applies. ReserveInstance calls it with locked rows, CreateInstanceProblem with
// plain reads. Only a client user can have can_create_instances refuse it.
func creationProblem(p Principal, whatsAppWeb bool, org gen.Organization, client gen.Client, orgCount, clientCount int32) *apierr.Error {
	if whatsAppWeb {
		if !org.WhatsappWebAllowed {
			return apierr.Forbidden(apierr.CodeWhatsAppWebNotAllowed, "WhatsApp Web is not allowed for this organization.")
		}
		if org.WhatsappWebAcknowledgedAt == nil {
			return apierr.Forbidden(apierr.CodeWhatsAppWebNotAcknowledged, "The owner must accept the WhatsApp Web terms first.")
		}
	}
	if p.Role == RoleClient && !client.CanCreateInstances {
		return apierr.Forbidden(apierr.CodeInstanceCreationNotAllowed, "This client may not create instances.")
	}
	if orgCount >= org.MaxInstances {
		return apierr.Conflict(apierr.CodeQuotaExceeded,
			fmt.Sprintf("The organization has reached its limit of %d instances.", org.MaxInstances))
	}
	if client.MaxInstances != 0 && clientCount >= client.MaxInstances {
		return apierr.Conflict(apierr.CodeQuotaExceeded,
			fmt.Sprintf("This client has reached its limit of %d instances.", client.MaxInstances))
	}
	return nil
}

// CreateInstanceProblem says why a client user cannot create a WhatsApp Web instance in its own
// client now: the code of the first rule that refuses it, or nil. An owner always gets nil. The
// answer is a plain read, so only a create call decides.
func (s *Service) CreateInstanceProblem(ctx context.Context, p Principal) (*string, error) {
	if err := Allow(p, CreateInstance); err != nil {
		return nil, err
	}
	if p.Role == RoleOwner {
		return nil, nil
	}
	q := gen.New(s.pool)
	org, err := q.GetOrganization(ctx, p.OrganizationID)
	if err != nil {
		return nil, toAPIError(err)
	}
	cv, err := q.GetClientView(ctx, gen.GetClientViewParams{ID: *p.ClientID, OrganizationID: p.OrganizationID})
	if err != nil {
		return nil, toAPIError(err)
	}
	orgCount, err := q.CountInstancesOfOrganization(ctx, org.ID)
	if err != nil {
		return nil, toAPIError(err)
	}
	if problem := creationProblem(p, true, org, cv.Client, orgCount, cv.InstanceCount); problem != nil {
		return &problem.Code, nil
	}
	return nil, nil
}

func errPhoneNumberInUse() error {
	return apierr.Conflict(apierr.CodePhoneNumberIDInUse, "This phone number id is already used by another instance.")
}
