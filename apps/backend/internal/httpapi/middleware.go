package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/tenancy"
)

type authKind int

const (
	authNone authKind = iota
	authCookie
	authKey
)

// statusClientClosed is the nginx status for a client that closed the request. The log uses it, and
// toProblem marks with it a problem that nobody writes.
const statusClientClosed = 499

const sessionCookie = "calyx_session"

type ctxKey int

const (
	keyRequestID ctxKey = iota
	keyPrincipal
	keyAuthKind
	keySessionToken
	keyBearer
	keyClientIP
	keyLogger
	keyRequestInfo
)

// maxUserAgentLog is the most bytes of User-Agent that the log keeps.
const maxUserAgentLog = 200

// requestInfo holds what withPrincipal learns, for the log line that outer writes. outer owns the
// record, so the context carries a pointer.
type requestInfo struct {
	auth     authKind
	apiKeyID *uuid.UUID
}

func (k authKind) logName() string {
	switch k {
	case authCookie:
		return "cookie"
	case authKey:
		return "api_key"
	}
	return "none"
}

// truncateUserAgent cuts s at the last whole UTF-8 character at or before byte maxUserAgentLog.
func truncateUserAgent(s string) string {
	if len(s) <= maxUserAgentLog {
		return s
	}
	cut := maxUserAgentLog
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func withRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, keyRequestID, id)
}

// requestID returns the id that the middleware gave to the request.
func requestID(r *http.Request) string { return requestIDOf(r.Context()) }

func requestIDOf(ctx context.Context) string {
	id, _ := ctx.Value(keyRequestID).(string)
	return id
}

// withLogger puts the logger of the server in the context, so that toProblem can log an internal
// error also where no server is at hand (the Huma error bridge).
func withLogger(ctx context.Context, log *slog.Logger) context.Context {
	return context.WithValue(ctx, keyLogger, log)
}

func loggerOf(ctx context.Context) *slog.Logger {
	if log, ok := ctx.Value(keyLogger).(*slog.Logger); ok && log != nil {
		return log
	}
	return slog.Default()
}

// principal returns the user of the request. It panics on a Public route: a handler that needs a
// principal must be registered as LoggedIn.
func principal(r *http.Request) tenancy.Principal { return principalOf(r.Context()) }

// principalOf is principal for an operation, which gets the context of the request.
func principalOf(ctx context.Context) tenancy.Principal {
	p, ok := ctx.Value(keyPrincipal).(tenancy.Principal)
	if !ok {
		panic("httpapi: principal called on a request without authentication")
	}
	return p
}

func authKindOf(ctx context.Context) authKind {
	k, _ := ctx.Value(keyAuthKind).(authKind)
	return k
}

// sessionToken returns the session token of a cookie request, and "" for an API key.
func sessionToken(ctx context.Context) string {
	s, _ := ctx.Value(keySessionToken).(string)
	return s
}

// bearerOf returns the API key of the request, and "" for a login cookie.
func bearerOf(ctx context.Context) string {
	s, _ := ctx.Value(keyBearer).(string)
	return s
}

// clientIP returns the address that the middleware worked out for the request.
func clientIP(r *http.Request) string { return clientIPOf(r.Context()) }

func clientIPOf(ctx context.Context) string {
	s, _ := ctx.Value(keyClientIP).(string)
	return s
}

// computeClientIP returns the host of RemoteAddr. With trustProxy it returns the last entry of
// X-Forwarded-For when that entry is an IP address: the proxy appends the address it saw, so the
// last entry is the one a client cannot forge.
func computeClientIP(r *http.Request, trustProxy bool) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !trustProxy {
		return host
	}
	// The proxy appends to the last header line, so the last entry of the last line is the one a
	// client cannot forge.
	lines := r.Header.Values("X-Forwarded-For")
	if len(lines) == 0 {
		return host
	}
	xff := lines[len(lines)-1]
	last := strings.TrimSpace(xff[strings.LastIndex(xff, ",")+1:])
	if net.ParseIP(last) == nil {
		return host
	}
	return last
}

// statusRecorder keeps the status for the log. Unwrap lets http.ResponseController reach the real
// writer (Flush for SSE).
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(p []byte) (int, error) {
	if !s.wrote {
		s.status = http.StatusOK
		s.wrote = true
	}
	return s.ResponseWriter.Write(p)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// publicOrigin returns scheme://host of u, in lower case, without the default port.
func publicOrigin(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port == "" || (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		return scheme + "://" + host
	}
	return scheme + "://" + host + ":" + port
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// chain wraps one plain route (a page, /healthz or the catch-all) in the outer wrapper. These
// routes are public and outside /api/v1, so they need no authentication and no CSRF check. A
// route with an empty Pattern is the catch-all and is named "unmatched" in the log.
func (s *server) chain(route Route, h http.HandlerFunc) http.Handler {
	name := route.Method + " " + route.Pattern
	if route.Pattern == "" {
		name = "unmatched"
	}
	return s.outer(name, h)
}

// outer is the wrapper of every route, plain or Huma: recover -> request id -> client IP -> log.
// name is the route in the log.
func (s *server) outer(name string, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := uuid.NewString()
		w.Header().Set("X-Request-Id", id)
		rec := &statusRecorder{ResponseWriter: w}
		start := time.Now()
		// Replace any client value: Huma forwards this header to each tool call of /mcp.
		if name == "POST "+mcpPath {
			r.Header.Set(mcpRequestIDHeader, id)
		}
		mcpID, _ := r.Context().Value(mcpRequestIDKey).(string)
		ctx := withRequestID(r.Context(), id)
		ctx = withLogger(ctx, s.log)
		ctx = context.WithValue(ctx, keyClientIP, computeClientIP(r, s.Config.TrustProxy))
		info := &requestInfo{}
		ctx = context.WithValue(ctx, keyRequestInfo, info)
		r = r.WithContext(ctx)

		defer func() {
			if p := recover(); p != nil {
				if err, ok := p.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(p)
				}
				// toProblem logs the panic as an internal error, with the stack.
				prob := toProblem(ctx, fmt.Errorf("panic in a handler (route %s): %v\n%s", name, p, debug.Stack()))
				if !rec.wrote {
					writeProblem(rec, prob)
				}
			}
			status := rec.status
			if status == 0 {
				status = http.StatusOK
				if r.Context().Err() != nil {
					status = statusClientClosed
				}
			}
			if name == "GET /healthz" && status == http.StatusOK {
				return
			}
			level := slog.LevelInfo
			if status >= 500 {
				level = slog.LevelError
			}
			attrs := []any{
				"method", r.Method, "route", name, "status", status,
				"duration_ms", time.Since(start).Milliseconds(), "request_id", id,
				"user_agent", truncateUserAgent(r.UserAgent()), "auth", info.auth.logName(),
			}
			if info.apiKeyID != nil {
				attrs = append(attrs, "api_key_id", info.apiKeyID.String())
			}
			if mcpID != "" {
				attrs = append(attrs, "mcp_request_id", mcpID)
			}
			s.log.Log(context.Background(), level, "HTTP request.", attrs...)
		}()

		h(rec, r)
	})
}

// authenticate reads the API key or the session cookie, and puts the principal in the context.
func (s *server) authenticate(r *http.Request) (*http.Request, error) {
	if r.Header.Get("Authorization") != "" {
		return s.authenticateAPIKey(r)
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return r, apierr.New(401, apierr.CodeUnauthenticated, "Log in or send an API key.")
	}
	p, err := s.Auth.Authenticate(r.Context(), c.Value, "")
	if err != nil {
		return r, err
	}
	return withPrincipal(r, p, authCookie, c.Value, ""), nil
}

// authenticateAPIKey reads only the API key and ignores a session cookie. /mcp uses it directly.
func (s *server) authenticateAPIKey(r *http.Request) (*http.Request, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return r, apierr.New(401, apierr.CodeUnauthenticated, "This endpoint needs an API key in the Authorization header.")
	}
	scheme, rest, _ := strings.Cut(auth, " ")
	rest = strings.TrimSpace(rest)
	if !strings.EqualFold(scheme, "Bearer") || rest == "" {
		return r, apierr.New(401, apierr.CodeUnauthenticated, "Use Authorization: Bearer <API key>.")
	}
	p, err := s.Auth.Authenticate(r.Context(), "", rest)
	if err != nil {
		return r, err
	}
	return withPrincipal(r, p, authKey, "", rest), nil
}

// withPrincipal returns r with the principal, the kind of authentication and the credentials in its
// context: the session token for a cookie, the bearer for an API key. A stream checks them again.
func withPrincipal(r *http.Request, p tenancy.Principal, kind authKind, token, bearer string) *http.Request {
	ctx := context.WithValue(r.Context(), keyPrincipal, p)
	ctx = context.WithValue(ctx, keyAuthKind, kind)
	ctx = context.WithValue(ctx, keySessionToken, token)
	ctx = context.WithValue(ctx, keyBearer, bearer)
	if info, ok := ctx.Value(keyRequestInfo).(*requestInfo); ok {
		info.auth = kind
		info.apiKeyID = p.APIKeyID
	}
	return r.WithContext(ctx)
}

// checkOrigin is the CSRF check. A cookie request that changes state needs the right Origin. A
// Public API route checks Origin only when the browser sends it. API key requests are not checked.
func (s *server) checkOrigin(r *http.Request, route Route) error {
	if !isMutating(r.Method) {
		return nil
	}
	origin := strings.ToLower(r.Header.Get("Origin"))
	switch {
	case route.Auth == LoggedIn && authKindOf(r.Context()) == authCookie:
		if origin != s.origin {
			return apierr.New(403, apierr.CodeCSRFCheckFailed, "The Origin header must be "+s.origin+".")
		}
	case route.Auth == Public && strings.HasPrefix(route.Pattern, "/api/v1/"):
		if origin != "" && origin != s.origin {
			return apierr.New(403, apierr.CodeCSRFCheckFailed, "The Origin header must be "+s.origin+".")
		}
	}
	return nil
}
