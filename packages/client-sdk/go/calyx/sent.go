package calyx

// Sent is the result of a send call. It holds the answer and the idempotency key that the call
// used, so that the program can send again with the same key.
type Sent[T any] struct {
	// Data is the answer of the send call.
	Data T
	// IdempotencyKey is the key that the call used.
	IdempotencyKey string
}
