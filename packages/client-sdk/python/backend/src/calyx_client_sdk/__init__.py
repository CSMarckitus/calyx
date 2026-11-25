"""A client for the HTTP API of the Calyx backend."""

from ._generated.codes import BackendCode, ClientCode, CoreCode
from ._version import __version__
from .client import AsyncBackendClient, BackendClient
from .errors import CalyxError, FieldError, RetryAdvice, Source
from .events import (
    BulkFinished,
    BulkFinishedEvent,
    ConnectionUpdate,
    ConnectionUpdateEvent,
    DeliveryStatus,
    Event,
    InvalidPayloadEvent,
    MessageReceived,
    MessageReceivedEvent,
    MessageStatus,
    MessageStatusEvent,
    OtherEvent,
    decode_event,
)
from .idempotency import new_idempotency_key
from .pairing import (
    AsyncPairingStream,
    PairingDone,
    PairingFailed,
    PairingStream,
    PairingUpdate,
)
from .sent import Sent
from .webhooks import verify_webhook

__all__ = [
    "AsyncBackendClient",
    "AsyncPairingStream",
    "BackendClient",
    "BackendCode",
    "BulkFinished",
    "BulkFinishedEvent",
    "CalyxError",
    "ClientCode",
    "ConnectionUpdate",
    "ConnectionUpdateEvent",
    "CoreCode",
    "DeliveryStatus",
    "Event",
    "FieldError",
    "InvalidPayloadEvent",
    "MessageReceived",
    "MessageReceivedEvent",
    "MessageStatus",
    "MessageStatusEvent",
    "OtherEvent",
    "PairingDone",
    "PairingFailed",
    "PairingStream",
    "PairingUpdate",
    "RetryAdvice",
    "Sent",
    "Source",
    "__version__",
    "decode_event",
    "new_idempotency_key",
    "verify_webhook",
]
