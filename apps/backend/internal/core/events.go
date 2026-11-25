package core

import (
	"context"
	"errors"
	"io"
	"time"

	"google.golang.org/grpc/metadata"

	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// ErrNoHeaders means that the core did not confirm the subscription in time.
var ErrNoHeaders = errors.New("the core sent no headers")

// SubscribeConnections listens to "connection.update" events. No message content reaches the
// backend, because the request names only that event type.
//
// It waits at most headerWait for the response headers. The core sends them directly after it
// registers the subscription. With none, it returns ErrNoHeaders. Then it calls onReady once with the
// core version from the header "calyx-version" (empty when the core sends none), and onEvent with
// the instance name of each event. It returns when the stream ends: nil at a clean end, the error
// of the stream otherwise, ctx.Err() when ctx ends.
func (c *Client) SubscribeConnections(ctx context.Context, headerWait time.Duration, onReady func(coreVersion string), onEvent func(coreName string)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // ends the stream and frees the goroutine that waits for the headers
	stream, err := c.events.Subscribe(ctx, &corev1.SubscribeRequest{EventTypes: []string{"connection.update"}})
	if err != nil {
		return err
	}
	type headerResult struct {
		version string
		err     error
	}
	headers := make(chan headerResult, 1)
	go func() {
		md, err := stream.Header()
		if err == nil && md == nil {
			// A refused token ends the stream and gives nil headers. Recv has the real status.
			if _, err = stream.Recv(); err == nil {
				err = ErrNoHeaders
			}
		}
		headers <- headerResult{coreVersion(md), err}
	}()
	var version string
	timer := time.NewTimer(headerWait)
	defer timer.Stop()
	select {
	case h := <-headers:
		if h.err != nil {
			return h.err
		}
		version = h.version
	case <-timer.C:
		return ErrNoHeaders
	case <-ctx.Done():
		return ctx.Err()
	}
	onReady(version)
	for {
		ev, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		onEvent(ev.GetInstanceName())
	}
}

// coreVersion reads the header "calyx-version". An older core sends none, so it gives "".
func coreVersion(md metadata.MD) string {
	if values := md.Get("calyx-version"); len(values) > 0 {
		return values[0]
	}
	return ""
}
