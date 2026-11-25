package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
)

// PairingItem is one event of the pairing stream. Exactly one of Update, Done and Failed is set.
type PairingItem struct {
	Event  string   // "update", "done" or "failed"
	Update *Pairing // set when Event is "update"
	Done   *Done    // set when Event is "done"
	Failed *Failed  // set when Event is "failed"
}

// idleTimeout is the longest time without data or a ping. The backend pings every 15 seconds.
// It is a variable so that the tests can make it short.
var idleTimeout = 45 * time.Second

// errIdle is the cause of the cancel by the idle timer. It tells the timer from the program.
var errIdle = errors.New("the pairing stream was idle")

// FollowPairing reads the pairing stream of the instance id. It gives "update" items, then one
// "done" or "failed" item, and then the loop ends. An error is the last item. A 404 on a
// new follow means that no pairing runs and that the backend keeps no failure. Read the instance.
// The SDK never opens the stream again. The end of the loop, a break too, closes the connection;
// the pairing continues in the backend until CancelPairing.
func (c *Client) FollowPairing(ctx context.Context, id string) iter.Seq2[PairingItem, error] {
	return func(yield func(PairingItem, error) bool) {
		if err := c.followPairing(ctx, id, yield); err != nil {
			yield(PairingItem{}, err)
		}
	}
}

// followPairing gives the items to yield. It returns nil after done, failed or a break.
func (c *Client) followPairing(ctx context.Context, id string, yield func(PairingItem, error) bool) error {
	op := operations["followPairing"]
	target := c.baseURL + strings.ReplaceAll(op.path, "{id}", url.PathEscape(id))
	idle := idleTimeout

	// The idle time starts before the connection opens and starts again with each read.
	reqCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	timer := time.AfterFunc(idle, func() { cancel(errIdle) })
	defer timer.Stop()

	req, err := http.NewRequestWithContext(reqCtx, op.method, target, nil)
	// New checked the base URL, so a failure is a bug of the SDK.
	if err != nil {
		panic("backend: cannot build the request of followPairing")
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", userAgent)
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	res, err := c.http.Do(req)
	if err != nil {
		return streamFailure(ctx, reqCtx, idle, calyx.CodeNetworkError, "The connection to the backend failed.", err)
	}
	// Close also stops the read of a stream that is still open.
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode < 200 || res.StatusCode > 299 {
		text, err := readAnswer(res.Body)
		if errors.Is(err, errAnswerTooLarge) {
			return answerTooLarge("")
		}
		if err != nil && (ctx.Err() != nil || errors.Is(context.Cause(reqCtx), errIdle)) {
			return streamFailure(ctx, reqCtx, idle, calyx.CodeStreamFailed, "The connection to the backend failed.", err)
		}
		// With a partial body, problemError reports an answer that is not problem+json.
		return problemError(res, text, "")
	}
	if mt, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type")); mt != "text/event-stream" {
		detail := fmt.Sprintf("The backend answered %d with a body that is not an event stream.", res.StatusCode)
		return clientError(calyx.CodeInvalidResponse, detail, "", nil)
	}

	p := newParser(&idleReader{r: res.Body, timer: timer, idle: idle})
	for {
		event, data, err := p.nextItem()
		var ce *calyx.Error
		if errors.As(err, &ce) {
			return err
		}
		if err != nil {
			return streamFailure(ctx, reqCtx, idle, calyx.CodeStreamFailed, "The pairing stream broke.", err)
		}
		item, err := decodeItem(event, data)
		if err != nil {
			return err
		}
		// The loop body of the program is not idle time: pings wait in the socket meanwhile.
		timer.Stop()
		if !yield(item, nil) {
			return nil
		}
		timer.Reset(idle)
		if event != "update" {
			return nil
		}
	}
}

// streamFailure gives the code of a failed send or read: aborted or timeout from ctx of the
// program, stream_failed from the idle timer, and code for every other failure.
func streamFailure(ctx, reqCtx context.Context, idle time.Duration, code calyx.Code, detail string, err error) *calyx.Error {
	if ctx.Err() != nil {
		return transportError(ctx, "", err)
	}
	if errors.Is(context.Cause(reqCtx), errIdle) {
		detail := fmt.Sprintf("The pairing stream sent no data for %s.", idle)
		return clientError(calyx.CodeStreamFailed, detail, "", errIdle)
	}
	return clientError(code, detail, "", err)
}

// decodeItem decodes the data of a known event. The parser already checked that it is JSON.
func decodeItem(event string, data json.RawMessage) (PairingItem, error) {
	item := PairingItem{Event: event}
	var target any
	switch event {
	case "update":
		item.Update = &Pairing{}
		target = item.Update
	case "done":
		item.Done = &Done{}
		target = item.Done
	default:
		item.Failed = &Failed{}
		target = item.Failed
	}
	if err := json.Unmarshal(data, target); err != nil {
		detail := "The backend sent a \"" + event + "\" event with data of the wrong shape."
		return PairingItem{}, clientError(calyx.CodeInvalidResponse, detail, "", err)
	}
	return item, nil
}

// idleReader starts the idle timer again each time data arrives, a ping too.
type idleReader struct {
	r     io.Reader
	timer *time.Timer
	idle  time.Duration
}

func (r *idleReader) Read(b []byte) (int, error) {
	n, err := r.r.Read(b)
	if n > 0 {
		r.timer.Reset(r.idle)
	}
	return n, err
}
