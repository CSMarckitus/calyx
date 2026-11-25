//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeGraph stands in for the Graph API of Meta. The core's Meta provider calls it. It answers like
// the core's own testutil.FakeGraphAPI, which another module cannot import.
type fakeGraph struct {
	URL string

	mu         sync.Mutex
	validToken string
	sent       int
	requests   []graphRequest
}

type graphRequest struct {
	Method, Path string
	Body         map[string]any
}

func newFakeGraph(t *testing.T) *fakeGraph {
	g := &fakeGraph{validToken: "good-token"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v23.0/{id}", g.credentials)
	mux.HandleFunc("POST /v23.0/{id}/messages", g.messages)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		g.record(r, nil)
		writeGraph(w, http.StatusNotFound, map[string]any{"error": map[string]any{"message": "unknown path", "code": 100}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	g.URL = srv.URL
	return g
}

func writeGraph(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (g *fakeGraph) record(r *http.Request, body map[string]any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.requests = append(g.requests, graphRequest{Method: r.Method, Path: r.URL.Path, Body: body})
}

// authorized records the request and reports whether the token is valid. It answers 401 with code
// 190 when it is not.
func (g *fakeGraph) authorized(w http.ResponseWriter, r *http.Request, body map[string]any) bool {
	g.mu.Lock()
	ok := r.Header.Get("Authorization") == "Bearer "+g.validToken
	g.mu.Unlock()
	g.record(r, body)
	if !ok {
		writeGraph(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"message": "Invalid OAuth access token", "code": 190}})
	}
	return ok
}

func (g *fakeGraph) credentials(w http.ResponseWriter, r *http.Request) {
	if g.authorized(w, r, nil) {
		writeGraph(w, http.StatusOK, map[string]any{"id": r.PathValue("id")})
	}
}

func (g *fakeGraph) messages(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	if !g.authorized(w, r, body) {
		return
	}
	g.mu.Lock()
	g.sent++
	n := g.sent
	g.mu.Unlock()
	writeGraph(w, http.StatusOK, map[string]any{"messages": []any{map[string]any{"id": fmt.Sprintf("wamid.%d", n)}}})
}

func (g *fakeGraph) setValidToken(token string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.validToken = token
}

// sends returns the POST .../messages requests, in order.
func (g *fakeGraph) sends() []graphRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []graphRequest
	for _, r := range g.requests {
		if r.Method == http.MethodPost && strings.HasSuffix(r.Path, "/messages") {
			out = append(out, r)
		}
	}
	return out
}

// has reports whether the fake received a request with this method and path.
func (g *fakeGraph) has(method, path string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, r := range g.requests {
		if r.Method == method && r.Path == path {
			return true
		}
	}
	return false
}
