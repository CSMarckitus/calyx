package backend

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
)

// The browser flows and one account action. The SDK does not wrap them.
var excludedOperationIDs = []string{
	"login", "logout", "requestPasswordReset", "confirmPasswordReset",
	"acceptInvitation", "changePassword", "acknowledgeWhatsAppWeb",
	"createAPIKey", "inviteClientUser",
	"lookupInvitation", "lookupPasswordReset", "cancelInvitation", "removeClientAccess", "followEvents",
}

func TestOperationTableMatchesOpenAPI(t *testing.T) {
	data, err := os.ReadFile("../../../shared-types/openapi/backend.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	type route struct{ method, path string }
	spec := map[string]route{}
	for path, item := range doc.Paths {
		for method, op := range item {
			if op.OperationID != "" {
				spec[op.OperationID] = route{strings.ToUpper(method), path}
			}
		}
	}
	excluded := map[string]bool{}
	for _, id := range excludedOperationIDs {
		excluded[id] = true
	}

	if len(operations) != 23 {
		t.Errorf("table has %d rows, want 23", len(operations))
	}
	for id := range spec {
		if _, ok := operations[id]; !ok && !excluded[id] {
			t.Errorf("operation %q is not in the table and not excluded", id)
		}
	}
	for id, row := range operations {
		if excluded[id] {
			t.Errorf("excluded operation %q is in the table", id)
		}
		want, ok := spec[id]
		if !ok {
			t.Errorf("operation %q is not in the OpenAPI document", id)
			continue
		}
		if row.method != want.method || row.path != want.path {
			t.Errorf("%s: got %s %s, want %s %s", id, row.method, row.path, want.method, want.path)
		}
	}
}

// seen is what the test server recorded about one request.
type seen struct {
	method, path, query, body, contentType, idemKey string
}

// methodCase is one row of TestMethods.
type methodCase struct {
	name   string
	call   func(c *Client) (any, error)
	method string
	path   string
	query  string
	body   string // the expected JSON body; empty means no body and no Content-Type
	answer string
	status int // the status of the answer; 0 means 200
	want   any // the expected result; nil for a method that returns only an error
}

func TestMethods(t *testing.T) {
	ctx := context.Background()
	var last seen
	var status int
	var answer string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		last = seen{r.Method, r.URL.Path, r.URL.RawQuery, string(b), r.Header.Get("Content-Type"), r.Header.Get("Idempotency-Key")}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}, Config{})

	name := "n"
	cases := []methodCase{
		{"ListAPIKeys", func(c *Client) (any, error) { return c.ListAPIKeys(ctx) },
			"GET", "/api/v1/api-keys", "", "", `{"items":[{"id":"k"}]}`, 0, ApiKeyList{Items: []ApiKey{{Id: "k"}}}},
		{"RevokeAPIKey", func(c *Client) (any, error) { return nil, c.RevokeAPIKey(ctx, "k 1") },
			"DELETE", "/api/v1/api-keys/k 1", "", "", "", 204, nil},
		{"ListClients", func(c *Client) (any, error) { return c.ListClients(ctx) },
			"GET", "/api/v1/clients", "", "", `{"items":[{"id":"c"}]}`, 0, ClientList{Items: []OrgClient{{Id: "c"}}}},
		{"CreateClient", func(c *Client) (any, error) { return c.CreateClient(ctx, CreateClientRequest{Name: "n"}) },
			"POST", "/api/v1/clients", "", `{"name":"n"}`, `{"id":"c"}`, 201, OrgClient{Id: "c"}},
		{"GetClient", func(c *Client) (any, error) { return c.GetClient(ctx, "c") },
			"GET", "/api/v1/clients/c", "", "", `{"id":"c"}`, 0, OrgClient{Id: "c"}},
		{"UpdateClient", func(c *Client) (any, error) { return c.UpdateClient(ctx, "c", UpdateClientRequest{Name: &name}) },
			"PATCH", "/api/v1/clients/c", "", `{"name":"n"}`, `{"id":"c"}`, 0, OrgClient{Id: "c"}},
		{"DeleteClient", func(c *Client) (any, error) { return nil, c.DeleteClient(ctx, "c") },
			"DELETE", "/api/v1/clients/c", "", "", "", 204, nil},
		{"ListInstances", func(c *Client) (any, error) { return c.ListInstances(ctx) },
			"GET", "/api/v1/instances", "", "", `{"items":[{"id":"i"}]}`, 0, InstanceList{Items: []Instance{{Id: "i"}}}},
		{"CreateInstance", func(c *Client) (any, error) {
			return c.CreateInstance(ctx, CreateInstanceRequest{DisplayName: "n", Provider: "WHATSAPP_WEB"})
		},
			"POST", "/api/v1/instances", "", `{"display_name":"n","provider":"WHATSAPP_WEB"}`, `{"instance":{"id":"i"}}`, 201, CreateInstanceResult{Instance: Instance{Id: "i"}}},
		{"GetInstance", func(c *Client) (any, error) { return c.GetInstance(ctx, "i") },
			"GET", "/api/v1/instances/i", "", "", `{"id":"i"}`, 0, Instance{Id: "i"}},
		{"UpdateInstance", func(c *Client) (any, error) {
			return c.UpdateInstance(ctx, "i", UpdateInstanceRequest{DisplayName: &name})
		},
			"PATCH", "/api/v1/instances/i", "", `{"display_name":"n"}`, `{"id":"i"}`, 0, Instance{Id: "i"}},
		{"PairInstance", func(c *Client) (any, error) { return c.PairInstance(ctx, "i", PairInstanceRequest{}) },
			"POST", "/api/v1/instances/i/pair", "", `{}`, `{"pairing":{"message":"x"}}`, 0, PairResult{Pairing: Pairing{Message: "x"}}},
		{"CancelPairing", func(c *Client) (any, error) { return nil, c.CancelPairing(ctx, "i") },
			"DELETE", "/api/v1/instances/i/pairing", "", "", "", 204, nil},
		{"ListBulkJobs", func(c *Client) (any, error) { return c.ListBulkJobs(ctx, "i") },
			"GET", "/api/v1/instances/i/bulk-jobs", "", "", `{"items":[{"id":"b"}]}`, 0, BulkJobList{Items: []BulkJob{{Id: "b"}}}},
		{"GetBulkJob", func(c *Client) (any, error) { return c.GetBulkJob(ctx, "b") },
			"GET", "/api/v1/bulk-jobs/b", "", "", `{"id":"b"}`, 0, BulkJob{Id: "b"}},
		{"CancelBulkJob", func(c *Client) (any, error) { return c.CancelBulkJob(ctx, "b") },
			"POST", "/api/v1/bulk-jobs/b/cancel", "", "", `{"id":"b"}`, 0, BulkJob{Id: "b"}},
		{"GetMe", func(c *Client) (any, error) { return c.GetMe(ctx) },
			"GET", "/api/v1/me", "", "", `{"user":{"id":"u"}}`, 0, Me{User: User{Id: "u"}}},
		{"GetOrganization", func(c *Client) (any, error) { return c.GetOrganization(ctx) },
			"GET", "/api/v1/organization", "", "", `{"id":"o"}`, 0, Organization{Id: "o"}},
		{"UpdateOrganization", func(c *Client) (any, error) { return c.UpdateOrganization(ctx, UpdateOrganizationRequest{Name: &name}) },
			"PATCH", "/api/v1/organization", "", `{"name":"n"}`, `{"id":"o"}`, 0, Organization{Id: "o"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, answer = tc.status, tc.answer
			if status == 0 {
				status = 200
			}
			got, err := tc.call(c)
			if err != nil {
				t.Fatal(err)
			}
			wantBody := tc.body
			wantType := ""
			if tc.body != "" {
				wantType = "application/json"
			}
			wantPath, _ := url.PathUnescape(last.path)
			if last.method != tc.method || wantPath != tc.path || last.query != tc.query ||
				last.body != wantBody || last.contentType != wantType || last.idemKey != "" {
				t.Errorf("request = %+v, want %s %s body %q", last, tc.method, tc.path, wantBody)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("result = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func TestRemoveInstanceStatus(t *testing.T) {
	for _, want := range []int{202, 204} {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/instances/i" {
				t.Errorf("request = %s %s", r.Method, r.URL.Path)
			}
			w.WriteHeader(want)
		}, Config{})
		got, err := c.RemoveInstance(context.Background(), "i")
		if err != nil || got != want {
			t.Errorf("RemoveInstance = %d, %v; want %d", got, err, want)
		}
	}
}

func TestSendKeys(t *testing.T) {
	ctx := context.Background()
	sends := map[string]struct {
		path string
		body string
		call func(c *Client, o SendOptions) (string, error)
	}{
		"SendMessage": {"/api/v1/instances/i/messages", `{"text":"hi","to":"1"}`, func(c *Client, o SendOptions) (string, error) {
			s, err := c.SendMessage(ctx, "i", SendMessageRequest{To: "1", Text: "hi"}, o)
			if err == nil && s.Data.MessageId != "m" {
				t.Errorf("result = %+v", s.Data)
			}
			return s.IdempotencyKey, err
		}},
		"CreateBulkJob": {"/api/v1/instances/i/bulk-jobs", `{"recipients":["1"],"text":"hi"}`, func(c *Client, o SendOptions) (string, error) {
			s, err := c.CreateBulkJob(ctx, "i", CreateBulkJobRequest{Recipients: &[]string{"1"}, Text: ptr("hi")}, o)
			if err == nil && s.Data.Id != "m" {
				t.Errorf("result = %+v", s.Data)
			}
			return s.IdempotencyKey, err
		}},
	}
	for name, s := range sends {
		for _, given := range []string{"", "my-key"} {
			t.Run(name+"/key="+given, func(t *testing.T) {
				var sent, body string
				fail := false
				c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
					sent = r.Header.Get("Idempotency-Key")
					b, _ := io.ReadAll(r.Body)
					body = string(b)
					if r.Method != http.MethodPost || r.URL.Path != s.path {
						t.Errorf("request = %s %s", r.Method, r.URL.Path)
					}
					w.Header().Set("Content-Type", "application/json")
					if fail {
						w.WriteHeader(http.StatusTooManyRequests)
						_, _ = io.WriteString(w, `{"code":"instance_queue_full","title":"Too fast"}`)
						return
					}
					_, _ = io.WriteString(w, `{"id":"m","message_id":"m"}`)
				}, Config{})
				got, err := s.call(c, SendOptions{IdempotencyKey: given})
				if err != nil {
					t.Fatal(err)
				}
				if sent == "" || got != sent || (given != "" && got != given) {
					t.Errorf("header %q, result key %q, given %q", sent, got, given)
				}
				if body != s.body {
					t.Errorf("body = %s, want %s", body, s.body)
				}
				fail = true
				got, err = s.call(c, SendOptions{IdempotencyKey: given})
				e := asCalyxError(t, err, "instance_queue_full")
				if e.IdempotencyKey == "" || e.IdempotencyKey != sent || got != "" && got != sent {
					t.Errorf("error key %q, header %q", e.IdempotencyKey, sent)
				}
			})
		}
	}
}

func TestPathIsEscapedOnTheWire(t *testing.T) {
	var wire string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		wire = r.URL.EscapedPath()
		_, _ = io.WriteString(w, `{}`)
	}, Config{})
	if _, err := c.GetInstance(context.Background(), "a/b"); err != nil {
		t.Fatal(err)
	}
	if want := "/api/v1/instances/a%2Fb"; wire != want {
		t.Errorf("wire path = %q, want %q", wire, want)
	}
}

func TestSendErrorsCarryTheKey(t *testing.T) {
	t.Run("SendMessage canceled", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {}, Config{})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		s, err := c.SendMessage(ctx, "i", SendMessageRequest{To: "1", Text: "hi"}, SendOptions{})
		e := asCalyxError(t, err, calyx.CodeAborted)
		if e.IdempotencyKey == "" || s.IdempotencyKey != e.IdempotencyKey {
			t.Errorf("error key %q, result key %q", e.IdempotencyKey, s.IdempotencyKey)
		}
	})
	t.Run("CreateBulkJob network failure", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		c, err := New(Config{BaseURL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		srv.Close()
		s, err := c.CreateBulkJob(context.Background(), "i", CreateBulkJobRequest{}, SendOptions{IdempotencyKey: "mine"})
		e := asCalyxError(t, err, calyx.CodeNetworkError)
		if e.IdempotencyKey != "mine" || s.IdempotencyKey != "mine" {
			t.Errorf("error key %q, result key %q", e.IdempotencyKey, s.IdempotencyKey)
		}
	})
}
