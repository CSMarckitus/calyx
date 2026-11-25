// Package calyx holds the types that the backend client and the core client of the Calyx SDK
// share: the error type, the code table, the retry advice and the events.
//
// The package uses two Go idioms:
//
//   - Every failure is an *Error. Read it with errors.As, then read its Code, Source and Retry
//     fields, or call HasCode.
//   - Code, Source and Retry are open string types. A new backend can send a value that this
//     SDK version does not know, so a switch on these types needs a default case.
package calyx
