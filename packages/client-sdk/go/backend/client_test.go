// The package is backend, not backend_test: the tests call the unexported request helper.
package backend

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
)

const testAPIKey = "calyx_secret_api_key_123"

// newTestClient starts a test server with the handler and returns a client for it.
func newTestClient(t *testing.T, h http.HandlerFunc, cfg Config) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg.BaseURL = srv.URL
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// asCalyxError checks that err is a *calyx.Error with the code, and that its retry advice
// comes from calyx.AdviceFor.
func asCalyxError(t *testing.T, err error, code calyx.Code) *calyx.Error {
	t.Helper()
	var e *calyx.Error
	if !errors.As(err, &e) {
		t.Fatalf("error %v (%T) is not a *calyx.Error", err, err)
	}
	if e.Code != code {
		t.Fatalf("code = %q, want %q (error %v)", e.Code, code, e)
	}
	if e.Retry != calyx.AdviceFor(e.Source, e.Code) {
		t.Errorf("retry = %q, want the advice %q", e.Retry, calyx.AdviceFor(e.Source, e.Code))
	}
	return e
}

// asCreateCallError is asCalyxError for a call with a create-call rule: it checks the code and
// the retry advice that the rule gives.
func asCreateCallError(t *testing.T, err error, code calyx.Code, advice calyx.Retry) *calyx.Error {
	t.Helper()
	var e *calyx.Error
	if !errors.As(err, &e) {
		t.Fatalf("error %v (%T) is not a *calyx.Error", err, err)
	}
	if e.Code != code {
		t.Fatalf("code = %q, want %q (error %v)", e.Code, code, e)
	}
	if e.Retry != advice {
		t.Errorf("retry = %q, want %q", e.Retry, advice)
	}
	return e
}

func TestNewRefusesAnEmptyBaseURL(t *testing.T) {
	_, err := New(Config{})
	e := asCalyxError(t, err, calyx.CodeInvalidConfig)
	if e.Source != calyx.SourceClient {
		t.Errorf("source = %q, want client", e.Source)
	}
}

func TestNewRemovesTheFinalSlash(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c, err := New(Config{BaseURL: srv.URL + "/"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.do(context.Background(), request{op: "getMe"}); err != nil {
		t.Fatal(err)
	}
	if path != "/api/v1/me" {
		t.Errorf("path = %q, want /api/v1/me", path)
	}
}

// countingTransport counts the requests, so a test can see that the copy keeps the transport.
type countingTransport struct {
	mu sync.Mutex
	n  int
}

func (ct *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	ct.mu.Lock()
	ct.n++
	ct.mu.Unlock()
	return http.DefaultTransport.RoundTrip(r)
}

func TestNewCopiesTheHTTPClientWithoutTimeout(t *testing.T) {
	tr := &countingTransport{}
	program := &http.Client{Timeout: 50 * time.Millisecond, Transport: tr}
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}, Config{HTTPClient: program})
	if _, err := c.do(context.Background(), request{op: "getMe"}); err != nil {
		t.Fatalf("the client Timeout stopped the call: %v", err)
	}
	if program.Timeout != 50*time.Millisecond {
		t.Errorf("New changed the client of the program: Timeout = %v", program.Timeout)
	}
	if tr.n != 1 {
		t.Errorf("the transport of the program saw %d requests, want 1", tr.n)
	}
}

func TestAuthorizationHeader(t *testing.T) {
	for _, apiKey := range []string{"", testAPIKey} {
		var got string
		var seen bool
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			got, seen = r.Header.Get("Authorization"), len(r.Header.Values("Authorization")) > 0
			w.WriteHeader(http.StatusNoContent)
		}, Config{APIKey: apiKey})
		if _, err := c.do(context.Background(), request{op: "getMe"}); err != nil {
			t.Fatal(err)
		}
		if apiKey == "" && seen {
			t.Errorf("Authorization sent without an API key: %q", got)
		}
		if apiKey != "" && got != "Bearer "+apiKey {
			t.Errorf("Authorization = %q, want Bearer <key>", got)
		}
	}
}

func TestRequestBuildsTheCall(t *testing.T) {
	var method, path, contentType, idemKey string
	var body map[string]string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.EscapedPath()
		contentType, idemKey = r.Header.Get("Content-Type"), r.Header.Get("Idempotency-Key")
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"done"}`))
	}, Config{})
	var out struct{ Name string }
	status, err := c.do(context.Background(), request{
		op: "sendMessage", id: "a b/c", body: map[string]string{"to": "x"}, result: &out, key: "k1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK || out.Name != "done" {
		t.Errorf("status %d, result %+v", status, out)
	}
	if method != http.MethodPost || path != "/api/v1/instances/a%20b%2Fc/messages" {
		t.Errorf("request %s %s", method, path)
	}
	if contentType != "application/json" || body["to"] != "x" {
		t.Errorf("content type %q, body %v", contentType, body)
	}
	if idemKey != "k1" {
		t.Errorf("Idempotency-Key = %q, want k1", idemKey)
	}
}

func TestRequestWithoutBodyOrKey(t *testing.T) {
	var contentType string
	var hasKey bool
	var length int64
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		contentType, hasKey, length = r.Header.Get("Content-Type"), len(r.Header.Values("Idempotency-Key")) > 0, r.ContentLength
		w.WriteHeader(http.StatusAccepted)
	}, Config{})
	status, err := c.do(context.Background(), request{op: "removeInstance", id: "i1"})
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusAccepted {
		t.Errorf("status = %d, want 202", status)
	}
	if contentType != "" || hasKey || length != 0 {
		t.Errorf("content type %q, key sent %v, length %d", contentType, hasKey, length)
	}
}

func TestRequestRejectsA2xxBodyThatIsNotJSON(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html></html>"))
	}, Config{})
	var out struct{}
	_, err := c.do(context.Background(), request{op: "getMe", result: &out, key: "k2"})
	e := asCalyxError(t, err, calyx.CodeInvalidResponse)
	if e.Source != calyx.SourceClient || e.Status != 0 || e.IdempotencyKey != "k2" {
		t.Errorf("unexpected %+v", e)
	}
}

func TestBackendErrorCases(t *testing.T) {
	data, err := os.ReadFile("../../testdata/backend-errors.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			Name     string            `json:"name"`
			Status   int               `json:"status"`
			Headers  map[string]string `json:"headers"`
			Body     string            `json:"body"`
			Expected struct {
				Source            string `json:"source"`
				Code              string `json:"code"`
				Status            int    `json:"status"`
				Detail            string `json:"detail"`
				Retry             string `json:"retry"`
				RequestID         string `json:"requestId"`
				RetryAfterSeconds int    `json:"retryAfterSeconds"`
				Errors            []struct {
					Location string `json:"location"`
					Message  string `json:"message"`
				} `json:"errors"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) != 8 {
		t.Fatalf("%d cases, want 8", len(file.Cases))
	}
	for _, tc := range file.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tc.Headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tc.Status)
				_, _ = w.Write([]byte(tc.Body))
			}, Config{APIKey: testAPIKey})
			_, err := c.do(context.Background(), request{op: "getMe"})
			x := tc.Expected
			e := asCalyxError(t, err, calyx.Code(x.Code))
			if string(e.Source) != x.Source || e.Status != x.Status || e.Detail != x.Detail ||
				string(e.Retry) != x.Retry || e.RequestID != x.RequestID ||
				e.RetryAfter != time.Duration(x.RetryAfterSeconds)*time.Second {
				t.Errorf("got %+v, want %+v", e, x)
			}
			if len(e.Errors) != len(x.Errors) {
				t.Fatalf("errors = %+v, want %+v", e.Errors, x.Errors)
			}
			for i, fe := range x.Errors {
				if e.Errors[i] != (calyx.FieldError{Location: fe.Location, Message: fe.Message}) {
					t.Errorf("errors[%d] = %+v, want %+v", i, e.Errors[i], fe)
				}
			}
			if strings.Contains(e.Error(), testAPIKey) {
				t.Errorf("the error text has the API key: %q", e.Error())
			}
		})
	}
}

func TestNetworkFailure(t *testing.T) {
	t.Run("no connection", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close()
		c, err := New(Config{BaseURL: url, APIKey: testAPIKey})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.do(context.Background(), request{op: "getMe", key: "k3"})
		e := asCalyxError(t, err, calyx.CodeNetworkError)
		if e.Source != calyx.SourceClient || e.IdempotencyKey != "k3" {
			t.Errorf("unexpected %+v", e)
		}
		if errors.Unwrap(e) == nil {
			t.Error("a network error has no cause")
		}
		if strings.Contains(e.Error(), testAPIKey) {
			t.Errorf("the error text has the API key: %q", e.Error())
		}
	})
	t.Run("connection that stops", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":`))
			conn, _, err := http.NewResponseController(w).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}, Config{APIKey: testAPIKey})
		var out struct{}
		_, err := c.do(context.Background(), request{op: "getMe", result: &out})
		_ = asCalyxError(t, err, calyx.CodeNetworkError)
	})
}

func TestCanceledContextGivesAborted(t *testing.T) {
	arrived := make(chan struct{})
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		<-r.Context().Done()
	}, Config{APIKey: testAPIKey})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-arrived
		cancel()
	}()
	_, err := c.do(ctx, request{op: "getMe", key: "k4"})
	e := asCalyxError(t, err, calyx.CodeAborted)
	if !errors.Is(err, context.Canceled) {
		t.Error("errors.Is does not find context.Canceled")
	}
	if e.IdempotencyKey != "k4" || strings.Contains(e.Error(), testAPIKey) {
		t.Errorf("unexpected %q", e.Error())
	}
}

func TestPassedDeadlineGivesTimeout(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}, Config{APIKey: testAPIKey})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.do(ctx, request{op: "getMe"})
	e := asCalyxError(t, err, calyx.CodeTimeout)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Error("errors.Is does not find context.DeadlineExceeded")
	}
	if strings.Contains(e.Error(), testAPIKey) {
		t.Errorf("the error text has the API key: %q", e.Error())
	}
}

// deadlineTransport records the deadline of the request context. A server handler cannot see
// the deadline of the client, so the test reads it on the client side, just before the send.
type deadlineTransport struct {
	deadline time.Time
	ok       bool
}

func (dt *deadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	dt.deadline, dt.ok = r.Context().Deadline()
	return http.DefaultTransport.RoundTrip(r)
}

func TestDefaultLimitOnlyWithoutDeadline(t *testing.T) {
	const tolerance = 5 * time.Second
	h := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }

	t.Run("default of the operation", func(t *testing.T) {
		for op, want := range map[string]time.Duration{
			"getMe": 2 * time.Minute, "sendMessage": 6 * time.Minute, "createBulkJob": 150 * time.Second,
		} {
			dt := &deadlineTransport{}
			c := newTestClient(t, h, Config{HTTPClient: &http.Client{Transport: dt}})
			start := time.Now()
			if _, err := c.do(context.Background(), request{op: op, id: "i1"}); err != nil {
				t.Fatal(err)
			}
			if !dt.ok {
				t.Fatalf("%s: the request has no deadline", op)
			}
			if left := dt.deadline.Sub(start); left < want-tolerance || left > want+tolerance {
				t.Errorf("%s: time left %v, want %v", op, left, want)
			}
		}
	})

	t.Run("deadline of the program", func(t *testing.T) {
		// A longer deadline than the default stays, and a shorter one stays too.
		for _, limit := range []time.Duration{time.Hour, 10 * time.Second} {
			dt := &deadlineTransport{}
			c := newTestClient(t, h, Config{HTTPClient: &http.Client{Transport: dt}})
			ctx, cancel := context.WithTimeout(context.Background(), limit)
			want, _ := ctx.Deadline()
			if _, err := c.do(ctx, request{op: "getMe"}); err != nil {
				t.Fatal(err)
			}
			cancel()
			if !dt.ok || !dt.deadline.Equal(want) {
				t.Errorf("limit %v: deadline %v (set %v), want %v", limit, dt.deadline, dt.ok, want)
			}
		}
	})
}

func TestSendKey(t *testing.T) {
	if k := sendKey(SendOptions{IdempotencyKey: "mine"}); k != "mine" {
		t.Errorf("sendKey kept %q, want mine", k)
	}
	a, b := sendKey(SendOptions{}), sendKey(SendOptions{})
	if a == "" || a == b {
		t.Errorf("sendKey made %q and %q, want two new keys", a, b)
	}
}

func TestNewRefusesABaseURLWithoutHTTPSchemeOrHost(t *testing.T) {
	for _, u := range []string{"localhost:8080", "ftp://x", "http://"} {
		_, err := New(Config{BaseURL: u})
		_ = asCalyxError(t, err, calyx.CodeInvalidConfig)
	}
}

func TestA204WithAResultIsNotDecoded(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}, Config{})
	var out struct{ Name string }
	status, err := c.do(context.Background(), request{op: "getMe", result: &out})
	if err != nil || status != http.StatusNoContent {
		t.Fatalf("status %d, error %v; want 204 and no error", status, err)
	}
}

func TestABodyThatCannotBeEncodedPanics(t *testing.T) {
	c, err := New(Config{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if r := recover(); r != "backend: cannot encode the body of createClient" {
			t.Errorf("recover() = %v, want the encode panic", r)
		}
	}()
	_, _ = c.do(context.Background(), request{op: "createClient", body: make(chan int)})
}

// setMaxAnswer sets a small answer size limit for one test.
func setMaxAnswer(t *testing.T, n int) {
	t.Helper()
	old := maxAnswerBytes
	maxAnswerBytes = n
	t.Cleanup(func() { maxAnswerBytes = old })
}

func TestRequestSendsTheUserAgent(t *testing.T) {
	var ua string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusNoContent)
	}, Config{})
	if _, err := c.do(context.Background(), request{op: "getMe"}); err != nil {
		t.Fatal(err)
	}
	if want := "calyx-go-sdk/" + calyx.Version; ua != want {
		t.Errorf("User-Agent = %q, want %q", ua, want)
	}
}

func TestAnswerOverTheLimitGivesInvalidResponse(t *testing.T) {
	setMaxAnswer(t, 1024)
	for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"id":"` + strings.Repeat("a", 2048) + `"}`))
		}, Config{})
		var out map[string]any
		_, err := c.do(context.Background(), request{op: "getMe", result: &out})
		_ = asCalyxError(t, err, calyx.CodeInvalidResponse)
	}
}

func TestAnswerOfExactlyTheLimitIsRead(t *testing.T) {
	setMaxAnswer(t, 1024)
	body := `{"id":"` + strings.Repeat("a", 1024-9) + `"}`
	if len(body) != 1024 {
		t.Fatalf("test body is %d bytes", len(body))
	}
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}, Config{})
	var out map[string]any
	if _, err := c.do(context.Background(), request{op: "getMe", result: &out}); err != nil {
		t.Fatal(err)
	}
}
