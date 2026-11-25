package testutil

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

type GraphRequest struct {
	Method, Path, Authorization string
	Body                        map[string]any
}

type graphResponse struct {
	status int
	body   any
}

// FakeGraphAPI stands in for Meta's Graph API. By default GET /v23.0/<id> and
// POST /v23.0/<id>/messages succeed with ValidToken and answer 401 (code 190) otherwise.
type FakeGraphAPI struct {
	URL string

	mu         sync.Mutex
	validToken string
	next       *graphResponse
	requests   []GraphRequest
	sent       int
	server     *httptest.Server
}

func NewFakeGraphAPI(t *testing.T) *FakeGraphAPI {
	f := &FakeGraphAPI{validToken: "good-token"}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	f.URL = f.server.URL
	t.Cleanup(f.server.Close)
	return f
}

func (f *FakeGraphAPI) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)

	f.mu.Lock()
	f.requests = append(f.requests, GraphRequest{Method: r.Method, Path: r.URL.Path, Authorization: r.Header.Get("Authorization"), Body: body})
	answer := graphResponse{status: http.StatusOK}
	switch {
	case r.Header.Get("Authorization") != "Bearer "+f.validToken:
		answer = graphResponse{status: http.StatusUnauthorized, body: map[string]any{
			"error": map[string]any{"message": "Invalid OAuth access token", "code": 190},
		}}
	case f.next != nil && r.Method == http.MethodPost:
		answer, f.next = *f.next, nil
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/messages"):
		f.sent++
		answer.body = map[string]any{"messages": []any{map[string]any{"id": fmt.Sprintf("wamid.%d", f.sent)}}}
	default:
		answer.body = map[string]any{"id": strings.TrimPrefix(r.URL.Path, "/v23.0/")}
	}
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(answer.status)
	_ = json.NewEncoder(w).Encode(answer.body)
}

// SetValidToken changes the token the fake accepts (an "expired" token is any other).
func (f *FakeGraphAPI) SetValidToken(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.validToken = token
}

// NextSendResponse makes the next POST answer status and body, once.
func (f *FakeGraphAPI) NextSendResponse(status int, body any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next = &graphResponse{status: status, body: body}
}

func (f *FakeGraphAPI) Requests() []GraphRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]GraphRequest(nil), f.requests...)
}

// Close stops the server, so Meta looks unreachable.
func (f *FakeGraphAPI) Close() { f.server.Close() }
