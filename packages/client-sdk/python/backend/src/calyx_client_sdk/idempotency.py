import re
import uuid

from ._generated.codes import ClientCode
from .errors import CalyxError, Source

__all__ = ["new_idempotency_key"]


def new_idempotency_key() -> str:
    """Make a key for one message or one bulk job (a UUID version 4).

    Store the key before the call. To repeat a failed send, use the same key: then the message does
    not go out two times. Calyx forgets a key after 24 hours, so do not repeat a send after that
    time.
    """
    return str(uuid.uuid4())


def resolve_idempotency_key(key: object) -> str:
    """Check the key of the program, or make one. Only for the two Calyx clients."""
    # `object`, because a program without a type checker can pass any value, for example a UUID.
    if key is None:
        return new_idempotency_key()
    if key == "":
        raise _bad_key("The idempotency key is empty. Maybe the program lost its stored key.")
    if not isinstance(key, str) or not re.fullmatch("[!-~]{1,255}", key):
        # The text never holds the key: it can be a secret that is in the wrong place.
        raise _bad_key(
            "The idempotency key must be a string of 1 to 255 printable ASCII characters, with no "
            "space."
        )
    return key


def _bad_key(detail: str) -> CalyxError:
    return CalyxError(source=Source.CLIENT, code=ClientCode.INVALID_CONFIG, status=0, detail=detail)
