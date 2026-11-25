from collections.abc import Iterable, Mapping
from dataclasses import dataclass
from enum import StrEnum
from functools import partial
from typing import Any, Final

from ._generated.advice import ADVICE


class Source(StrEnum):
    """The place where an error starts."""

    BACKEND = "backend"
    """The Calyx backend refused the request, or it could not do it."""

    CORE = "core"
    """The Calyx core refused the request, or it could not do it. Only the core client gives it."""

    CLIENT = "client"
    """The SDK found the problem, for example a time limit, the network or a wrong setting."""


class RetryAdvice(StrEnum):
    """What your program can do after an error. The SDK never repeats a request by itself."""

    RETRY_NOW = "retry_now"
    """Nothing happened, or the result is unknown. Send the same request again now. A send uses the
    same idempotency key again."""

    RETRY_LATER = "retry_later"
    """Wait for `retry_after` seconds or more. Then send the same request again, with the same
    idempotency key. If `retry_after` is 0, choose the time to wait."""

    CHECK_IF_SENT = "check_if_sent"
    """Nobody knows if WhatsApp got the message, and this key cannot send again. Check the chat
    first. If the message is not there, send it with a new key after 10 minutes or more. For a core
    error, count the 10 minutes from `metadata["at"]`. For a backend error, `detail` can give the
    earliest time."""

    DO_NOT_RETRY = "do_not_retry"
    """The same request fails again, or it sends a duplicate. Change the request or the
    configuration first."""

    UNKNOWN = "unknown"
    """The SDK has no advice. After a failed `create_client` or `create_instance`, first check if
    the record exists. A second call can make a second record."""


@dataclass(frozen=True, slots=True, kw_only=True)
class FieldError:
    """One problem with one field of a request. The backend gives it."""

    location: str
    """The place of the field in the request, for example `body.text`."""

    message: str
    """Why the backend refused the field."""


class CalyxError(Exception):
    """The one error of the SDK. Use `code` to know what happened and `retry` to know what to do.

    Example:

        try:
            client.send_message(...)
        except CalyxError as error:
            if error.retry is RetryAdvice.RETRY_LATER:
                wait = error.retry_after or 30

    The text of the error has the code, the status, the request id and the idempotency key. With the
    source `Source.CLIENT`, it also has `detail`. The `detail` of a server can have private data, so
    the text of the error leaves it out.
    """

    # Final makes the fields read-only for a type checker. A __setattr__ that refuses each change
    # would break contextlib, which sets __traceback__.

    source: Final[Source]
    """The place where the error starts: the backend, the core or the SDK."""

    code: Final[str]
    """What happened. Compare it with `BackendCode`, `CoreCode` or `ClientCode`. A newer server
    can give a code that is in none of these classes."""

    status: Final[int]
    """The HTTP status (backend) or the number of the gRPC status (core). 0 when `source` is
    `Source.CLIENT`."""

    detail: Final[str]
    """An explanation for the developer, in English. The SDK never puts the API key or the core
    token in it. Do not compare this text: use `code`. The text of the error has it only when
    `source` is `Source.CLIENT`."""

    retry: Final[RetryAdvice]
    """What your program can do now, for example send the same request again."""

    errors: Final[tuple[FieldError, ...]]
    """The fields of the request that the backend refused. Empty for other errors."""

    request_id: Final[str | None]
    """The id of the request in the logs of the backend. `None` when the backend gave none."""

    retry_after: Final[int]
    """The minimum number of seconds to wait before the next try. 0 when the server gave none."""

    idempotency_key: Final[str | None]
    """The key of the send that failed. Use the same key again only when `retry` is
    `RetryAdvice.RETRY_NOW` or `RetryAdvice.RETRY_LATER`. The core forgets a key after 24 hours.
    `None` when the call sends nothing, or when the key is not valid."""

    metadata: Final[Mapping[str, str]]
    """More data about an error of the core, for example `at`. Empty for other errors."""

    def __init__(
        self,
        *,
        source: Source,
        code: str,
        status: int,
        detail: str,
        retry: RetryAdvice | None = None,
        errors: Iterable[FieldError] = (),
        request_id: str | None = None,
        retry_after: int = 0,
        idempotency_key: str | None = None,
        metadata: Mapping[str, str] | None = None,
    ) -> None:
        """The SDK makes this error. Your program makes one only in its tests.

        Without `retry`, the advice is the one of the source and the code.
        """
        self.source = source
        self.code = code
        self.status = status
        self.detail = detail
        self.retry = (
            retry
            if retry is not None
            else RetryAdvice(ADVICE.get((source, code), RetryAdvice.UNKNOWN))
        )
        self.errors = tuple(errors)
        self.request_id = request_id
        self.retry_after = retry_after
        self.idempotency_key = idempotency_key
        self.metadata = dict(metadata or {})

        text = self._identifiers()
        # The SDK wrote this detail, and it holds no private data. The text of a server can hold it.
        if source is Source.CLIENT and detail:
            text += f": {detail}"
        super().__init__(text)

    def _identifiers(self) -> str:
        text = f"{self.code} (status {self.status}"
        if self.request_id is not None:
            text += f", request id {self.request_id}"
        if self.idempotency_key is not None:
            text += f", idempotency key {self.idempotency_key}"
        return text + ")"

    def __repr__(self) -> str:
        return f"<{type(self).__name__} {self.source}: {self._identifiers()}, {self.retry}>"

    def __reduce__(self) -> tuple[Any, ...]:
        # The default passes `args` by position, and the constructor takes keywords only.
        make = partial(
            type(self),
            source=self.source,
            code=self.code,
            status=self.status,
            detail=self.detail,
            retry=self.retry,
            errors=self.errors,
            request_id=self.request_id,
            retry_after=self.retry_after,
            idempotency_key=self.idempotency_key,
            metadata=self.metadata,
        )
        return make, ()
