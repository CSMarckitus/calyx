// Command fakebackend is an HTTP server that answers like the Calyx backend. The VB6 tests and
// the Windows probe use it. It never opens an outbound connection and never sends a message.
//
// Usage: fakebackend -addr 127.0.0.1:8080
package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const apiKey = "test-key"

// server holds the state of the fake: the last request and the count of each idempotency key.
type server struct {
	hold time.Duration // how long scenario 5 stays silent

	mu   sync.Mutex
	last map[string]any
	keys map[string]int
}

func newServer() *server {
	return &server{
		hold: 20 * time.Second,
		last: map[string]any{"method": "", "path": "", "headers": map[string]string{}, "bodyHex": ""},
		keys: map[string]int{},
	}
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_fake/last", s.serveLast)
	mux.HandleFunc("POST /api/v1/instances/{id}/messages", s.api(s.sendMessage))
	mux.HandleFunc("GET /api/v1/instances", s.api(s.listInstances))
	mux.HandleFunc("GET /api/v1/instances/{id}", s.api(s.getInstance))
	mux.HandleFunc("GET /api/v1/instances/{id}/bulk-jobs", s.api(s.listBulkJobs))
	mux.HandleFunc("POST /api/v1/instances/{id}/bulk-jobs", s.api(s.createBulkJob))
	mux.HandleFunc("GET /api/v1/bulk-jobs/{id}", s.api(s.getBulkJob))
	mux.HandleFunc("POST /api/v1/bulk-jobs/{id}/cancel", s.api(s.getBulkJob))
	return mux
}

// api records the request, checks the key, and handles the scenarios that apply to all routes.
// The handler of the route runs for scenarios 1, 6 and the send-only ones.
func (s *server) api(next func(w http.ResponseWriter, r *http.Request, scenario string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		if r.Header.Get("Authorization") != "Bearer "+apiKey {
			problem(w, http.StatusUnauthorized, "unauthenticated", "Missing or wrong API key.")
			return
		}
		scenario := scenarioOf(r)
		switch scenario {
		case "4":
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "<html><body><h1>502 Bad Gateway</h1></body></html>")
		case "5":
			s.holdAndClose(w, r)
		case "8":
			w.Header().Set("Location", "https://example.invalid/")
			w.WriteHeader(http.StatusFound)
		default:
			next(w, r, scenario)
		}
	}
}

// scenarioOf returns the scenario number. The header wins over the ID in the path, so a
// route without an ID (the list of instances) can pick a scenario too.
func scenarioOf(r *http.Request) string {
	if h := r.Header.Get("X-Fake-Scenario"); h != "" {
		return h
	}
	id := r.PathValue("id")
	const prefix = "00000000-0000-4000-8000-00000000000"
	if len(id) == len(prefix)+1 && strings.HasPrefix(id, prefix) {
		return id[len(prefix):]
	}
	return "1"
}

// record saves the request for /_fake/last. The body is in hex, so the tests see the exact bytes.
func (s *server) record(r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	headers := map[string]string{}
	for k, v := range r.Header {
		headers[k] = v[0]
	}
	s.mu.Lock()
	s.last = map[string]any{
		"method": r.Method, "path": r.URL.Path, "headers": headers, "bodyHex": hex.EncodeToString(body),
	}
	s.mu.Unlock()
}

func (s *server) serveLast(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, s.last)
}

// holdAndClose stays silent, then closes the connection without an answer.
func (s *server) holdAndClose(w http.ResponseWriter, r *http.Request) {
	select {
	case <-time.After(s.hold):
	case <-r.Context().Done():
		return
	}
	if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
		conn.Close()
	}
}

func (s *server) sendMessage(w http.ResponseWriter, r *http.Request, scenario string) {
	switch scenario {
	case "2":
		s.mu.Lock()
		s.keys[r.Header.Get("Idempotency-Key")]++
		n := s.keys[r.Header.Get("Idempotency-Key")]
		s.mu.Unlock()
		if n <= 2 {
			w.Header().Set("Retry-After", "12")
			problem(w, http.StatusConflict, "send_in_progress", "The core still sends this message.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"message_id": sendMessageID, "replayed": true})
	case "3":
		problem(w, http.StatusGatewayTimeout, "send_outcome_unknown", "The core did not report the result.")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"message_id": sendMessageID, "replayed": false})
	}
}

func (s *server) listInstances(w http.ResponseWriter, _ *http.Request, scenario string) {
	writeJSON(w, http.StatusOK, map[string]any{"items": []any{instanceJSON(instanceID, scenario == "6")}})
}

func (s *server) getInstance(w http.ResponseWriter, r *http.Request, scenario string) {
	writeJSON(w, http.StatusOK, instanceJSON(r.PathValue("id"), scenario == "6"))
}

func (s *server) listBulkJobs(w http.ResponseWriter, _ *http.Request, scenario string) {
	writeJSON(w, http.StatusOK, map[string]any{"items": []any{jobJSON(jobID, scenario == "6")}})
}

func (s *server) createBulkJob(w http.ResponseWriter, _ *http.Request, scenario string) {
	writeJSON(w, http.StatusCreated, jobJSON(jobID, scenario == "6"))
}

// getBulkJob answers both the read and the cancel route: the job has the same shape.
func (s *server) getBulkJob(w http.ResponseWriter, r *http.Request, scenario string) {
	writeJSON(w, http.StatusOK, jobJSON(r.PathValue("id"), scenario == "6"))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// problem writes an error in the shape of the backend (problem+json).
func problem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type": "about:blank", "title": http.StatusText(status), "status": status,
		"detail": detail, "code": code,
	})
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "address to listen on")
	flag.Parse()
	fmt.Println("fakebackend listens on", *addr)
	log.Fatal(http.ListenAndServe(*addr, newServer().handler()))
}
