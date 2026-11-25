package backend

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
)

// createCallsFile is the shared data of the create-call rule.
type createCallsFile struct {
	Operations []string `json:"operations"`
	Codes      []string `json:"codes"`
	Retry      string   `json:"retry"`
}

func TestCreateCallListsMatchTheSharedFile(t *testing.T) {
	data, err := os.ReadFile("../../testdata/create-calls.json")
	if err != nil {
		t.Fatal(err)
	}
	var f createCallsFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	var ops, codes []string
	for op := range createOperations {
		ops = append(ops, op)
	}
	for _, c := range createCodes {
		codes = append(codes, string(c))
	}
	sort.Strings(ops)
	sort.Strings(codes)
	sort.Strings(f.Operations)
	sort.Strings(f.Codes)
	if !slices.Equal(ops, f.Operations) {
		t.Errorf("operations = %v, the file has %v", ops, f.Operations)
	}
	if !slices.Equal(codes, f.Codes) {
		t.Errorf("codes = %v, the file has %v", codes, f.Codes)
	}
	if string(createCallRetry) != f.Retry {
		t.Errorf("advice = %q, the file has %q", createCallRetry, f.Retry)
	}
}

// failures has one way to get each of the four codes. The call that it gets decides the op.
var failures = map[calyx.Code]func(t *testing.T) (*Client, context.Context){
	calyx.CodeNetworkError: func(t *testing.T) (*Client, context.Context) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			conn, _, err := http.NewResponseController(w).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}, Config{APIKey: testAPIKey})
		return c, context.Background()
	},
	calyx.CodeTimeout: func(t *testing.T) (*Client, context.Context) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			// The server sees a closed connection only after it reads the body of the POST.
			_, _ = io.Copy(io.Discard, r.Body)
			<-r.Context().Done()
		}, Config{APIKey: testAPIKey})
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		t.Cleanup(cancel)
		return c, ctx
	},
	calyx.CodeInvalidResponse: func(t *testing.T) (*Client, context.Context) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not json"))
		}, Config{APIKey: testAPIKey})
		return c, context.Background()
	},
	calyx.CodeAborted: func(t *testing.T) (*Client, context.Context) {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}, Config{APIKey: testAPIKey})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return c, ctx
	},
}

func TestCreateCallsAdviseUnknownForClientErrors(t *testing.T) {
	calls := map[string]func(*Client, context.Context) error{
		"createClient": func(c *Client, ctx context.Context) error {
			_, err := c.CreateClient(ctx, CreateClientRequest{Name: "n"})
			return err
		},
		"createInstance": func(c *Client, ctx context.Context) error {
			_, err := c.CreateInstance(ctx, CreateInstanceRequest{DisplayName: "n", Provider: "WHATSAPP_WEB"})
			return err
		},
	}
	for op := range createOperations {
		call, ok := calls[op]
		if !ok {
			t.Fatalf("the test has no call for %s", op)
		}
		for _, code := range createCodes {
			t.Run(op+"/"+string(code), func(t *testing.T) {
				c, ctx := failures[code](t)
				e := asCreateCallError(t, call(c, ctx), code, calyx.RetryUnknown)
				if e.Source != calyx.SourceClient {
					t.Errorf("source = %q, want client", e.Source)
				}
			})
		}
	}
}

func TestUpdateClientKeepsTheAdviceOfTheTable(t *testing.T) {
	c, ctx := failures[calyx.CodeNetworkError](t)
	_, err := c.UpdateClient(ctx, "id", UpdateClientRequest{})
	_ = asCreateCallError(t, err, calyx.CodeNetworkError, calyx.RetryNow)
}

func TestCreateCallKeepsTheAdviceOfOtherErrors(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"not_found","detail":"d"}`))
	}, Config{APIKey: testAPIKey})
	_, err := c.CreateClient(context.Background(), CreateClientRequest{Name: "n"})
	_ = asCalyxError(t, err, "not_found")
}

func TestCreateCallAdvisesUnknownForAnHTMLErrorPage(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>Bad gateway</html>"))
	}, Config{APIKey: testAPIKey})
	_, err := c.CreateClient(context.Background(), CreateClientRequest{Name: "n"})
	_ = asCreateCallError(t, err, calyx.CodeInvalidResponse, calyx.RetryUnknown)
}

func TestCreateCallAdvisesUnknownForAnAnswerThatIsTooLarge(t *testing.T) {
	setMaxAnswer(t, 4)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"long answer"}`))
	}, Config{APIKey: testAPIKey})
	_, err := c.CreateClient(context.Background(), CreateClientRequest{Name: "n"})
	_ = asCreateCallError(t, err, calyx.CodeInvalidResponse, calyx.RetryUnknown)
}
