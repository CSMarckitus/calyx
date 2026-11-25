"""A client for the gRPC API of the Calyx core."""

from ._version import __version__
from .client import AsyncCoreClient, CoreClient
from .streams import AsyncEventStream, AsyncInstanceStream, EventStream, InstanceStream

__all__ = [
    "AsyncCoreClient",
    "AsyncEventStream",
    "AsyncInstanceStream",
    "CoreClient",
    "EventStream",
    "InstanceStream",
    "__version__",
]
