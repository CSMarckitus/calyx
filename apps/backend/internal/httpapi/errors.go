// Package httpapi is the HTTP layer of the backend. It only translates: a handler checks the shape
// of the request, calls one service method and encodes the answer.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"calyx/backend/internal/apierr"
	"calyx/backend/internal/core"
)

const problemTypeBase = "https://calyx.dev/errors/"

// problemCode is the code of an error answer. The list comes from the errors table, so no copy
// of it can go out of date.
type problemCode string

func (problemCode) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Enum = nil
	for _, code := range apierr.Codes() {
		s.Enum = append(s.Enum, code)
	}
	return s
}

// problem is the body of an error answer (RFC 9457).
type problem struct {
	Type   string      `json:"type" doc:"A URI that names the error: https://calyx.dev/errors/ and the code."`
	Title  string      `json:"title" doc:"The name of the HTTP status, for example Conflict."`
	Status int         `json:"status" doc:"The HTTP status."`
	Detail string      `json:"detail" doc:"A sentence for people that says what went wrong. In a program, use code."`
	Code   problemCode `json:"code" doc:"The error code, for example not_found."`
	// Errors lists the wrong fields of an invalid request.
	Errors []problemField `json:"errors,omitempty" doc:"The first 20 wrong fields; with more than 20, a last item counts the rest. Absent when the error is not about fields."`

	// retryAfter is the wait for the Retry-After header. It stays out of the body and the schema.
	retryAfter time.Duration
}

// problemField is one wrong field in a problem body.
type problemField struct {
	Location string `json:"location" doc:"The path of the field in the request, for example body.messages[3].to. body alone is the whole body."`
	Message  string `json:"message" doc:"What is wrong with the field. It never repeats the value."`
}

// Error, GetStatus, ContentType and GetHeaders make a problem a Huma StatusError,
// ContentTypeFilter and HeadersError: Huma writes a returned problem as it is.
func (p *problem) Error() string  { return p.Detail }
func (p *problem) GetStatus() int { return p.Status }

func (p *problem) ContentType(ct string) string {
	if ct == "application/json" {
		return "application/problem+json"
	}
	return ct
}

// GetHeaders holds only Retry-After. The API middleware and writeProblem set Cache-Control; Huma
// adds these headers to the ones already set, so a second Cache-Control would repeat it.
func (p *problem) GetHeaders() http.Header {
	h := http.Header{}
	switch {
	case p.Code == apierr.CodeRateLimited:
		h.Set("Retry-After", strconv.Itoa(retryAfterSeconds))
	case p.retryAfter > 0:
		h.Set("Retry-After", strconv.FormatInt(int64((p.retryAfter+time.Second-1)/time.Second), 10))
	}
	return h
}

// maxProblemFields caps the field errors in one answer, to keep the body small.
const maxProblemFields = 20

// capFields keeps at most maxProblemFields items. A last item tells how many items it dropped.
func capFields(fields []problemField) []problemField {
	rest := len(fields) - maxProblemFields
	if rest <= 0 {
		return fields
	}
	msg := fmt.Sprintf("%d more field errors.", rest)
	if rest == 1 {
		msg = "1 more field error."
	}
	return append(fields[:maxProblemFields:maxProblemFields], problemField{Location: "body", Message: msg})
}

// retryAfterSeconds is the value of Retry-After for a 429 rate_limited answer. The limits count
// per minute.
const retryAfterSeconds = 60

// toProblem is the one conversion of an error into a problem body. A request that ended because
// the client left gets a problem with status 499, which nobody writes. A plain error, or an
// apierr "internal", is logged at Error level with the request id; its detail names the request
// id only. Field paths get the "body." prefix.
func toProblem(ctx context.Context, err error) *problem {
	// The client left and the work stopped because of it. This is not an internal error.
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		return &problem{Status: statusClientClosed}
	}
	ae, ok := apierr.As(err)
	if !ok || ae.Code == apierr.CodeInternal {
		id := requestIDOf(ctx)
		cause := err
		if ok {
			if c := ae.Unwrap(); c != nil {
				cause = c
			} else {
				cause = ae
			}
		}
		loggerOf(ctx).Error("Internal error.", "request_id", id, "error", core.Loggable(cause))
		ae = apierr.New(http.StatusInternalServerError, apierr.CodeInternal,
			fmt.Sprintf("Internal error. Request id: %s.", id))
	}
	var fields []problemField
	for _, fe := range ae.Errors {
		fields = append(fields, problemField{Location: "body." + fe.Field, Message: fe.Message})
	}
	return &problem{
		Type:   problemTypeBase + ae.Code,
		Title:  http.StatusText(ae.Status),
		Status: ae.Status,
		Detail: ae.Detail,
		Code:   problemCode(ae.Code),
		Errors: capFields(fields),

		retryAfter: ae.RetryAfter,
	}
}

// serviceError is what an operation returns for an error of a service: its problem body, which
// Huma writes as it is. It returns nil for a nil error.
func serviceError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	return toProblem(ctx, err)
}

// writeError writes err as application/problem+json, through toProblem. A request that ended
// because the client left gets no answer.
func writeError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	p := toProblem(withLogger(r.Context(), log), err)
	if p.Status == statusClientClosed {
		return
	}
	writeProblem(w, p)
}

// writeProblem writes p as application/problem+json, as it is.
func writeProblem(w http.ResponseWriter, p *problem) {
	h := w.Header()
	for k, v := range p.GetHeaders() {
		h[k] = v
	}
	h.Set("Content-Type", "application/problem+json")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// init installs the bridge once, before any API exists: huma.NewError is a package variable, and
// a later assignment would race with parallel tests. Huma calls NewErrorWithContext for the
// errors of a request; the default version drops the context, so the bridge replaces both.
//
// The same function makes the document show lists as arrays that are never null. The operations
// write [] for an empty list.
func init() {
	huma.DefaultArrayNullable = false
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		return humaError(nil, status, msg, errs)
	}
	huma.NewErrorWithContext = func(ctx huma.Context, status int, msg string, errs ...error) huma.StatusError {
		return humaError(ctx, status, msg, errs)
	}
}

// noRequestLog takes the log lines of a Huma error without a request: defineErrors builds one at
// registration to get the error schema, and that is not an internal error.
var noRequestLog = slog.New(slog.DiscardHandler)

// humaError maps an error that Huma creates to the problem body.
// It copies the location and the message of each error detail, never its value: Huma fills the
// value with the rejected input.
func humaError(hctx huma.Context, status int, msg string, errs []error) *problem {
	ctx := withLogger(context.Background(), noRequestLog)
	var op *huma.Operation
	if hctx != nil {
		ctx, op = hctx.Context(), hctx.Operation()
	}
	var details []*huma.ErrorDetail
	for _, err := range errs {
		if d, ok := err.(huma.ErrorDetailer); ok {
			details = append(details, d.ErrorDetail())
		}
	}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		// Row 1: the client left. Nobody reads the answer, and nothing failed in the backend.
		return &problem{Status: statusClientClosed}
	case slices.ContainsFunc(details, func(d *huma.ErrorDetail) bool { return d.Location == "path.id" }):
		// Row 2: a malformed id cannot exist.
		return toProblem(ctx, apierr.NotFound())
	case status >= 400 && status <= 499:
		// Row 3: shape, malformed JSON, missing body, 408, 413, 415.
		fields := make([]problemField, 0, len(details))
		for _, d := range details {
			fields = append(fields, problemField{Location: d.Location, Message: d.Message})
		}
		p := toProblem(ctx, apierr.Invalid(humaErrorDetail(status, op, len(fields) > 0)))
		p.Errors = capFields(fields)
		return p
	}
	// Row 4: anything else is internal. The log gets the message and the error texts, but only
	// the location and the message of an error detail.
	causes := make([]error, 0, len(errs))
	for _, err := range errs {
		if d, ok := err.(huma.ErrorDetailer); ok {
			causes = append(causes, fmt.Errorf("%s: %s", d.ErrorDetail().Location, d.ErrorDetail().Message))
		} else if err != nil {
			causes = append(causes, err)
		}
	}
	cause := errors.New(msg)
	if len(causes) > 0 {
		cause = fmt.Errorf("%s: %w", msg, errors.Join(causes...))
	}
	return toProblem(ctx, apierr.Internal(cause))
}

// humaErrorDetail is the detail of a 400 that Huma created.
func humaErrorDetail(status int, op *huma.Operation, hasFields bool) string {
	switch {
	case hasFields:
		return "The request has invalid fields."
	case status == http.StatusRequestEntityTooLarge && op != nil:
		return fmt.Sprintf("The body is larger than %d bytes.", op.MaxBodyBytes)
	case status == http.StatusRequestTimeout:
		return "The body did not arrive in time."
	case status == http.StatusBadRequest:
		// Huma's only 400 without details: "request body is required".
		return "The body is empty."
	}
	return "The request is not valid."
}
