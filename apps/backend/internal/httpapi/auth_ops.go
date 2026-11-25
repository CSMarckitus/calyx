package httpapi

import (
	"context"
	"errors"
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/auth"
	"calyx/backend/internal/database/gen"
	"calyx/backend/internal/tenancy"
)

// cookieMaxAge is the absolute limit of a session. The server enforces the 30-day sliding expiry.
const cookieMaxAge = int(auth.SessionAbsoluteTTL / 1e9)

// sessionCookieFor returns the session cookie with token. An empty token clears the cookie.
func (s *server) sessionCookieFor(token string) http.Cookie {
	c := http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   cookieMaxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.Config.PublicURL != nil && s.Config.PublicURL.Scheme == "https",
	}
	if token == "" {
		c.MaxAge = -1
	}
	return c
}

// idInput is the input of an operation that has only a path id. A malformed id is 404.
type idInput struct {
	ID uuid.UUID `path:"id" format:"uuid"`
}

type loginInput struct {
	Body struct {
		Email    string `json:"email" doc:"The email address of the user."`
		Password string `json:"password" doc:"The password of the user."`
	}
}

type emailInput struct {
	Body struct {
		Email emailAddress `json:"email"`
	}
}

type tokenPasswordInput struct {
	Body struct {
		Token    string      `json:"token" doc:"The token of the invitation link or of the reset link."`
		Password newPassword `json:"password"`
	}
}

// The lookups take the token in the body, so no log line holds it.
type lookupInvitationInput struct {
	Body struct {
		Token string `json:"token" doc:"The token of the invitation link."`
	}
}

type lookupPasswordResetInput struct {
	Body struct {
		Token string `json:"token" doc:"The token of the reset link."`
	}
}

type changePasswordInput struct {
	Body struct {
		CurrentPassword string      `json:"current_password" doc:"The password that the user has now."`
		NewPassword     newPassword `json:"new_password"`
	}
}

type createAPIKeyInput struct {
	Body struct {
		Name apiKeyName `json:"name"`
	}
}

// sessionOutput is the user of a new session, with its cookie.
type sessionOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      userJSON
}

type logoutOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

type meJSON struct {
	CreateInstanceProblem *string  `json:"create_instance_problem" doc:"Why the user cannot create a WHATSAPP_WEB instance in its own client now: whatsapp_web_not_allowed, whatsapp_web_not_acknowledged, instance_creation_not_allowed or quota_exceeded. A later version can add values. null when no rule stops it, and always null for an owner. The answer of createInstance stays the authority."`
	User                  userJSON `json:"user" doc:"The user of the API key or of the login."`
	Organization          any      `json:"organization" doc:"The organization of the user. A client user gets only id and name."`
}

// TransformSchema documents the organization, because "any" has no schema.
func (meJSON) TransformSchema(r huma.Registry, s *huma.Schema) *huma.Schema {
	org := r.Schema(reflect.TypeFor[meOrganizationJSON](), true, "")
	org.Description = s.Properties["organization"].Description
	s.Properties["organization"] = org
	return s
}

type meOutput struct{ Body meJSON }

type invitationLookupOutput struct{ Body invitationLookupJSON }

type passwordResetLookupOutput struct{ Body passwordResetLookupJSON }

type apiKeyListOutput struct{ Body itemsJSON[apiKeyJSON] }

type apiKeyOutput struct{ Body apiKeyJSON }

func (s *server) registerAuthOps(api huma.API) {
	const v1 = "/api/v1"
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "login", Tags: []string{"Account"}, Method: http.MethodPost, Path: v1 + "/auth/login",
		Summary: "Log in with email and password.",
		Description: "The answer sets the session cookie calyx_session (HttpOnly, SameSite=Lax, 90 days). " +
			"Errors: 400 invalid_request, 401 unauthenticated, 403 csrf_check_failed, 429 rate_limited.",
	}, accessPublic, ""), s.login)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "logout", Tags: []string{"Account"}, Method: http.MethodPost, Path: v1 + "/auth/logout",
		Summary: "End the current session. Applies to sessions only.",
		Description: "The answer clears the session cookie. " +
			"Errors: 401 unauthenticated, 403 csrf_check_failed or api_key_not_allowed.",
	}, accessLoggedInCookie, tenancy.ManageOwnAccount), s.logout)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "requestPasswordReset", Tags: []string{"Account"}, Method: http.MethodPost, Path: v1 + "/auth/password-reset",
		Summary: "Ask for a password reset mail.", DefaultStatus: http.StatusAccepted,
		Description: "The answer is the same for an unknown email. " +
			"Errors: 400 invalid_request, 403 csrf_check_failed, 429 rate_limited.",
	}, accessPublic, ""), s.requestPasswordReset)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "confirmPasswordReset", Tags: []string{"Account"}, Method: http.MethodPost, Path: v1 + "/auth/password-reset/confirm",
		Summary:     "Set a new password with a reset token.",
		Description: "Errors: 400 invalid_request, 403 csrf_check_failed, 410 token_invalid, 429 rate_limited.",
	}, accessPublic, ""), s.confirmPasswordReset)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "acceptInvitation", Tags: []string{"Account"}, Method: http.MethodPost, Path: v1 + "/invitations/accept",
		Summary: "Accept an invitation and set the password.",
		Description: "The user is created, or a user whose access was removed from the same client returns with " +
			"the new password, and is logged in. The answer sets the session cookie. " +
			"Errors: 400 invalid_request, 403 csrf_check_failed, 409 email_in_use (the email already has an " +
			"account), 410 token_invalid, 429 rate_limited.",
	}, accessPublic, ""), s.acceptInvitation)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "getMe", Tags: []string{"Account"}, Method: http.MethodGet, Path: v1 + "/me",
		Summary: "The user and the organization of the caller.",
		Description: "An owner gets the whole organization, and create_instance_problem is null. A client user gets " +
			"only the id and the name of its organization, and create_instance_problem. " +
			"Errors: 401 unauthenticated.",
	}, accessLoggedIn, tenancy.ManageOwnAccount), s.getMe)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "changePassword", Tags: []string{"Account"}, Method: http.MethodPut, Path: v1 + "/me/password",
		Summary: "Change the password of the caller.",
		Description: "This session stays and every other session ends. Errors: 400 invalid_request (the " +
			"current password is wrong, or the new one breaks the rules), 401 unauthenticated, " +
			"403 csrf_check_failed or api_key_not_allowed.",
	}, accessLoggedInCookie, tenancy.ManageOwnAccount), s.changePassword)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "listAPIKeys", Tags: []string{"Account"}, Method: http.MethodGet, Path: v1 + "/api-keys",
		Summary:     "List the API keys of the caller.",
		Description: "The keys, newest first. The token is never shown again. Errors: 401 unauthenticated.",
	}, accessLoggedIn, tenancy.ManageOwnAccount), s.listAPIKeys)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "createAPIKey", Tags: []string{"Account"}, Method: http.MethodPost, Path: v1 + "/api-keys",
		Summary: "Create an API key.", DefaultStatus: http.StatusCreated,
		Description: "The answer holds the token. The token is shown once. " +
			"Errors: 400 invalid_request, 401 unauthenticated, 403 csrf_check_failed or api_key_not_allowed.",
	}, accessLoggedInCookie, tenancy.ManageOwnAccount), s.createAPIKey)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "revokeAPIKey", Tags: []string{"Account"}, Method: http.MethodDelete, Path: v1 + "/api-keys/{id}",
		Summary:     "Revoke an API key of the caller.",
		Description: "Errors: 401 unauthenticated, 403 csrf_check_failed, 404 not_found.",
	}, accessLoggedIn, tenancy.ManageOwnAccount), s.revokeAPIKey)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "lookupInvitation", Tags: []string{"Account"}, Method: http.MethodPost, Path: v1 + "/invitations/lookup",
		Summary: "Read the invitation of a token before it is accepted.",
		Description: "The token stays valid. Errors: 400 invalid_request, 403 csrf_check_failed, " +
			"410 token_invalid, 429 rate_limited.",
	}, accessPublic, ""), s.lookupInvitation)
	huma.Register(api, withAccess(huma.Operation{
		OperationID: "lookupPasswordReset", Tags: []string{"Account"}, Method: http.MethodPost, Path: v1 + "/auth/password-reset/lookup",
		Summary: "Read the email of a reset token before the new password is set.",
		Description: "The token stays valid. A disabled user gives 410. Errors: 400 invalid_request, " +
			"403 csrf_check_failed, 410 token_invalid, 429 rate_limited.",
	}, accessPublic, ""), s.lookupPasswordReset)
}

// POST /auth/login
func (s *server) login(ctx context.Context, in *loginInput) (*sessionOutput, error) {
	sess, user, err := s.Auth.Login(ctx, in.Body.Email, in.Body.Password, clientIPOf(ctx))
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	return &sessionOutput{SetCookie: s.sessionCookieFor(sess.Token), Body: userResource(user)}, nil
}

// POST /auth/logout
func (s *server) logout(ctx context.Context, _ *struct{}) (*logoutOutput, error) {
	if err := s.Auth.Logout(ctx, sessionToken(ctx)); err != nil {
		return nil, serviceError(ctx, err)
	}
	return &logoutOutput{SetCookie: s.sessionCookieFor("")}, nil
}

// POST /auth/password-reset
func (s *server) requestPasswordReset(ctx context.Context, in *emailInput) (*struct{}, error) {
	return nil, serviceError(ctx, s.Auth.RequestPasswordReset(ctx, string(in.Body.Email), clientIPOf(ctx)))
}

// POST /auth/password-reset/confirm
func (s *server) confirmPasswordReset(ctx context.Context, in *tokenPasswordInput) (*struct{}, error) {
	return nil, serviceError(ctx, s.Auth.ConfirmPasswordReset(ctx, in.Body.Token, string(in.Body.Password), clientIPOf(ctx)))
}

// POST /invitations/accept
func (s *server) acceptInvitation(ctx context.Context, in *tokenPasswordInput) (*sessionOutput, error) {
	sess, user, err := s.Auth.AcceptInvitation(ctx, in.Body.Token, string(in.Body.Password), clientIPOf(ctx))
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	return &sessionOutput{SetCookie: s.sessionCookieFor(sess.Token), Body: userResource(user)}, nil
}

// POST /invitations/lookup
func (s *server) lookupInvitation(ctx context.Context, in *lookupInvitationInput) (*invitationLookupOutput, error) {
	info, err := s.Auth.LookupInvitation(ctx, in.Body.Token, clientIPOf(ctx))
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	return &invitationLookupOutput{Body: invitationLookupResource(info)}, nil
}

// POST /auth/password-reset/lookup
func (s *server) lookupPasswordReset(ctx context.Context, in *lookupPasswordResetInput) (*passwordResetLookupOutput, error) {
	email, err := s.Auth.LookupPasswordReset(ctx, in.Body.Token, clientIPOf(ctx))
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	return &passwordResetLookupOutput{Body: passwordResetLookupJSON{Email: email}}, nil
}

// GET /me. An owner gets the whole organization. A client user gets only its id and name: the
// counts and the limits belong to the owner.
func (s *server) getMe(ctx context.Context, _ *struct{}) (*meOutput, error) {
	p := principalOf(ctx)
	q := gen.New(s.Pool)
	user, err := q.HTTPGetUser(ctx, p.UserID)
	if err != nil {
		return nil, serviceError(ctx, dbError(err))
	}
	problem, err := s.Tenancy.CreateInstanceProblem(ctx, p)
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	out := &meOutput{Body: meJSON{CreateInstanceProblem: problem, User: userResource(user)}}
	if p.Role == tenancy.RoleOwner {
		org, err := s.Tenancy.GetOrganization(ctx, p)
		if err != nil {
			return nil, serviceError(ctx, err)
		}
		out.Body.Organization = organizationResource(org, s.Config)
	} else {
		org, err := q.HTTPGetOrganizationSummary(ctx, p.OrganizationID)
		if err != nil {
			return nil, serviceError(ctx, dbError(err))
		}
		out.Body.Organization = organizationSummary(org)
	}
	return out, nil
}

// PUT /me/password. A password change ends every other session of the user.
func (s *server) changePassword(ctx context.Context, in *changePasswordInput) (*struct{}, error) {
	return nil, serviceError(ctx, s.Auth.ChangePassword(ctx, principalOf(ctx), sessionToken(ctx),
		in.Body.CurrentPassword, string(in.Body.NewPassword)))
}

// GET /api-keys
func (s *server) listAPIKeys(ctx context.Context, _ *struct{}) (*apiKeyListOutput, error) {
	keys, err := s.Auth.ListAPIKeys(ctx, principalOf(ctx))
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	items := make([]apiKeyJSON, 0, len(keys))
	for _, k := range keys {
		items = append(items, apiKeyResource(k, ""))
	}
	return &apiKeyListOutput{Body: itemsJSON[apiKeyJSON]{Items: items}}, nil
}

// POST /api-keys
func (s *server) createAPIKey(ctx context.Context, in *createAPIKeyInput) (*apiKeyOutput, error) {
	key, err := s.Auth.CreateAPIKey(ctx, principalOf(ctx), string(in.Body.Name))
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	return &apiKeyOutput{Body: apiKeyResource(key.ApiKey, key.Token)}, nil
}

// DELETE /api-keys/{id}
func (s *server) revokeAPIKey(ctx context.Context, in *idInput) (*struct{}, error) {
	return nil, serviceError(ctx, s.Auth.RevokeAPIKey(ctx, principalOf(ctx), in.ID))
}

// dbError maps pgx.ErrNoRows to 404 and any other error to 500.
func dbError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := apierr.As(err); ok {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return apierr.NotFound()
	}
	return apierr.Internal(err)
}
