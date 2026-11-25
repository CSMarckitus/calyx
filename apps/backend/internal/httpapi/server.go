package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/auth"
	"calyx/backend/internal/bulk"
	"calyx/backend/internal/clock"
	"calyx/backend/internal/config"
	"calyx/backend/internal/instances"
	"calyx/backend/internal/live"
	"calyx/backend/internal/tenancy"
)

// Deps is everything the HTTP layer needs.
type Deps struct {
	Config    config.Config
	Auth      *auth.Service
	Tenancy   *tenancy.Service
	Instances *instances.Service
	Bulk      *bulk.Service
	Pool      *pgxpool.Pool
	Clock     clock.Clock
	Logger    *slog.Logger
	// Live gives the health of the core and the stream of changes. A nil Live gives core "unknown".
	Live *live.Live
	// Version is the version of the backend binary.
	Version string
}

// AuthMode tells the middleware whether a route needs a session or an API key.
type AuthMode int

const (
	Public AuthMode = iota
	LoggedIn
)

// Route names a route for the middleware: its method, its http.ServeMux pattern and its access.
type Route struct {
	Method, Pattern string
	Auth            AuthMode
}

// server holds what the handlers and the middleware share.
type server struct {
	Deps
	log    *slog.Logger
	origin string // publicOrigin of PublicURL, in lower case
}

func newServer(d Deps) *server {
	s := &server{Deps: d, log: d.Logger}
	if d.Config.PublicURL != nil {
		s.origin = publicOrigin(d.Config.PublicURL)
	}
	return s
}

// NewServer registers the Huma API, the three paths of the mail links, /healthz, /openapi.json and the /api/v1/
// catch-all. Each Huma operation has the outer wrapper and the API middleware; each plain route
// has the outer wrapper.
func NewServer(d Deps) http.Handler {
	mux, _ := newMux(d)
	return mux
}

// newMux is NewServer, and also returns the Huma API, so that a test can add operations.
func newMux(d Deps) (*http.ServeMux, huma.API) {
	if d.Config.PublicURL == nil {
		panic("httpapi: NewServer needs Config.PublicURL")
	}
	s := newServer(d)
	mux := http.NewServeMux()
	api := newAPI(mux, d)
	public := func(method, pattern string, h http.HandlerFunc) {
		mux.Handle(method+" "+pattern, s.chain(Route{Method: method, Pattern: pattern, Auth: Public}, h))
	}
	public("GET", "/healthz", s.healthz)
	public("GET", "/openapi.json", s.serveDocument(api))
	// Only GET: the mux answers 405 to the other methods.
	public("GET", "/invite", pageText(inviteText))
	public("GET", "/reset-password", pageText(resetText))
	public("GET", "/instances/{id}", pageText(instanceText))
	mux.Handle("/api/v1/", s.chain(Route{}, s.notFound))
	return mux, api
}

// notFound answers every /api/v1 path that no route matches, also a known path with another
// method, so that every /api/v1 answer is a problem document.
func (s *server) notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, s.log, apierr.NotFound())
}
