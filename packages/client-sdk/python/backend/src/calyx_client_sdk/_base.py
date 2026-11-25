from collections.abc import Mapping

from ._call import Config, Operation, finish, prepare
from ._http import AsyncTransport, SyncTransport
from .idempotency import resolve_idempotency_key
from .sent import Sent


class SyncBase:
    """The generated methods of `BackendClient` use it."""

    def __init__(self, config: Config, transport: SyncTransport) -> None:
        self._config = config
        self._transport = transport

    def _call[T](
        self,
        operation: Operation[T],
        *,
        path_id: str | None = None,
        arguments: Mapping[str, object] | None = None,
        idempotency_key: str | None = None,
        time_limit: float | None = None,
    ) -> T:
        prepared = prepare(
            self._config,
            operation,
            path_id=path_id,
            arguments=arguments,
            idempotency_key=idempotency_key,
            time_limit=time_limit,
        )
        return finish(prepared, self._transport.send(prepared))

    def _send[T](
        self,
        operation: Operation[T],
        *,
        path_id: str,
        arguments: Mapping[str, object],
        idempotency_key: str | None,
        time_limit: float | None,
    ) -> Sent[T]:
        # The key is made before the request, so each error of the call has it.
        key = resolve_idempotency_key(idempotency_key)
        data = self._call(
            operation,
            path_id=path_id,
            arguments=arguments,
            idempotency_key=key,
            time_limit=time_limit,
        )
        return Sent(data=data, idempotency_key=key)


class AsyncBase:
    """The generated methods of `AsyncBackendClient` use it."""

    def __init__(self, config: Config, transport: AsyncTransport) -> None:
        self._config = config
        self._transport = transport

    async def _call[T](
        self,
        operation: Operation[T],
        *,
        path_id: str | None = None,
        arguments: Mapping[str, object] | None = None,
        idempotency_key: str | None = None,
        time_limit: float | None = None,
    ) -> T:
        prepared = prepare(
            self._config,
            operation,
            path_id=path_id,
            arguments=arguments,
            idempotency_key=idempotency_key,
            time_limit=time_limit,
        )
        return finish(prepared, await self._transport.send(prepared))

    async def _send[T](
        self,
        operation: Operation[T],
        *,
        path_id: str,
        arguments: Mapping[str, object],
        idempotency_key: str | None,
        time_limit: float | None,
    ) -> Sent[T]:
        key = resolve_idempotency_key(idempotency_key)
        data = await self._call(
            operation,
            path_id=path_id,
            arguments=arguments,
            idempotency_key=key,
            time_limit=time_limit,
        )
        return Sent(data=data, idempotency_key=key)
