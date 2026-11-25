package core

import (
	"context"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// The default limits of the unary calls. The SDK adds one only when ctx has no deadline.
const (
	// sendLimit is longer than 5 minutes (CONTRACT.md, time rule 1). The slowest normal send
	// waits about 4 minutes in the core queue: 20 waiting sends x (10 s typing + 2 s gap).
	// If the SDK cancels a send, the core removes the message from its queue.
	sendLimit  = 6 * time.Minute
	otherLimit = 2 * time.Minute
)

// keepaliveParams is the keepalive ping of New. It finds a core host that disappears. The core
// refuses pings more often than once a minute, and pings while no call runs.
var keepaliveParams = keepalive.ClientParameters{Time: 2 * time.Minute, Timeout: 20 * time.Second}

// Client calls the gRPC API of the core. It is safe for concurrent use.
type Client struct {
	conn      *grpc.ClientConn // nil with NewFromConn: Close does not close a connection of the program
	token     string
	closed    atomic.Bool
	instances corev1.InstanceServiceClient
	messages  corev1.MessageServiceClient
	events    corev1.EventServiceClient
}

// New makes a Client with its own connection. The first call connects. A Config without
// Address or without Security gives a *calyx.Error with the code invalid_config.
func New(cfg Config) (*Client, error) {
	if cfg.Address == "" {
		return nil, clientError(calyx.CodeInvalidConfig, "The address of the core is empty.", "", nil)
	}
	if cfg.Security.creds == nil {
		return nil, clientError(calyx.CodeInvalidConfig, "Security is required: use core.Insecure() or core.TLS(cfg).", "", nil)
	}
	conn, err := grpc.NewClient(cfg.Address,
		grpc.WithTransportCredentials(cfg.Security.creds),
		grpc.WithKeepaliveParams(keepaliveParams),
		grpc.WithUserAgent("calyx-go-sdk/"+Version),
	)
	if err != nil {
		return nil, clientError(calyx.CodeInvalidConfig, "The address of the core is not valid.", "", err)
	}
	c := NewFromConn(conn, cfg.Token)
	c.conn = conn
	return c, nil
}

// NewFromConn makes a Client on a connection that the program made, for example with its own
// TLS, interceptors or an in-memory connection. Close does not close that connection. The SDK adds
// no keepalive ping to it: use grpc.WithKeepaliveParams to find a core host that disappears.
func NewFromConn(conn grpc.ClientConnInterface, token string) *Client {
	return &Client{
		token:     token,
		instances: corev1.NewInstanceServiceClient(conn),
		messages:  corev1.NewMessageServiceClient(conn),
		events:    corev1.NewEventServiceClient(conn),
	}
}

// Close stops the Client. It closes the connection that New made. A call after Close gives
// the code aborted. A second Close does nothing.
func (c *Client) Close() error {
	if !c.closed.CompareAndSwap(false, true) || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// outgoing adds the token to ctx (step 1 of the per-call helper). The streams use it too.
// After Close it gives the error aborted.
func (c *Client) outgoing(ctx context.Context, idemKey string) (context.Context, *calyx.Error) {
	if c.closed.Load() {
		return ctx, closedError(idemKey)
	}
	// Metadata, not call credentials: call credentials refuse a connection without TLS.
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+c.token), nil
}

// unary is the per-call helper of every unary call. It adds the token, applies limit when ctx
// has no deadline, makes the call and converts an error to a *calyx.Error that carries idemKey.
func unary[Req, Res any](ctx context.Context, c *Client, limit time.Duration, idemKey string, req Req,
	rpc func(context.Context, Req, ...grpc.CallOption) (Res, error),
) (Res, error) {
	var zero Res
	ctx, cerr := c.outgoing(ctx, idemKey)
	if cerr != nil {
		return zero, cerr
	}
	// A default would shorten a deadline of the program, so it applies only without one.
	if _, has := ctx.Deadline(); !has {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, limit)
		defer cancel()
	}
	res, err := rpc(ctx, req)
	if err != nil {
		// Close during the call makes grpc-go give CANCELLED; the table says aborted.
		if c.closed.Load() {
			return zero, closedError(idemKey)
		}
		return zero, convertError(ctx, err, idemKey)
	}
	return res, nil
}
