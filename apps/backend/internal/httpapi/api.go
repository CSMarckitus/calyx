package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/tenancy"
)

// access is who may call an operation. withAccess puts it in the operation's Metadata.
type access string

const (
	accessPublic   access = "public"
	accessLoggedIn access = "logged_in" // also needs a tenancy.Action
	// accessLoggedInCookie is accessLoggedIn for a login cookie only: an API key gets 403.
	accessLoggedInCookie access = "logged_in_cookie"
)

const (
	metaAccess = "access"
	metaAction = "action"
)

const (
	// bodyReadTimeout is the time a client has to send a body, except for bulk creation.
	bodyReadTimeout = 5 * time.Second
	smallBodyLimit  = 64 << 10 // 64 KiB: every JSON body except bulk jobs
)

// withAccess returns op with its access level and, for accessLoggedIn, its action and its
// security. It also sets the body limits that op leaves at zero: 64 KiB and 5 s. Registration
// panics when an operation did not go through withAccess.
func withAccess(op huma.Operation, acc access, action tenancy.Action) huma.Operation {
	// Clone, so a mark that is already there (the MCP tool mark) stays and the caller's map is not changed.
	op.Metadata = maps.Clone(op.Metadata)
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[metaAccess] = acc
	if action != "" {
		op.Metadata[metaAction] = action
	}
	switch acc {
	case accessLoggedIn:
		op.Security = []map[string][]string{{"session": {}}, {"apiKey": {}}}
	case accessLoggedInCookie:
		op.Security = []map[string][]string{{"session": {}}}
	}
	if op.MaxBodyBytes == 0 {
		op.MaxBodyBytes = smallBodyLimit
	}
	if op.BodyReadTimeout == 0 {
		op.BodyReadTimeout = bodyReadTimeout
	}
	return op
}

// checkAccess runs when an operation is added to the document, at registration.
func checkAccess(_ *huma.OpenAPI, op *huma.Operation) {
	switch acc, _ := op.Metadata[metaAccess].(access); acc {
	case accessPublic:
	case accessLoggedIn, accessLoggedInCookie:
		if a, _ := op.Metadata[metaAction].(tenancy.Action); a == "" {
			panic("httpapi: operation " + op.OperationID + " is logged_in and has no action")
		}
	default:
		panic("httpapi: operation " + op.OperationID + " has no access level")
	}
}

// addRoles marks an operation with the roles that the action allows, so the API documentation
// page can show each role only its own operations. The roles come from tenancy, never by hand.
func addRoles(_ *huma.OpenAPI, op *huma.Operation) {
	action, _ := op.Metadata[metaAction].(tenancy.Action)
	if action == "" {
		return
	}
	roles := tenancy.RolesFor(action)
	if len(roles) == 0 {
		panic("httpapi: operation " + op.OperationID + " has an action that no role allows")
	}
	names := make([]string, len(roles))
	for i, r := range roles {
		names[i] = string(r)
	}
	if op.Extensions == nil {
		op.Extensions = map[string]any{}
	}
	op.Extensions["x-calyx-roles"] = names
}

// schemaName names a schema for SDK authors. It starts from Huma's name of the Go type and
// removes the Go conventions of this package: itemsJSON[instanceJSON] becomes InstanceList,
// loginInput's body becomes LoginRequest, instanceJSON becomes Instance.
func schemaName(t reflect.Type, hint string) string {
	name := huma.DefaultSchemaNamer(t, hint)
	if item, ok := strings.CutPrefix(name, "ItemsJSON"); ok {
		return strings.TrimSuffix(item, "JSON") + "List"
	}
	if op, ok := strings.CutSuffix(name, "InputBody"); ok {
		return op + "Request"
	}
	return strings.TrimSuffix(name, "JSON")
}

// addDefaultError documents the problem as the default response of op. Huma adds it only to an
// operation with at most one documented response, so an operation with two (204 and 202) has
// none without this function.
func addDefaultError(oapi *huma.OpenAPI, op *huma.Operation) {
	if _, ok := op.Responses["default"]; ok {
		return
	}
	op.Responses["default"] = &huma.Response{
		Description: "Error",
		Content: map[string]*huma.MediaType{"application/problem+json": {
			Schema: oapi.Components.Schemas.Schema(reflect.TypeFor[problem](), true, "Error"),
		}},
	}
}

// bodyMessage is a message for people, in the errors[] of a 400 answer.
type bodyMessage string

func (e bodyMessage) Error() string { return string(e) }

// jsonFormat is Huma's JSON format with two changes. A decode error gets a fixed message: the
// message of encoding/json can quote a part of the rejected input. A problem with status 499
// is not written: the client left (row 1 of the bridge).
var jsonFormat = huma.Format{
	Marshal: func(w io.Writer, v any) error {
		if p, ok := v.(*problem); ok && p.Status == statusClientClosed {
			return nil
		}
		return huma.DefaultJSONFormat.Marshal(w, v)
	},
	Unmarshal: func(data []byte, v any) error {
		err := huma.DefaultJSONFormat.Unmarshal(data, v)
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		switch {
		case errors.As(err, &syntaxErr):
			return bodyMessage("The body is not valid JSON.")
		case errors.As(err, &typeErr) && typeErr.Field == "":
			return bodyMessage("The body must hold one JSON object.")
		case errors.As(err, &typeErr):
			return bodyMessage(fmt.Sprintf("Field %s has the wrong type.", typeErr.Field))
		}
		return err
	},
}

// startHereText is the first text of the API documentation page, in Markdown.
const startHereText = "## Start here\n\n" +
	"1. Get an API key from the person who manages Calyx, or create one on the Calyx web page. A key starts with `cx_`.\n" +
	"2. Send each request to `/api/v1` on the address of Calyx, with the header `Authorization: Bearer <key>`.\n" +
	"3. Call `GET /api/v1/me` to check the key.\n" +
	"4. Send a message with `POST /api/v1/instances/{id}/messages` and an `Idempotency-Key` header. A second request with the\n" +
	"   same key never sends the message again.\n" +
	"5. An error answer has the type `application/problem+json`. Read its `code`. When the answer has a `Retry-After` header,\n" +
	"   wait that number of seconds before the next request."

// newAPI creates the Huma API on mux and registers the operations. Each operation gets the outer
// wrapper, then the API middleware. The API serves no document, docs or schemas: /openapi.json is a
// plain handler.
func newAPI(mux *http.ServeMux, d Deps) huma.API {
	s := newServer(d)
	config := huma.Config{
		OpenAPI: &huma.OpenAPI{
			OpenAPI: "3.1.0",
			Info: &huma.Info{
				Title: "Calyx backend API",
				// The API version, not the build version: the committed copy must not change with
				// each build.
				Version: "v1",
				// The API description is a contract, like the .proto files, so it is Apache-2.0.
				// The backend code stays AGPL-3.0-only.
				License:     &huma.License{Name: "Apache-2.0"},
				Description: startHereText,
			},
			// The order is the order of the sections on the documentation page.
			Tags: []*huma.Tag{
				{Name: "Account", Description: "Your user, your password and your API keys."},
				{Name: "Organization", Description: "The organization and its settings. Only the owner can call these operations."},
				{Name: "Clients", Description: "The clients of the organization and their users. Only the owner can call these operations."},
				{Name: "Instances", Description: "The WhatsApp numbers: create, read, change, remove and pair them."},
				{Name: "Messages", Description: "Send one message from an instance."},
				{Name: "Bulk jobs", Description: "Send one text or a list of messages to many recipients, with the pacing of the core."},
				{Name: "Events", Description: "The stream of changes for the Calyx web page. Programs use webhooks instead."},
			},
			Components: &huma.Components{
				Schemas: huma.NewMapRegistry("#/components/schemas/", schemaName),
				SecuritySchemes: map[string]*huma.SecurityScheme{
					"session": {Type: "apiKey", In: "cookie", Name: sessionCookie,
						Description: "The browser sends the login cookie of Calyx. Programs cannot use this scheme."},
					"apiKey": {Type: "http", Scheme: "bearer",
						Description: "Send an API key in the header `Authorization: Bearer <key>`. Programs use this scheme."},
				},
			},
			// The path texts come before the MCP server, which adds its own function below.
			OnAddOperation: []huma.AddOpFunc{checkAccess, addRoles, addDefaultError, describePathParams},
		},
		Formats:       map[string]huma.Format{"application/json": jsonFormat, "json": jsonFormat},
		DefaultFormat: "application/json",
	}
	api := humago.New(outerMux{mux: mux, s: s}, config)
	if d.Config.MCPEnabled {
		api = attachMCP(api)
	}
	api.UseMiddleware(s.apiMiddleware)
	s.registerAuthOps(api)
	s.registerOrganizationOps(api)
	s.registerClientOps(api)
	s.registerInstanceOps(api)
	s.registerBulkOps(api)
	s.registerEventOps(api)
	describeSchemas(api)
	return api
}

// outerMux registers each Huma operation on the ServeMux inside the outer wrapper. The pattern
// ("POST /api/v1/...") is the route name in the log.
type outerMux struct {
	mux *http.ServeMux
	s   *server
}

func (m outerMux) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	m.mux.Handle(pattern, m.s.outer(pattern, h))
}

// ServeHTTP is called only by Huma, for a tool call of /mcp. The production handler is the
// ServeMux itself, so a network request never comes here. For that reason the header is safe to
// trust here and nowhere else.
func (m outerMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if id := r.Header.Get(mcpRequestIDHeader); id != "" {
		r = r.WithContext(context.WithValue(r.Context(), mcpRequestIDKey, id))
	}
	m.mux.ServeHTTP(w, r)
}

// apiMiddleware runs before Huma reads the input: no-store, authentication, the CSRF rule, the
// action of the operation, then the content type of a body. /mcp gets the API key check only.
// Its own answers go through writeError, never through Huma's error functions, so they keep
// their status.
func (s *server) apiMiddleware(ctx huma.Context, next func(huma.Context)) {
	r, w := humago.Unwrap(ctx)
	ctx.SetHeader("Cache-Control", "no-store")
	op := ctx.Operation()
	acc, _ := op.Metadata[metaAccess].(access)
	switch {
	case op.Path == mcpPath && acc == "":
		// Only the transport has no access mark; a marked operation here keeps its normal checks.
		// Every MCP method needs an API key; each tool call runs its own action check.
		var err error
		if r, err = s.authenticateAPIKey(r); err != nil {
			writeError(w, r, s.log, err)
			return
		}
		next(huma.WithContext(ctx, r.Context()))
		return
	case acc == "":
		// An unmarked operation must never count as public. checkAccess stops this at
		// registration, except for an operation that is registered on the adapter.
		writeError(w, r, s.log, apierr.Internal(fmt.Errorf("operation %s %s has no access mark", op.Method, op.Path)))
		return
	}
	route := Route{Method: op.Method, Pattern: op.Path, Auth: Public}
	loggedIn := acc == accessLoggedIn || acc == accessLoggedInCookie
	if loggedIn {
		route.Auth = LoggedIn
		var err error
		if r, err = s.authenticate(r); err != nil {
			writeError(w, r, s.log, err)
			return
		}
	}
	if err := s.checkOrigin(r, route); err != nil {
		writeError(w, r, s.log, err)
		return
	}
	if loggedIn {
		action, _ := op.Metadata[metaAction].(tenancy.Action)
		if err := tenancy.Allow(principal(r), action); err != nil {
			writeError(w, r, s.log, err)
			return
		}
		// Checked after Allow, so a caller without the right still gets its own error first.
		if acc == accessLoggedInCookie {
			if authKindOf(r.Context()) == authKey {
				s.log.Warn("API key refused.", "request_id", requestID(r), "route", op.Method+" "+op.Path, "api_key_id", principal(r).APIKeyID)
				writeError(w, r, s.log, apierr.Forbidden(apierr.CodeAPIKeyNotAllowed, "This operation needs a login in a browser. An API key cannot use it."))
				return
			}
		}
	}
	// After authorization, so a refused caller gets that error first; still before Huma reads the body.
	if op.RequestBody != nil {
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" {
			writeError(w, r, s.log, apierr.Invalid("Content-Type must be application/json."))
			return
		}
	}
	next(huma.WithContext(ctx, r.Context()))
}
