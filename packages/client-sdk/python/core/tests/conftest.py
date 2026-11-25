import gc
from collections.abc import Iterator

import certificates
import pytest
from certificates import Authority
from fake_core import Call, call_async, call_sync


@pytest.fixture(params=[call_sync, call_async], ids=["sync", "async"])
def call(request: pytest.FixtureRequest) -> Call:
    chosen: Call = request.param
    return chosen


@pytest.fixture(autouse=True)
def nothing_open() -> Iterator[None]:
    yield
    # A thing that nobody closed gives its warning here, in the test that left it open.
    gc.collect()


@pytest.fixture(scope="module")
def authority(tmp_path_factory: pytest.TempPathFactory) -> Authority:
    return certificates.make_authority(tmp_path_factory.mktemp("tls") / "ca")


@pytest.fixture(scope="module")
def other_authority(tmp_path_factory: pytest.TempPathFactory) -> Authority:
    return certificates.make_authority(tmp_path_factory.mktemp("tls") / "ca")
