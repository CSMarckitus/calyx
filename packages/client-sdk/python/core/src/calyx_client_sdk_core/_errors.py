"""The errors of the calls (CONTRACT.md parts 4 to 6). A port of packages/client-sdk/go/core."""

from collections.abc import Iterable
from typing import Final

import grpc
from calyx_client_sdk import CalyxError, ClientCode, RetryAdvice, Source
from google.protobuf.any_pb2 import Any as AnyMessage
from google.protobuf.message import DecodeError, Message
from google.rpc.error_details_pb2 import ErrorInfo, RetryInfo
from google.rpc.status_pb2 import Status

# The domain of the reasons of the core.
_DOMAIN: Final = "calyx.core.v1"


def client_error(
    code: str, detail: str, key: str | None, *, retry: RetryAdvice | None = None
) -> CalyxError:
    """Makes an error that the SDK found in a call."""
    return CalyxError(
        source=Source.CLIENT,
        code=code,
        status=0,
        detail=detail,
        retry=retry,
        idempotency_key=key,
    )


def closed_error(key: str | None) -> CalyxError:
    return client_error(ClientCode.ABORTED, "The client is closed.", key)


def timeout_error(key: str | None) -> CalyxError:
    return client_error(ClientCode.TIMEOUT, "The core did not answer in time.", key)


def core_error(
    status: grpc.StatusCode,
    detail: str,
    trailers: Iterable[tuple[str, str | bytes]],
    key: str | None,
) -> CalyxError:
    """Makes the error of a call from its status. It has no cause: the text of an error of gRPC has
    the detail, and the detail can be private."""
    code = status.name
    metadata: dict[str, str] = {}
    retry_after = 0
    has_info = False
    for item in _details(trailers):
        if _is(item, ErrorInfo):
            info = _parse(ErrorInfo, item.value)
            # The first one of the core wins.
            if info is None or has_info or info.domain != _DOMAIN:
                continue
            has_info = True
            # An empty reason keeps the name of the status.
            code = info.reason or code
            metadata = dict(info.metadata)
        elif _is(item, RetryInfo) and (retry := _parse(RetryInfo, item.value)):
            # Round up, so the program never waits less than the core asks. A negative delay means
            # no wait.
            delay = retry.retry_delay
            retry_after = max(0, delay.seconds + (1 if delay.nanos > 0 else 0))
    return CalyxError(
        source=Source.CORE,
        code=code,
        status=status.value[0],
        detail=detail,
        retry_after=retry_after,
        idempotency_key=key,
        metadata=metadata,
    )


def _details(trailers: Iterable[tuple[str, str | bytes]]) -> Iterable[AnyMessage]:
    for name, value in trailers:
        if name == "grpc-status-details-bin" and isinstance(value, bytes):
            status = _parse(Status, value)
            return () if status is None else status.details
    return ()


def _is(item: AnyMessage, kind: type[Message]) -> bool:
    return item.type_url == f"type.googleapis.com/{kind.DESCRIPTOR.full_name}"


def _parse[M: Message](kind: type[M], data: bytes) -> M | None:
    """Bytes that do not decode count as no detail."""
    try:
        return kind.FromString(data)
    # The pure Python version of protobuf gives a UnicodeDecodeError.
    except (DecodeError, UnicodeDecodeError):
        return None
