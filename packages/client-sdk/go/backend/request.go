package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
	"github.com/csmarckitus/calyx/packages/client-sdk/go/internal/errcause"
	"github.com/csmarckitus/calyx/packages/client-sdk/go/internal/key"
)

// maxAnswerBytes is the largest answer body that the SDK reads (32 MiB). It is a variable so
// that the tests can make it small.
var maxAnswerBytes = 32 << 20

// userAgent is the User-Agent header of every request.
var userAgent = "calyx-go-sdk/" + calyx.Version

// errAnswerTooLarge is the cause of the invalid_response for an answer over maxAnswerBytes.
var errAnswerTooLarge = errors.New("the answer is larger than the size limit")

// readAnswer reads a body, and gives errAnswerTooLarge when it is longer than maxAnswerBytes.
// It reads one byte more than the limit, so it never keeps a larger body.
func readAnswer(r io.Reader) ([]byte, error) {
	text, err := io.ReadAll(io.LimitReader(r, int64(maxAnswerBytes)+1))
	if err == nil && len(text) > maxAnswerBytes {
		return nil, errAnswerTooLarge
	}
	return text, err
}

// answerTooLarge is the client error for an answer over the size limit.
func answerTooLarge(idemKey string) *calyx.Error {
	return clientError(calyx.CodeInvalidResponse,
		"The backend answered with a body that is larger than the SDK reads.", idemKey, errAnswerTooLarge)
}

// request is one call of an operation of the table. The body must be nil for an operation
// without a request body and not nil for one with a body: the backend answers 400 to a missing
// body.
type request struct {
	op     string // the operationId, the key of the operations table
	id     string // the value of {id} in the path; empty when the path has none
	body   any    // encoded as JSON; nil sends no body and no Content-Type
	result any    // a pointer that receives the JSON body of a 2xx answer; nil ignores the body
	key    string // the Idempotency-Key; empty sends no header
}

// sendKey gives the idempotency key of a send call: the key of the program, or a new one.
func sendKey(opts SendOptions) string {
	if opts.IdempotencyKey != "" {
		return opts.IdempotencyKey
	}
	return key.New()
}

// do sends one request and never sends it again. It returns the HTTP status of a 2xx answer.
// Every error is a *calyx.Error that carries r.key.
func (c *Client) do(ctx context.Context, r request) (int, error) {
	// Step 1: the operation table.
	op, ok := operations[r.op]
	if !ok {
		panic("backend: unknown operation " + r.op)
	}

	// Step 2: the URL.
	target := c.baseURL + strings.ReplaceAll(op.path, "{id}", url.PathEscape(r.id))

	// Step 5: a default would shorten a deadline of the program, so it applies only without one.
	if _, has := ctx.Deadline(); !has && op.limit > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, op.limit)
		defer cancel()
	}

	var body io.Reader
	if r.body != nil {
		data, err := json.Marshal(r.body)
		// The generated request types always encode, so a failure is a bug of the SDK.
		if err != nil {
			panic("backend: cannot encode the body of " + r.op)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, op.method, target, body)
	// New checked the base URL and the table has valid methods, so a failure is a bug of the SDK.
	if err != nil {
		panic("backend: cannot build the request of " + r.op)
	}
	if r.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	req.Header.Set("User-Agent", userAgent)

	// Step 3: the API key.
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	// Step 4: the idempotency key of a send call.
	if r.key != "" {
		req.Header.Set("Idempotency-Key", r.key)
	}

	// Step 6: send the request and read the whole answer.
	res, err := c.http.Do(req)
	var text []byte
	if err == nil {
		text, err = readAnswer(res.Body)
		_ = res.Body.Close()
	}
	if errors.Is(err, errAnswerTooLarge) {
		return 0, forCall(r.op, answerTooLarge(r.key))
	}
	if err != nil {
		return 0, forCall(r.op, transportError(ctx, r.key, err))
	}

	// Step 8: an answer that is not 2xx.
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return 0, forCall(r.op, problemError(res, text, r.key))
	}

	// Step 7: decode the 2xx body. A 204 or an empty body gives nothing.
	if r.result != nil && res.StatusCode != http.StatusNoContent && len(text) > 0 {
		if err := json.Unmarshal(text, r.result); err != nil {
			detail := fmt.Sprintf("The backend answered %d with a body that is not JSON.", res.StatusCode)
			return 0, forCall(r.op, clientError(calyx.CodeInvalidResponse, detail, r.key, err))
		}
	}
	return res.StatusCode, nil
}

// transportError gives the code of a failed send or read: aborted when the program canceled
// ctx, timeout when a deadline passed, network_error for every other failure.
func transportError(ctx context.Context, idemKey string, err error) *calyx.Error {
	switch ctxErr := ctx.Err(); {
	case errors.Is(ctxErr, context.Canceled):
		return clientError(calyx.CodeAborted, "The program canceled the call.", idemKey, ctxErr)
	case errors.Is(ctxErr, context.DeadlineExceeded):
		return clientError(calyx.CodeTimeout, "The backend did not answer in time.", idemKey, ctxErr)
	}
	return clientError(calyx.CodeNetworkError, "The connection to the backend failed.", idemKey, err)
}

// problemError converts an answer that is not 2xx. Only a JSON body with a code is a backend
// error; any other body is invalid_response.
func problemError(res *http.Response, text []byte, idemKey string) *calyx.Error {
	var p struct {
		Code   *string `json:"code"`
		Detail string  `json:"detail"`
		Title  string  `json:"title"`
		Errors []struct {
			Location string `json:"location"`
			Message  string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(text, &p) != nil || p.Code == nil {
		detail := fmt.Sprintf("The backend answered %d with a body that is not problem+json.", res.StatusCode)
		return clientError(calyx.CodeInvalidResponse, detail, idemKey, nil)
	}
	e := &calyx.Error{
		Source:         calyx.SourceBackend,
		Code:           calyx.Code(*p.Code),
		Status:         res.StatusCode,
		Detail:         p.Detail,
		Retry:          calyx.AdviceFor(calyx.SourceBackend, calyx.Code(*p.Code)),
		RequestID:      res.Header.Get("X-Request-Id"),
		IdempotencyKey: idemKey,
	}
	if e.Detail == "" {
		e.Detail = p.Title
	}
	for _, fe := range p.Errors {
		e.Errors = append(e.Errors, calyx.FieldError{Location: fe.Location, Message: fe.Message})
	}
	// The backend sends Retry-After as a number of seconds. The SDK reads only this form.
	if s, err := strconv.Atoi(strings.TrimSpace(res.Header.Get("Retry-After"))); err == nil && s > 0 {
		e.RetryAfter = time.Duration(s) * time.Second
	}
	return e
}

// clientError builds an error that the SDK itself found. The cause never reaches Error().
func clientError(code calyx.Code, detail, idemKey string, cause error) *calyx.Error {
	e := &calyx.Error{
		Source:         calyx.SourceClient,
		Code:           code,
		Detail:         detail,
		Retry:          calyx.AdviceFor(calyx.SourceClient, code),
		IdempotencyKey: idemKey,
	}
	if cause != nil {
		errcause.Set(e, cause)
	}
	return e
}
