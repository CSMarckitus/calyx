// The package is core, not core_test: the tests call the unexported per-call helper.
package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/keepalive"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// asCalyxError checks that err is a *calyx.Error with the code, that its retry advice comes
// from calyx.AdviceFor, and that its text does not hold the core token.
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
	if strings.Contains(e.Error(), testToken) {
		t.Errorf("the error text holds the core token: %q", e.Error())
	}
	return e
}

// listInstances calls ListInstances through the per-call helper.
func listInstances(ctx context.Context, c *Client) (*corev1.ListInstancesResponse, error) {
	return unary(ctx, c, otherLimit, "", &corev1.ListInstancesRequest{}, c.instances.ListInstances)
}

func TestNewRefusesAConfigWithoutSecurity(t *testing.T) {
	c, err := New(Config{Address: "127.0.0.1:1", Token: testToken})
	e := asCalyxError(t, err, calyx.CodeInvalidConfig)
	if e.Source != calyx.SourceClient {
		t.Errorf("source = %q, want client", e.Source)
	}
	if c != nil {
		t.Error("New gave a client with the error")
	}
}

func TestNewRefusesAnEmptyAddress(t *testing.T) {
	_, err := New(Config{Token: testToken, Security: Insecure()})
	if e := asCalyxError(t, err, calyx.CodeInvalidConfig); e.Source != calyx.SourceClient {
		t.Errorf("source = %q, want client", e.Source)
	}
}

func TestEveryCallSendsTheToken(t *testing.T) {
	f := newFakeCore(t)
	clients := map[string]*Client{
		"New":         f.newClient(t),
		"NewFromConn": NewFromConn(f.dial(t), testToken),
	}
	for name, c := range clients {
		t.Run(name, func(t *testing.T) {
			before := len(f.recorded())
			if _, err := listInstances(context.Background(), c); err != nil {
				t.Fatal(err)
			}
			calls := f.recorded()
			if len(calls) != before+1 {
				t.Fatalf("the fake core saw %d new calls, want 1", len(calls)-before)
			}
			got := calls[len(calls)-1].md.Get("authorization")
			if len(got) != 1 || got[0] != "Bearer "+testToken {
				t.Errorf("authorization = %q, want one value \"Bearer <token>\"", got)
			}
		})
	}
}

func TestNewSetsTheUserAgent(t *testing.T) {
	f := newFakeCore(t)
	c := f.newClient(t)
	if _, err := listInstances(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	ua := f.recorded()[0].md.Get("user-agent")
	if len(ua) != 1 || !strings.HasPrefix(ua[0], "calyx-go-sdk/"+Version) {
		t.Errorf("user-agent = %q, want the prefix calyx-go-sdk/%s", ua, Version)
	}
}

func TestKeepaliveValues(t *testing.T) {
	if keepaliveParams.Time != 2*time.Minute {
		t.Errorf("ping interval = %v, want 2m", keepaliveParams.Time)
	}
	if keepaliveParams.Timeout != 20*time.Second {
		t.Errorf("ping timeout = %v, want 20s", keepaliveParams.Timeout)
	}
	// The core counts a ping with no call as a fault.
	if keepaliveParams.PermitWithoutStream {
		t.Error("the client pings with no call, want pings only while a call runs")
	}
	// The core closes a connection that pings more often than once a minute (keepalivePolicy
	// in apps/core/internal/grpcserver).
	if coreMinTime := time.Minute; keepaliveParams.Time <= coreMinTime {
		t.Errorf("ping interval = %v, want more than %v", keepaliveParams.Time, coreMinTime)
	}
}

// grpc-go raises a shorter interval to 10 s, so a test with real pings would be slow. This test
// reads the parameters that the connection has.
func TestNewGivesTheKeepaliveToTheConnection(t *testing.T) {
	c := newFakeCore(t).newClient(t)
	params := reflect.ValueOf(c.conn).Elem()
	for _, name := range []string{"dopts", "copts", "KeepaliveParams"} {
		params = params.FieldByName(name)
		if !params.IsValid() {
			t.Fatalf("grpc-go has no field %s now: find the new place of the keepalive parameters", name)
		}
	}
	got := keepalive.ClientParameters{
		Time:                time.Duration(params.FieldByName("Time").Int()),
		Timeout:             time.Duration(params.FieldByName("Timeout").Int()),
		PermitWithoutStream: params.FieldByName("PermitWithoutStream").Bool(),
	}
	if got != keepaliveParams {
		t.Errorf("keepalive of the connection = %+v, want %+v", got, keepaliveParams)
	}
}

func TestCoreErrorCases(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/core-errors.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			Name         string `json:"name"`
			GRPCCode     int    `json:"grpcCode"`
			StatusBase64 string `json:"statusBase64"`
			Expected     struct {
				Source   string            `json:"source"`
				Code     string            `json:"code"`
				Status   int               `json:"status"`
				Retry    string            `json:"retry"`
				Metadata map[string]string `json:"metadata"`

				RetryAfterSeconds int `json:"retryAfterSeconds"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) == 0 {
		t.Fatal("core-errors.json has no cases")
	}

	f := newFakeCore(t)
	c := f.newClient(t)
	for _, tc := range file.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			data, err := base64.StdEncoding.DecodeString(tc.StatusBase64)
			if err != nil {
				t.Fatal(err)
			}
			var st status.Status
			if err := proto.Unmarshal(data, &st); err != nil {
				t.Fatal(err)
			}
			if int(st.Code) != tc.GRPCCode {
				t.Fatalf("the status has the code %d, the case says %d", st.Code, tc.GRPCCode)
			}
			f.setUnary(func(context.Context, string, proto.Message) (proto.Message, error) {
				return nil, grpcstatus.FromProto(&st).Err()
			})

			_, err = listInstances(context.Background(), c)
			e := asCalyxError(t, err, calyx.Code(tc.Expected.Code))
			if string(e.Source) != tc.Expected.Source {
				t.Errorf("source = %q, want %q", e.Source, tc.Expected.Source)
			}
			if e.Status != tc.Expected.Status {
				t.Errorf("status = %d, want %d", e.Status, tc.Expected.Status)
			}
			if string(e.Retry) != tc.Expected.Retry {
				t.Errorf("retry = %q, want %q", e.Retry, tc.Expected.Retry)
			}
			if e.Detail != st.Message {
				t.Errorf("detail = %q, want %q", e.Detail, st.Message)
			}
			if e.RetryAfter != time.Duration(tc.Expected.RetryAfterSeconds)*time.Second {
				t.Errorf("retry after = %v, want %d s", e.RetryAfter, tc.Expected.RetryAfterSeconds)
			}
			// An absent metadata in the file means an empty map.
			if !maps.Equal(e.Metadata, tc.Expected.Metadata) {
				t.Errorf("metadata = %v, want %v", e.Metadata, tc.Expected.Metadata)
			}
		})
	}
}

func TestCanonicalNames(t *testing.T) {
	want := map[codes.Code]calyx.Code{
		codes.Canceled:           calyx.CodeGRPCCancelled,
		codes.Unknown:            calyx.CodeGRPCUnknown,
		codes.InvalidArgument:    calyx.CodeGRPCInvalidArgument,
		codes.DeadlineExceeded:   calyx.CodeGRPCDeadlineExceeded,
		codes.NotFound:           calyx.CodeGRPCNotFound,
		codes.AlreadyExists:      calyx.CodeGRPCAlreadyExists,
		codes.PermissionDenied:   calyx.CodeGRPCPermissionDenied,
		codes.ResourceExhausted:  calyx.CodeGRPCResourceExhausted,
		codes.FailedPrecondition: calyx.CodeGRPCFailedPrecondition,
		codes.Aborted:            calyx.CodeGRPCAborted,
		codes.OutOfRange:         calyx.CodeGRPCOutOfRange,
		codes.Unimplemented:      calyx.CodeGRPCUnimplemented,
		codes.Internal:           calyx.CodeGRPCInternal,
		codes.Unavailable:        calyx.CodeGRPCUnavailable,
		codes.DataLoss:           calyx.CodeGRPCDataLoss,
		codes.Unauthenticated:    calyx.CodeGRPCUnauthenticated,
	}
	for n := codes.Code(1); n <= 16; n++ {
		code, ok := want[n]
		if !ok {
			t.Fatalf("the test has no name for the code %d", n)
		}
		e := asCalyxError(t, convertError(context.Background(), grpcstatus.Error(n, "text"), "key-1"), code)
		if e.Source != calyx.SourceCore || e.Status != int(n) || e.IdempotencyKey != "key-1" {
			t.Errorf("code %d: source %q, status %d, key %q", n, e.Source, e.Status, e.IdempotencyKey)
		}
	}
}

func TestCloseKeepsTheConnectionOfTheProgram(t *testing.T) {
	f := newFakeCore(t)
	conn := f.dial(t)
	c := NewFromConn(conn, testToken)
	if _, err := listInstances(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if s := conn.GetState(); s == connectivity.Shutdown {
		t.Fatal("Close closed the connection of the program")
	}
	other := NewFromConn(conn, testToken)
	if _, err := listInstances(context.Background(), other); err != nil {
		t.Fatalf("a call on the connection of the program failed after Close: %v", err)
	}
}

func TestCallAfterCloseIsAborted(t *testing.T) {
	f := newFakeCore(t)
	clients := map[string]*Client{
		"New":         f.newClient(t),
		"NewFromConn": NewFromConn(f.dial(t), testToken),
	}
	for name, c := range clients {
		t.Run(name, func(t *testing.T) {
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			_, err := listInstances(context.Background(), c)
			e := asCalyxError(t, err, calyx.CodeAborted)
			if e.Source != calyx.SourceClient {
				t.Errorf("source = %q, want client", e.Source)
			}
			if err := c.Close(); err != nil {
				t.Errorf("a second Close gave %v", err)
			}
		})
	}
}

func TestUnaryAddsTheDefaultLimitOnlyWithoutADeadline(t *testing.T) {
	f := newFakeCore(t)
	c := f.newClient(t)
	start := time.Now()
	if _, err := listInstances(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	if _, err := listInstances(ctx, c); err != nil {
		t.Fatal(err)
	}
	calls := f.recorded()
	// The server rebuilds the deadline from the grpc-timeout header, so it can move a little.
	if left := calls[0].deadline.Sub(start); left < otherLimit-10*time.Second || left > otherLimit+time.Second {
		t.Errorf("time left without a deadline = %v, want about %v", left, otherLimit)
	}
	if left := calls[1].deadline.Sub(start); left <= otherLimit {
		t.Errorf("time left with a 1-hour deadline = %v, want more than %v", left, otherLimit)
	}
}

func TestANegativeRetryDelayGivesNoRetryAfter(t *testing.T) {
	st, err := grpcstatus.New(codes.ResourceExhausted, "The queue is full.").WithDetails(
		&errdetails.RetryInfo{RetryDelay: durationpb.New(-5 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	e := convertError(context.Background(), st.Err(), "")
	if e.RetryAfter != 0 {
		t.Errorf("retry after = %v, want 0", e.RetryAfter)
	}
}
