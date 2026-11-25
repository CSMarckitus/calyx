from dataclasses import dataclass


@dataclass(frozen=True, slots=True, kw_only=True)
class Sent[T]:
    """The result of a call that sends a message or starts a bulk job."""

    data: T
    """The answer of the server."""

    idempotency_key: str
    """The key that the call used: your key, or the key that the SDK made."""
