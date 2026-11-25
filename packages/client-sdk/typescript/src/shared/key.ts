// One new idempotency key for each new send.
export function newIdempotencyKey(): string {
  return crypto.randomUUID();
}
