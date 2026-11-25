import type { RetryAdvice } from "../errors/index.js";

// The operations that make a record and take no idempotency key. The list is in
// testdata/create-calls.json too, and a test compares the two.
export const createCallOperations: readonly string[] = ["createClient", "createInstance"];

// The client errors for which the program cannot know if the backend made the record.
export const createCallCodes: readonly string[] = [
  "timeout",
  "network_error",
  "invalid_response",
  "aborted",
];

// The advice for those errors. A second call can make a second record.
export const createCallRetry: RetryAdvice = "unknown";
