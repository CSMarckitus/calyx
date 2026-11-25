package backend

import (
	"slices"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
)

// createOperations are the operations that make a record and take no idempotency key. The list
// is in testdata/create-calls.json too, and a test compares the two.
var createOperations = map[string]bool{"createClient": true, "createInstance": true}

// createCodes are the client errors for which the program cannot know if the backend made the
// record.
var createCodes = []calyx.Code{
	calyx.CodeTimeout, calyx.CodeNetworkError, calyx.CodeInvalidResponse, calyx.CodeAborted,
}

// createCallRetry is the advice for those errors. A second call can make a second record.
const createCallRetry = calyx.RetryUnknown

// forCall sets the advice of a create call on a client error with one of createCodes. It returns
// every other error as it is.
func forCall(op string, e *calyx.Error) *calyx.Error {
	if !createOperations[op] || e.Source != calyx.SourceClient {
		return e
	}
	if slices.Contains(createCodes, e.Code) {
		e.Retry = createCallRetry
	}
	return e
}
