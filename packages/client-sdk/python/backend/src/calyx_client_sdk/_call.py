import json
import re
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass, field
from typing import Final, cast
from urllib.parse import quote, urlsplit

from ._checks import check_time_limit, config_error, is_visible_ascii
from ._fields import NotJsonError, WrongShapeError, parse_json, read, to_json
from ._generated.codes import ClientCode
from ._version import __version__
from .errors import CalyxError, FieldError, RetryAdvice, Source
from .idempotency import resolve_idempotency_key

# The calls that make a record and take no idempotency key, and the client errors after which the
# program cannot know if the record exists. A test compares them with testdata/create-calls.json.
CREATE_OPERATIONS: Final = frozenset({"createClient", "createInstance"})
CREATE_CODES: Final = frozenset(
    {
        ClientCode.TIMEOUT,
        ClientCode.NETWORK_ERROR,
        ClientCode.INVALID_RESPONSE,
        ClientCode.ABORTED,
    }
)
CREATE_RETRY: Final = RetryAdvice.UNKNOWN


@dataclass(frozen=True, slots=True, kw_only=True)
class Config:
    """The checked arguments of a client. `make_config` makes it."""

    base_url: str
    api_key: str | None = field(repr=False)
    time_limit: float | None


@dataclass(frozen=True, slots=True, kw_only=True)
class RawAnswer:
    """One HTTP answer, with its whole body."""

    status: int
    headers: Sequence[tuple[bytes, bytes]] = field(repr=False)
    body: bytes = field(repr=False)

    def header(self, name: str) -> str | None:
        """Gives the first value of a header. The letter case of the name has no meaning."""
        wanted = name.lower().encode()
        for key, value in self.headers:
            if key.lower() == wanted:
                return value.decode("latin-1")
        return None


@dataclass(frozen=True, slots=True, kw_only=True)
class Operation[T]:
    """One operation of the backend. `T` is the result of its method."""

    id: str
    """The operationId."""

    method: str

    path: str
    """The path. `{id}` is the place of the path parameter."""

    time_limit: float | None
    """The default time limit in seconds. None for a stream: it has no default limit."""

    read: Callable[[RawAnswer], T]
    """Makes the result from a 2xx answer: one of the `read_` functions."""


@dataclass(frozen=True, slots=True, kw_only=True)
class Prepared[T]:
    """One request that is ready for a transport."""

    operation: Operation[T]
    url: str
    headers: Mapping[str, str] = field(repr=False)
    body: bytes | None = field(repr=False)
    idempotency_key: str | None
    time_limit: float | None

    @property
    def method(self) -> str:
        return self.operation.method


def make_config(base_url: str, api_key: str | None, time_limit: float | None) -> Config:
    """Checks the arguments of a client. An error never holds the base URL or the API key."""
    if not _is_base_url(base_url):
        raise config_error(
            "The base URL must start with http:// or https://, have a valid host, and have only "
            "ASCII characters, with no space and no @, ?, # or backslash."
        )
    if api_key is not None and not is_visible_ascii(api_key):
        raise config_error("The API key must have only printable ASCII characters, with no space.")
    return Config(
        base_url=base_url.removesuffix("/"),
        api_key=api_key,
        time_limit=None if time_limit is None else check_time_limit(time_limit),
    )


def prepare[T](
    config: Config,
    operation: Operation[T],
    *,
    path_id: str | None = None,
    arguments: Mapping[str, object] | None = None,
    idempotency_key: str | None = None,
    time_limit: float | None = None,
) -> Prepared[T]:
    """Makes the request of a call. `arguments` are the fields of the body: one that is None is left
    out. Without `arguments`, the request has no body. A send call must have an idempotency key."""
    # The key is first, because each later error of the call carries it.
    key = None if idempotency_key is None else resolve_idempotency_key(idempotency_key)

    path = operation.path
    if "{id}" in path:
        path = path.replace("{id}", _encode_path_id(path_id, key))

    if time_limit is not None:
        limit = check_time_limit(time_limit, key)
    elif operation.time_limit is None:
        # A short limit of the client must not stop a pairing.
        limit = None
    elif config.time_limit is not None:
        limit = config.time_limit
    else:
        limit = float(operation.time_limit)

    # A compressed body could grow past the size limit in memory.
    headers = {"Accept-Encoding": "identity", "User-Agent": f"calyx-python-sdk/{__version__}"}
    # An empty key means no key, as in the other SDKs.
    if config.api_key:
        headers["Authorization"] = f"Bearer {config.api_key}"
    if operation.time_limit is None:
        headers["Accept"] = "text/event-stream"
    body = None
    if arguments is not None:
        headers["Content-Type"] = "application/json"
        body = _encode_body(arguments, key)
    elif operation.method == "POST":
        # It gives "Content-Length: 0". Some proxies refuse a POST with no length.
        body = b""
    if key is not None:
        headers["Idempotency-Key"] = key

    return Prepared(
        operation=operation,
        url=config.base_url + path,
        headers=headers,
        body=body,
        idempotency_key=key,
        time_limit=limit,
    )


def finish[T](prepared: Prepared[T], answer: RawAnswer) -> T:
    """Makes the result of a call, or raises the error of its answer."""
    if not 200 <= answer.status <= 299:
        raise _answer_error(prepared, answer)
    try:
        return prepared.operation.read(answer)
    except NotJsonError as error:
        raise client_error(
            prepared,
            ClientCode.INVALID_RESPONSE,
            f"The backend answered {answer.status} with a body that is not JSON.",
        ) from error
    except WrongShapeError as error:
        # A repeated call gets the same shape, so "retry now" would be an endless loop.
        raise client_error(
            prepared,
            ClientCode.INVALID_RESPONSE,
            f"The backend answered {answer.status} with JSON of the wrong shape. {error}",
            retry=RetryAdvice.UNKNOWN,
        ) from error


def check_stream(prepared: Prepared[object], answer: RawAnswer) -> None:
    """Raises the error of an answer that is not an event stream."""
    if not 200 <= answer.status <= 299:
        raise _answer_error(prepared, answer)
    kind = (answer.header("Content-Type") or "").partition(";")[0]
    if kind.strip().lower() != "text/event-stream":
        raise client_error(
            prepared,
            ClientCode.INVALID_RESPONSE,
            f"The backend answered {answer.status} with a body that is not an event stream.",
        )


def client_error(
    prepared: Prepared[object], code: str, detail: str, *, retry: RetryAdvice | None = None
) -> CalyxError:
    """Makes an error that the SDK found in a call. It has the key and the advice of the call."""
    if prepared.operation.id in CREATE_OPERATIONS and code in CREATE_CODES:
        retry = CREATE_RETRY
    return CalyxError(
        source=Source.CLIENT,
        code=code,
        status=0,
        detail=detail,
        retry=retry,
        idempotency_key=prepared.idempotency_key,
    )


def read_model[T](kind: type[T]) -> Callable[[RawAnswer], T]:
    """For an operation that gives an object of a dataclass."""

    def read_answer(answer: RawAnswer) -> T:
        return read(kind, parse_json(answer.body))

    return read_answer


def read_status(answer: RawAnswer) -> int:
    """For an operation with 2xx codes that differ in meaning."""
    return answer.status


def read_nothing(answer: RawAnswer) -> None:
    """For an operation with no answer body."""


def _answer_error(prepared: Prepared[object], answer: RawAnswer) -> CalyxError:
    if 300 <= answer.status <= 399:
        return client_error(
            prepared,
            ClientCode.INVALID_RESPONSE,
            f"The backend answered {answer.status}. The SDK does not follow redirects.",
        )
    problem = _parse_problem(answer.body)
    code = problem.get("code")
    if not isinstance(code, str):
        return client_error(
            prepared,
            ClientCode.INVALID_RESPONSE,
            f"The backend answered {answer.status} with a body that is not problem+json.",
        )
    return CalyxError(
        source=Source.BACKEND,
        code=code,
        status=answer.status,
        detail=_text(problem, "detail") or _text(problem, "title"),
        errors=_field_errors(problem.get("errors")),
        request_id=answer.header("X-Request-Id") or None,
        retry_after=_retry_after(answer.header("Retry-After") or ""),
        idempotency_key=prepared.idempotency_key,
    )


def _parse_problem(body: bytes) -> dict[str, object]:
    try:
        value = parse_json(body)
    except NotJsonError:
        return {}
    return cast(dict[str, object], value) if isinstance(value, dict) else {}


# RFC 9457: a reader ignores a member of a problem that has the wrong type.
def _text(data: Mapping[str, object], name: str) -> str:
    value = data.get(name)
    return value if isinstance(value, str) else ""


def _field_errors(value: object) -> list[FieldError]:
    items = cast(list[object], value) if isinstance(value, list) else []
    objects = [cast(dict[str, object], item) for item in items if isinstance(item, dict)]
    return [FieldError(location=_text(o, "location"), message=_text(o, "message")) for o in objects]


def _retry_after(value: str) -> int:
    # 9 digits are 31 years. Python refuses to convert a very long number.
    match = re.fullmatch("0*([0-9]{1,9})", value.strip())
    return int(match[1]) if match else 0


# These take `object`, because a program without a type checker can pass any value.
def _is_base_url(value: object) -> bool:
    if not isinstance(value, str) or not is_visible_ascii(value) or re.search(r"[@?#\\]", value):
        return False
    try:
        parts = urlsplit(value)
        # These two raise ValueError. The name lookup of Python uses "idna" too.
        _ = parts.port
        (parts.hostname or "").encode("idna")
    except ValueError:
        return False
    return parts.scheme in ("http", "https") and bool(parts.hostname)


def _encode_path_id(value: object, key: str | None) -> str:
    # With one of these three ids, the request would go to another path.
    if isinstance(value, str) and value not in ("", ".", ".."):
        try:
            return quote(value, safe="")
        except UnicodeEncodeError:
            pass
    raise config_error(
        'The id of the call must be a string of valid Unicode that is not empty, "." or "..".', key
    )


def _encode_body(arguments: Mapping[str, object], key: str | None) -> bytes:
    try:
        items = {name: to_json(value) for name, value in arguments.items() if value is not None}
        text = json.dumps(items, ensure_ascii=False, allow_nan=False, separators=(",", ":"))
        return text.encode()
    except (TypeError, ValueError, RecursionError) as error:
        reason = str(error)
    # Outside the "except": the error of Python can hold the body, and it would be the context.
    raise config_error(
        "An argument of the call has a value that JSON cannot hold.", key
    ) from ValueError(reason)
