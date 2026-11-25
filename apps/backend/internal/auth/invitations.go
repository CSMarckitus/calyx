package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/database"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/mail"
	"calyx/backend/internal/tenancy"
)

const InvitationTTL = 7 * 24 * time.Hour

// Only the holder of the link sees this at acceptance, so an invitation never reveals an account.
const detailEmailInUse = "This email already has an account. Log in, or ask for an invitation to another email."

const detailInvitationInvalid = "The invitation link is invalid or has expired."

// InvitationResult tells the caller what happened to the mail. Link is set when MailSent is false.
type InvitationResult struct {
	Email     string
	ExpiresAt time.Time
	MailSent  bool
	Link      string
}

// sendMail renders a template and sends it with a time limit.
func (s *Service) sendMail(ctx context.Context, template string, data mail.TemplateData) error {
	m, err := mail.Render(template, data)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, MailTimeout)
	defer cancel()
	return s.mailer.Send(ctx, m)
}

// logMailFailure writes one WARN line for a failed mail. It holds the kind, one id and the class
// of the error: never the address, the link or the text of the error, which can hold them.
// No line when mail is off, because that is a choice of the operator.
func (s *Service) logMailFailure(kind string, err error, idKey string, id uuid.UUID) {
	if errors.Is(err, mail.ErrNotConfigured) {
		return
	}
	s.log.Warn("A mail failed.", "kind", kind, idKey, id, "error_class", mail.FailureClass(err))
}

// InviteOwner invites the owner of an organization (operator commands). With replacesOwner set,
// the old owner stays active until the invitation is accepted.
func (s *Service) InviteOwner(ctx context.Context, orgID uuid.UUID, email string, replacesOwner *uuid.UUID) (InvitationResult, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return InvitationResult{}, err
	}
	q := gen.New(s.pool)
	org, err := q.GetOrganizationForInvitation(ctx, orgID)
	if err != nil {
		return InvitationResult{}, toAPIError(err)
	}
	owner, err := q.GetActiveOwner(ctx, orgID)
	switch {
	case err == nil && replacesOwner == nil:
		return InvitationResult{}, apierr.Conflict(apierr.CodeConflict, "The organization already has an owner. Transfer the ownership instead.")
	case err == nil && owner.ID != *replacesOwner:
		return InvitationResult{}, apierr.Conflict(apierr.CodeConflict, "That user is not the active owner of the organization.")
	case err != nil && !isNoRows(err):
		return InvitationResult{}, apierr.Internal(err)
	case err != nil && replacesOwner != nil:
		return InvitationResult{}, apierr.Conflict(apierr.CodeConflict, "That user is not the active owner of the organization.")
	}
	plain, hash := NewToken("")
	invID := uuid.New()
	expires := s.clk.Now().Add(InvitationTTL)
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		qt := gen.New(tx)
		if err := qt.DeletePendingOwnerInvitations(ctx, orgID); err != nil {
			return err
		}
		_, err := qt.CreateInvitation(ctx, gen.CreateInvitationParams{
			ID: invID, OrganizationID: orgID, Role: "owner", Email: email, TokenHash: hash,
			ExpiresAt: expires, ReplacesOwnerID: replacesOwner,
		})
		return err
	})
	if err != nil {
		return InvitationResult{}, toAPIError(mapInviteError(err))
	}
	link := s.link("/invite", plain)
	return s.invitationResult(ctx, "invitation_owner", mail.TemplateData{
		To: email, OrganizationName: org.Name, Link: link, PublicURL: s.publicURL.String(),
	}, invID, email, expires, link), nil
}

// InviteClientUser invites the user of a client (owner only).
func (s *Service) InviteClientUser(ctx context.Context, p tenancy.Principal, clientID uuid.UUID, email string) (InvitationResult, error) {
	if err := tenancy.Allow(p, tenancy.ManageClients); err != nil {
		return InvitationResult{}, err
	}
	email, err := NormalizeEmail(email)
	if err != nil {
		return InvitationResult{}, err
	}
	q := gen.New(s.pool)
	client, err := q.GetClientForInvitation(ctx, gen.GetClientForInvitationParams{ClientID: clientID, OrganizationID: p.OrganizationID})
	if err != nil {
		return InvitationResult{}, toAPIError(err)
	}
	if client.IsDefault {
		return InvitationResult{}, apierr.Conflict(apierr.CodeDefaultClient, "The default client has no user.")
	}
	has, err := q.ClientHasActiveUser(ctx, clientID)
	if err != nil {
		return InvitationResult{}, apierr.Internal(err)
	}
	if has {
		return InvitationResult{}, apierr.Conflict(apierr.CodeConflict, "This client already has a user.")
	}
	plain, hash := NewToken("")
	invID := uuid.New()
	expires := s.clk.Now().Add(InvitationTTL)
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		qt := gen.New(tx)
		if _, err := qt.DeletePendingClientInvitations(ctx, clientID); err != nil {
			return err
		}
		_, err := qt.CreateInvitation(ctx, gen.CreateInvitationParams{
			ID: invID, OrganizationID: p.OrganizationID, ClientID: &clientID, Role: "client", Email: email,
			TokenHash: hash, ExpiresAt: expires,
		})
		return err
	})
	if err != nil {
		return InvitationResult{}, toAPIError(mapInviteError(err))
	}
	link := s.link("/invite", plain)
	return s.invitationResult(ctx, "invitation_client", mail.TemplateData{
		To: email, OrganizationName: client.OrganizationName, ClientName: client.Name, Link: link, PublicURL: s.publicURL.String(),
	}, invID, email, expires, link), nil
}

// invitationResult sends the mail after the commit. A send error is not an error of the call:
// the invitation stays valid and the caller gets the link.
func (s *Service) invitationResult(ctx context.Context, template string, data mail.TemplateData, invID uuid.UUID, email string, expires time.Time, link string) InvitationResult {
	if err := s.sendMail(ctx, template, data); err != nil {
		s.logMailFailure(template, err, "invitation_id", invID)
		return InvitationResult{Email: email, ExpiresAt: expires, MailSent: false, Link: link}
	}
	return InvitationResult{Email: email, ExpiresAt: expires, MailSent: true}
}

func invitationGone() error {
	return apierr.New(410, apierr.CodeTokenInvalid, detailInvitationInvalid)
}

// AcceptInvitation creates the user, closes the invitation and logs the user in. For an owner
// transfer it also disables the old owner, in the same transaction. A client user whose access
// was removed returns when the invitation is for the same client: see canReturn.
func (s *Service) AcceptInvitation(ctx context.Context, token, password, ip string) (Session, gen.User, error) {
	if err := requireAll("token", token, "password", password); err != nil {
		return Session{}, gen.User{}, err
	}
	if !s.limiter.Allow("accept", ip, "") {
		return Session{}, gen.User{}, rateLimited()
	}
	now := s.clk.Now()
	inv, err := gen.New(s.pool).GetInvitationByTokenHash(ctx, HashToken(token))
	if err != nil {
		if isNoRows(err) {
			return Session{}, gen.User{}, invitationGone()
		}
		return Session{}, gen.User{}, apierr.Internal(err)
	}
	if inv.AcceptedAt != nil || !now.Before(inv.ExpiresAt) {
		return Session{}, gen.User{}, invitationGone()
	}
	if !s.limiter.Allow("accept", "", inv.Email) {
		return Session{}, gen.User{}, rateLimited()
	}
	if err := ValidatePassword("password", password); err != nil {
		return Session{}, gen.User{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return Session{}, gen.User{}, apierr.Internal(err)
	}
	var sess Session
	var user gen.User
	returned := false
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := gen.New(tx)
		locked, err := q.LockInvitation(ctx, inv.ID)
		if err != nil {
			if isNoRows(err) {
				return invitationGone() // replaced or deleted since the lookup
			}
			return err
		}
		if locked.AcceptedAt != nil || !now.Before(locked.ExpiresAt) {
			return invitationGone()
		}
		// The lock of the user makes two returns, or a return and a reset, run one after the other.
		existing, err := q.LockUserByEmail(ctx, locked.Email)
		switch {
		case err == nil && !canReturn(existing, locked):
			return apierr.Conflict(apierr.CodeEmailInUse, detailEmailInUse)
		case err == nil:
			user, err = returnUser(ctx, q, existing.ID, hash, now)
			if err != nil {
				return mapCreateUserError(err)
			}
			returned = true
		case !isNoRows(err):
			return err
		default:
			if locked.ReplacesOwnerID != nil {
				if err := disableUser(ctx, q, *locked.ReplacesOwnerID, now); err != nil {
					return err
				}
			}
			user, err = q.CreateUser(ctx, gen.CreateUserParams{
				ID: uuid.New(), Email: locked.Email, PasswordHash: hash, OrganizationID: locked.OrganizationID,
				Role: locked.Role, ClientID: locked.ClientID,
			})
			if err != nil {
				return mapCreateUserError(err)
			}
		}
		if err := q.MarkInvitationAccepted(ctx, gen.MarkInvitationAcceptedParams{AcceptedAt: now, ID: locked.ID}); err != nil {
			return err
		}
		sess, err = createSession(ctx, q, user.ID, now)
		return err
	})
	if err != nil {
		return Session{}, gen.User{}, toAPIError(err)
	}
	if returned {
		s.log.Info("A client user returned.", "actor_id", user.ID, "client_id", *user.ClientID)
	}
	return sess, user, nil
}

// canReturn tells if a user that exists can accept the invitation. Only a disabled client user of
// the same client can: every other user keeps 409 email_in_use.
func canReturn(u gen.User, inv gen.Invitation) bool {
	return u.DisabledAt != nil && u.Role == "client" &&
		u.ClientID != nil && inv.ClientID != nil && *u.ClientID == *inv.ClientID
}

// returnUser enables a removed client user with a new password. A login or a key creation that
// ran beside the removal can leave a row, so it deletes every session and pending reset and
// revokes every key first.
func returnUser(ctx context.Context, q *gen.Queries, userID uuid.UUID, hash string, now time.Time) (gen.User, error) {
	if err := q.DeleteSessionsOfUser(ctx, userID); err != nil {
		return gen.User{}, err
	}
	if err := q.DeletePendingPasswordResets(ctx, userID); err != nil {
		return gen.User{}, err
	}
	if err := q.RevokeAPIKeysOfUser(ctx, gen.RevokeAPIKeysOfUserParams{RevokedAt: now, UserID: userID}); err != nil {
		return gen.User{}, err
	}
	return q.ReactivateClientUser(ctx, gen.ReactivateClientUserParams{PasswordHash: hash, ID: userID})
}

// mapInviteError maps the violation of a pending-invitation index: a concurrent invitation won.
func mapInviteError(err error) error {
	if c, ok := database.UniqueViolation(err); ok &&
		(c == "invitations_one_pending_owner_per_org" || c == "invitations_one_pending_per_client") {
		return apierr.Conflict(apierr.CodeConflict, "Another invitation is being created. Try again.")
	}
	return err
}

func mapCreateUserError(err error) error {
	if c, ok := database.UniqueViolation(err); ok {
		switch c {
		case "users_email_key":
			return apierr.Conflict(apierr.CodeEmailInUse, detailEmailInUse)
		case "users_one_active_owner_per_org", "users_one_active_user_per_client":
			return apierr.Conflict(apierr.CodeConflict, "The organization or the client already has an active user.")
		}
	}
	return err
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// CancelInvitation deletes the pending invitation of a client (owner only), also an expired one.
// 404 when the client has none.
func (s *Service) CancelInvitation(ctx context.Context, p tenancy.Principal, clientID uuid.UUID) error {
	if err := tenancy.Allow(p, tenancy.ManageClients); err != nil {
		return err
	}
	q := gen.New(s.pool)
	// The delete filters by client only, so the organization check must come first.
	if _, err := q.GetClientForInvitation(ctx, gen.GetClientForInvitationParams{ClientID: clientID, OrganizationID: p.OrganizationID}); err != nil {
		return toAPIError(err)
	}
	n, err := q.DeletePendingClientInvitations(ctx, clientID)
	if err != nil {
		return apierr.Internal(err)
	}
	if n == 0 {
		return apierr.NotFound()
	}
	s.log.Info("An invitation was cancelled.", "actor_id", p.UserID, "client_id", clientID)
	return nil
}

// InvitationInfo is what the holder of an invitation link sees before the acceptance.
type InvitationInfo struct {
	Email            string
	OrganizationName string
	ExpiresAt        time.Time
}

// LookupInvitation returns the invitation of a token and does not use it. Unknown, accepted and
// expired tokens give the same 410.
func (s *Service) LookupInvitation(ctx context.Context, token, ip string) (InvitationInfo, error) {
	if err := requireAll("token", token); err != nil {
		return InvitationInfo{}, err
	}
	if !s.limiter.Allow("lookup", ip, "") {
		return InvitationInfo{}, rateLimited()
	}
	inv, err := gen.New(s.pool).GetInvitationForLookup(ctx, HashToken(token))
	if err != nil {
		if isNoRows(err) {
			return InvitationInfo{}, invitationGone()
		}
		return InvitationInfo{}, apierr.Internal(err)
	}
	if inv.AcceptedAt != nil || !s.clk.Now().Before(inv.ExpiresAt) {
		return InvitationInfo{}, invitationGone()
	}
	return InvitationInfo{Email: inv.Email, OrganizationName: inv.OrganizationName, ExpiresAt: inv.ExpiresAt}, nil
}
