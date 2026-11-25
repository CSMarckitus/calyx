package httpapi

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/danielgtaylor/huma/v2"

	"calyx/backend/internal/apierr"
)

// OpenAPIDocument returns the OpenAPI document of the API: indented JSON with one trailing
// newline. It builds the API with empty dependencies: registration reads only types and never
// calls a service. "backend openapi" prints it.
func OpenAPIDocument() ([]byte, error) {
	return marshalDocument(newAPI(http.NewServeMux(), Deps{}))
}

// marshalDocument returns the document of api. Huma writes the keys of each object in sorted
// order, so the bytes do not change from one build to the next.
func marshalDocument(api huma.API) ([]byte, error) {
	b, err := json.MarshalIndent(api.OpenAPI(), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// serveDocument returns the handler of GET /openapi.json. It builds the document of api once, on
// the first request: the build takes milliseconds, and the tests create many servers.
func (s *server) serveDocument(api huma.API) http.HandlerFunc {
	doc := sync.OnceValues(func() ([]byte, error) { return marshalDocument(api) })
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := doc()
		if err != nil {
			writeError(w, r, s.log, apierr.Internal(err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}
}
