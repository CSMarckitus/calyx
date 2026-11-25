"""Each operation of the two clients, against a fake backend."""

import asyncio
import gc
import json
import subprocess
import sys
import time
import uuid
from collections.abc import Callable, Iterator
from dataclasses import dataclass, field, fields, is_dataclass
from datetime import datetime
from pathlib import Path
from typing import Any, Protocol

import certificates
import pytest
from fake_server import Connection, FakeServer, Request

from calyx_client_sdk import (
    AsyncBackendClient,
    BackendClient,
    CalyxError,
    ClientCode,
    RetryAdvice,
    Sent,
    Source,
    models,
)
from calyx_client_sdk import __version__ as version
from calyx_client_sdk._generated import operations

BACKEND_JSON = Path(__file__).parents[4] / "shared-types" / "openapi" / "backend.json"
DOCUMENT: dict[str, Any] = json.loads(BACKEND_JSON.read_text())
SCHEMAS: dict[str, Any] = DOCUMENT["components"]["schemas"]
ID = "0b5e1a52-0c7e-4a4b-9a53-0f1c8d1a7c11"
KEY = "key-1"
API_KEY = "cx_secret"

# The operations with no method, from CONTRACT.md part 2. The client has no login.
EXCLUDED = {
    "login",
    "logout",
    "createAPIKey",
    "inviteClientUser",
    "requestPasswordReset",
    "confirmPasswordReset",
    "acceptInvitation",
    "changePassword",
    "acknowledgeWhatsAppWeb",
    "lookupInvitation",
    "lookupPasswordReset",
    "cancelInvitation",
    "removeClientAccess",
    "followEvents",
}
STREAM = "followPairing"


@dataclass(frozen=True, slots=True, kw_only=True)
class Expected:
    """An operation of backend.json."""

    method: str
    path: str
    answer: dict[str, Any] | None
    statuses: list[int]
    takes_key: bool


def read_document() -> dict[str, Expected]:
    found: dict[str, Expected] = {}
    for path, item in DOCUMENT["paths"].items():
        for method, operation in item.items():
            answers = {s: a for s, a in operation["responses"].items() if s.startswith("2")}
            # A stream (followPairing, followEvents) has no JSON answer.
            bodies = [
                a["content"]["application/json"]
                for a in answers.values()
                if "application/json" in a.get("content", {})
            ]
            found[operation["operationId"]] = Expected(
                method=method.upper(),
                path=path,
                answer=bodies[0]["schema"] if bodies else None,
                statuses=[int(s) for s in answers],
                takes_key=any(
                    p["name"] == "Idempotency-Key" for p in operation.get("parameters", [])
                ),
            )
    return found


OPERATIONS = {id: e for id, e in read_document().items() if id not in EXCLUDED | {STREAM}}


@dataclass(frozen=True, slots=True, kw_only=True)
class Case:
    """One call of an operation."""

    method: str
    arguments: dict[str, Any] = field(default_factory=dict[str, Any])
    path_id: str | None = None
    """The first argument."""

    body: dict[str, Any] | None = None
    result: type | None = None


WEBHOOKS = [models.WebhookInput(url="https://example.com", event="message.received", secret="s")]
WIRE_WEBHOOKS = [{"url": "https://example.com", "event": "message.received", "secret": "s"}]
CREDENTIALS = models.MetaCredentialsInput(phone_number_id="1", access_token="a", app_secret="b")
WIRE_CREDENTIALS = {"phone_number_id": "1", "access_token": "a", "app_secret": "b"}

# The first case of an operation has every argument that the operation has (or that one overload
# has).
CASES: dict[str, list[Case]] = {
    "listAPIKeys": [Case(method="list_api_keys", result=models.ApiKeyList)],
    "revokeAPIKey": [Case(method="revoke_api_key", path_id=ID)],
    "listClients": [Case(method="list_clients", result=models.ClientList)],
    "createClient": [
        Case(
            method="create_client",
            arguments={"name": "Ana", "can_create_instances": False, "max_instances": 0},
            body={"name": "Ana", "can_create_instances": False, "max_instances": 0},
            result=models.OrgClient,
        ),
        Case(
            method="create_client",
            arguments={"name": "Ana"},
            body={"name": "Ana"},
            result=models.OrgClient,
        ),
    ],
    "getClient": [Case(method="get_client", path_id=ID, result=models.OrgClient)],
    "updateClient": [
        Case(
            method="update_client",
            path_id=ID,
            arguments={"name": "Ana", "can_create_instances": True, "max_instances": 5},
            body={"name": "Ana", "can_create_instances": True, "max_instances": 5},
            result=models.OrgClient,
        ),
        # The backend refuses a request with no body.
        Case(method="update_client", path_id=ID, body={}, result=models.OrgClient),
    ],
    "deleteClient": [Case(method="delete_client", path_id=ID)],
    "listInstances": [Case(method="list_instances", result=models.InstanceList)],
    "createInstance": [
        Case(
            method="create_instance",
            arguments={
                "provider": models.Provider.WHATSAPP_WEB,
                "display_name": "Shop",
                "locale": "es-ES",
                "pairing": "code",
                "phone": "34600000000",
                "device_identity": models.DeviceIdentity.FIREFOX_LINUX,
                "send_only": True,
                "webhooks": [],
                "client_id": ID,
            },
            body={
                "provider": "WHATSAPP_WEB",
                "display_name": "Shop",
                "locale": "es-ES",
                "pairing": "code",
                "phone": "34600000000",
                "device_identity": "FIREFOX_LINUX",
                "send_only": True,
                "webhooks": [],
                "client_id": ID,
            },
            result=models.CreateInstanceResult,
        ),
        Case(
            method="create_instance",
            arguments={
                "provider": models.Provider.META_CLOUD,
                "display_name": "Shop",
                "meta_credentials": CREDENTIALS,
                "webhooks": WEBHOOKS,
                "client_id": ID,
            },
            body={
                "provider": "META_CLOUD",
                "display_name": "Shop",
                "meta_credentials": WIRE_CREDENTIALS,
                "webhooks": WIRE_WEBHOOKS,
                "client_id": ID,
            },
            result=models.CreateInstanceResult,
        ),
        # A wrong mix goes to the backend, which refuses it.
        Case(
            method="create_instance",
            arguments={"provider": "NEW", "display_name": "Shop", "locale": "es-ES"},
            body={"provider": "NEW", "display_name": "Shop", "locale": "es-ES"},
            result=models.CreateInstanceResult,
        ),
    ],
    "getInstance": [Case(method="get_instance", path_id=ID, result=models.Instance)],
    "updateInstance": [
        Case(
            method="update_instance",
            path_id=ID,
            arguments={
                "display_name": "Shop",
                "enabled": False,
                "meta_credentials": CREDENTIALS,
                "send_only": True,
                "webhooks": WEBHOOKS,
            },
            body={
                "display_name": "Shop",
                "enabled": False,
                "meta_credentials": WIRE_CREDENTIALS,
                "send_only": True,
                "webhooks": WIRE_WEBHOOKS,
            },
            result=models.Instance,
        ),
        Case(
            method="update_instance",
            path_id=ID,
            arguments={"webhooks": [], "enabled": None},
            body={"webhooks": []},
            result=models.Instance,
        ),
    ],
    "removeInstance": [Case(method="remove_instance", path_id=ID, result=int)],
    "pairInstance": [
        Case(
            method="pair_instance",
            path_id=ID,
            arguments={
                "device_identity": "EDGE_WINDOWS",
                "locale": "es-ES",
                "pairing": "code",
                "phone": "34600000000",
            },
            body={
                "device_identity": "EDGE_WINDOWS",
                "locale": "es-ES",
                "pairing": "code",
                "phone": "34600000000",
            },
            result=models.PairResult,
        ),
        Case(method="pair_instance", path_id=ID, body={}, result=models.PairResult),
    ],
    "cancelPairing": [Case(method="cancel_pairing", path_id=ID)],
    "sendMessage": [
        Case(
            method="send_message",
            path_id=ID,
            arguments={"to": "34600000000", "text": "Hi\nñ"},
            body={"to": "34600000000", "text": "Hi\nñ"},
            result=models.SendMessageResult,
        )
    ],
    "createBulkJob": [
        Case(
            method="create_bulk_job",
            path_id=ID,
            arguments={
                "recipients": ("34600000000", "34600000001"),
                "text": "Hi",
                "pacing": models.BulkPacingInput(delay=models.RangeInput(min=1, max=2)),
            },
            body={
                "recipients": ["34600000000", "34600000001"],
                "text": "Hi",
                "pacing": {"delay": {"min": 1, "max": 2}},
            },
            result=models.BulkJob,
        ),
        Case(
            method="create_bulk_job",
            path_id=ID,
            arguments={
                "messages": [
                    models.BulkMessageInput(to="1", text="a"),
                    models.BulkMessageInput(to="2", text="b", delay_ms=9),
                ]
            },
            body={
                "messages": [
                    {"to": "1", "text": "a"},
                    {"to": "2", "text": "b", "delay_ms": 9},
                ]
            },
            result=models.BulkJob,
        ),
    ],
    "listBulkJobs": [Case(method="list_bulk_jobs", path_id=ID, result=models.BulkJobList)],
    "getBulkJob": [Case(method="get_bulk_job", path_id=ID, result=models.BulkJob)],
    "cancelBulkJob": [Case(method="cancel_bulk_job", path_id=ID, result=models.BulkJob)],
    "getMe": [Case(method="get_me", result=models.Me)],
    "getOrganization": [Case(method="get_organization", result=models.Organization)],
    "updateOrganization": [
        Case(
            method="update_organization",
            arguments={"name": "Calyx"},
            body={"name": "Calyx"},
            result=models.Organization,
        )
    ],
}
EACH_CASE = [
    pytest.param(id, case, status, id=f"{id}-{number}-{status}")
    for id, cases in CASES.items()
    for number, case in enumerate(cases)
    for status in OPERATIONS[id].statuses
]
SENDS = [CASES["sendMessage"][0], *CASES["createBulkJob"]]
each_send = pytest.mark.parametrize("case", SENDS, ids=["message", "recipients", "messages"])


class Client(Protocol):
    """Makes one client, and calls one method of it."""

    def __call__(
        self, url: str, case: Case, *, options: dict[str, Any] | None = None, **more: Any
    ) -> Any: ...


def path_arguments(case: Case) -> list[str]:
    return [] if case.path_id is None else [case.path_id]


def call_sync(url: str, case: Case, *, options: dict[str, Any] | None = None, **more: Any) -> Any:
    client = BackendClient(url, API_KEY, **(options or {}))
    return getattr(client, case.method)(*path_arguments(case), **case.arguments, **more)


def call_async(url: str, case: Case, *, options: dict[str, Any] | None = None, **more: Any) -> Any:
    client = AsyncBackendClient(url, API_KEY, **(options or {}))
    return asyncio.run(
        getattr(client, case.method)(*path_arguments(case), **case.arguments, **more)
    )


@pytest.fixture(params=[call_sync, call_async], ids=["sync", "async"])
def call(request: pytest.FixtureRequest) -> Client:
    chosen: Client = request.param
    return chosen


@pytest.fixture(params=[BackendClient, AsyncBackendClient], ids=["sync", "async"])
def kind(request: pytest.FixtureRequest) -> type[BackendClient | AsyncBackendClient]:
    chosen: type[BackendClient | AsyncBackendClient] = request.param
    return chosen


@pytest.fixture(autouse=True)
def no_open_socket() -> Iterator[None]:
    yield
    gc.collect()


def sample(schema: dict[str, Any]) -> Any:
    """Gives an answer as the backend writes it, with no null in it."""
    if "oneOf" in schema:
        return sample(schema["oneOf"][0])
    if "$ref" in schema:
        return sample(SCHEMAS[schema["$ref"].rpartition("/")[2]])
    if "enum" in schema:
        return schema["enum"][0]
    kinds = schema["type"] if isinstance(schema["type"], list) else [schema["type"]]
    match kinds[0]:
        case "object":
            return {name: sample(value) for name, value in schema["properties"].items()}
        case "array":
            return [sample(schema["items"])]
        case "integer":
            return 7
        case "number":
            return 12.5
        case "boolean":
            return True
        case _:
            return "2026-11-22T10:30:00Z" if schema.get("format") == "date-time" else "text"


def to_wire(value: Any) -> Any:
    """Gives the JSON that a result comes from."""
    if is_dataclass(value):
        return {f.name: to_wire(getattr(value, f.name)) for f in fields(value)}
    if isinstance(value, tuple):
        return [to_wire(item) for item in value]
    if isinstance(value, datetime):
        assert value.utcoffset() is not None
        return value.strftime("%Y-%m-%dT%H:%M:%SZ")
    return value


def without_tokens(answer: Any) -> Any:
    """No included operation gives the token of an API key."""
    if isinstance(answer, dict):
        return {k: without_tokens(v) for k, v in answer.items() if k != "token"}
    if isinstance(answer, list):
        return [without_tokens(item) for item in answer]
    return answer


def answering(status: int, answer: Any = None, headers: str = "") -> Callable[[Connection], None]:
    body = b"" if answer is None else json.dumps(answer).encode()

    def handle(connection: Connection) -> None:
        connection.read_request()
        connection.answer(status, body, headers)

    return handle


def url_of(server: FakeServer) -> str:
    return f"http://127.0.0.1:{server.port}"


def one_request(server: FakeServer) -> Request:
    assert (server.connections, len(server.requests)) == (1, 1)
    return server.requests[0]


def test_each_operation_has_a_case_and_a_method(
    kind: type[BackendClient | AsyncBackendClient],
) -> None:
    assert len(OPERATIONS) == 22
    assert set(CASES) == set(OPERATIONS)
    assert read_document().keys() >= EXCLUDED | {STREAM}
    methods = {name for name in dir(kind) if not name.startswith("_")}
    assert methods - {"follow_pairing"} == {c.method for cases in CASES.values() for c in cases}


@pytest.mark.parametrize(("id", "case", "status"), EACH_CASE)
def test_an_operation(call: Client, id: str, case: Case, status: int) -> None:
    expected = OPERATIONS[id]
    answer = None if expected.answer is None else sample(expected.answer)
    with FakeServer(answering(status, answer)) as server:
        options = {"idempotency_key": KEY} if expected.takes_key else {}
        result = call(url_of(server), case, **options)
        request = one_request(server)

    path = expected.path.replace("{id}", case.path_id or "no id")
    assert request.line == f"{expected.method} {path} HTTP/1.1"
    body = b"" if case.body is None else request.body
    headers = {
        "host": f"127.0.0.1:{server.port}",
        "accept-encoding": "identity",
        "user-agent": f"calyx-python-sdk/{version}",
        "authorization": f"Bearer {API_KEY}",
    }
    if case.body is not None:
        headers["content-type"] = "application/json"
        assert json.loads(body) == case.body
    if expected.method in ("POST", "PATCH"):
        headers["content-length"] = str(len(body))
    if expected.takes_key:
        headers["idempotency-key"] = KEY
    assert request.body == body
    assert {name.lower(): value for name, value in request.headers} == headers
    assert len(request.headers) == len(headers)

    if expected.takes_key:
        assert type(result) is Sent
        assert result.idempotency_key == KEY
        result = result.data
    if case.result is None:
        assert result is None
    elif case.result is int:
        assert result == status
    else:
        assert type(result) is case.result
        assert to_wire(result) == without_tokens(answer)


@pytest.mark.parametrize("id", OPERATIONS)
def test_the_time_limit_and_the_id_of_an_operation(id: str) -> None:
    operation = getattr(operations, CASES[id][0].method.upper())
    assert operation.id == id
    assert operation.time_limit == {"sendMessage": 360, "createBulkJob": 150}.get(id, 120)


@pytest.mark.parametrize("id", OPERATIONS)
def test_an_error_of_the_backend(call: Client, id: str) -> None:
    problem = {"code": "not_found", "detail": "No such record."}
    with FakeServer(answering(404, problem, "X-Request-Id: r-1\r\n")) as server:
        with pytest.raises(CalyxError) as caught:
            call(url_of(server), CASES[id][0])
        one_request(server)
    error = caught.value
    assert (error.source, error.code, error.status) == (Source.BACKEND, "not_found", 404)
    assert (error.detail, error.request_id) == ("No such record.", "r-1")
    assert (error.idempotency_key is not None) is OPERATIONS[id].takes_key


# The idempotency key.


def wire_key(request: Request) -> str:
    [key] = request.all("Idempotency-Key")
    assert uuid.UUID(key).version == 4
    assert key == str(uuid.UUID(key))
    return key


def send_answer(case: Case) -> Any:
    assert case.result is not None
    return sample(SCHEMAS[case.result.__name__])


@each_send
def test_a_send_with_no_key_makes_one(call: Client, case: Case) -> None:
    with FakeServer(answering(200, send_answer(case))) as server:
        first = call(url_of(server), case)
        assert first.idempotency_key == wire_key(one_request(server))
        second = call(url_of(server), case, idempotency_key=None)
        assert second.idempotency_key == wire_key(server.requests[1])
    assert first.idempotency_key != second.idempotency_key


@each_send
def test_a_network_error_has_the_key_that_the_sdk_made(call: Client, case: Case) -> None:
    with FakeServer(close) as server:
        with pytest.raises(CalyxError) as caught:
            call(url_of(server), case)
        assert caught.value.idempotency_key == wire_key(one_request(server))
    assert caught.value.code == ClientCode.NETWORK_ERROR
    assert caught.value.retry is RetryAdvice.RETRY_NOW


def close(connection: Connection) -> None:
    connection.read_request()
    connection.sock.close()


@each_send
def test_a_backend_error_has_the_key_that_the_sdk_made(call: Client, case: Case) -> None:
    problem = {"code": "send_in_progress", "detail": "Wait."}
    with FakeServer(answering(409, problem, "Retry-After: 5\r\n")) as server:
        with pytest.raises(CalyxError) as caught:
            call(url_of(server), case)
        assert caught.value.idempotency_key == wire_key(one_request(server))
    assert (caught.value.retry, caught.value.retry_after) == (RetryAdvice.RETRY_LATER, 5)


@each_send
def test_a_bad_argument_has_the_key_that_the_sdk_made(call: Client, case: Case) -> None:
    with FakeServer(answering(200)) as server:
        with pytest.raises(CalyxError) as caught:
            call(url_of(server), case, time_limit=0)
        assert server.connections == 0
    assert caught.value.code == ClientCode.INVALID_CONFIG
    assert uuid.UUID(caught.value.idempotency_key).version == 4


@each_send
@pytest.mark.parametrize("key", ["", "with space", uuid.uuid4()])
def test_a_bad_key_sends_nothing(call: Client, case: Case, key: Any) -> None:
    with FakeServer(answering(200)) as server:
        with pytest.raises(CalyxError) as caught:
            call(url_of(server), case, idempotency_key=key)
        assert server.connections == 0
    assert (caught.value.code, caught.value.idempotency_key) == (ClientCode.INVALID_CONFIG, None)


@pytest.mark.parametrize("id", ["createClient", "createInstance"])
def test_a_create_call_with_no_answer(call: Client, id: str) -> None:
    with FakeServer(close) as server, pytest.raises(CalyxError) as caught:
        call(url_of(server), CASES[id][0])
    assert caught.value.code == ClientCode.NETWORK_ERROR
    assert caught.value.retry is RetryAdvice.UNKNOWN


# The constructor.


@pytest.mark.parametrize(
    "arguments",
    [
        {"base_url": "ftp://example.com"},
        {"base_url": "https://user:cx_secret@example.com"},
        {"api_key": "cx secret"},
        {"time_limit": 0},
    ],
)
def test_the_constructor_checks_its_arguments(
    kind: type[BackendClient | AsyncBackendClient], arguments: dict[str, Any]
) -> None:
    with pytest.raises(CalyxError) as caught:
        kind(**{"base_url": "https://example.com", **arguments})
    assert (caught.value.source, caught.value.code) == (Source.CLIENT, ClientCode.INVALID_CONFIG)
    assert "secret" not in f"{caught.value!r} {caught.value.detail}"


def test_a_client_shows_no_key(kind: type[BackendClient | AsyncBackendClient]) -> None:
    client = kind("https://example.com", API_KEY)
    assert API_KEY not in f"{client!r} {client} {vars(client)}"


def test_a_client_has_nothing_to_close(kind: type[BackendClient | AsyncBackendClient]) -> None:
    for name in ("close", "aclose", "__enter__", "__exit__", "__aenter__", "__aexit__", "__del__"):
        assert not hasattr(kind, name)


def test_a_call_changes_nothing_in_the_client() -> None:
    with FakeServer(answering(200, sample(OPERATIONS["getMe"].answer or {}))) as server:
        client = BackendClient(url_of(server), API_KEY)
        before = dict(vars(client))
        client.get_me()
        assert vars(client) == before
        async_client = AsyncBackendClient(url_of(server), API_KEY)
        before = dict(vars(async_client))
        asyncio.run(async_client.get_me())
        assert vars(async_client) == before


@pytest.mark.parametrize(
    ("client_limit", "call_limit"), [(0.2, None), (5, 0.2), (None, 0.2)], ids=str
)
def test_the_time_limits(
    call: Client, client_limit: float | None, call_limit: float | None
) -> None:
    with FakeServer(wait) as server:
        start = time.monotonic()
        with pytest.raises(CalyxError) as caught:
            options = {"time_limit": client_limit}
            call(url_of(server), CASES["getMe"][0], options=options, time_limit=call_limit)
        assert 0.15 < time.monotonic() - start < 2
        server.call_ended.set()
    assert caught.value.code == ClientCode.TIMEOUT


def wait(connection: Connection) -> None:
    connection.read_request()
    connection.wait()


def test_ca_file(call: Client, tmp_path: Path) -> None:
    authority = certificates.make_authority(tmp_path / "ca")
    answer = sample(OPERATIONS["getMe"].answer or {})
    context = authority.server_context("DNS:localhost")
    with FakeServer(answering(200, answer), tls=context) as server:
        url = f"https://localhost:{server.port}"
        with pytest.raises(CalyxError) as caught:
            call(url, CASES["getMe"][0])
        assert caught.value.code == ClientCode.NETWORK_ERROR
        options = {"ca_file": str(authority.ca_file)}
        assert type(call(url, CASES["getMe"][0], options=options)) is models.Me


# One client in many places.


def test_one_client_in_two_event_loops(tmp_path: Path) -> None:
    authority = certificates.make_authority(tmp_path / "ca")
    answer = sample(OPERATIONS["getMe"].answer or {})
    context = authority.server_context("DNS:localhost")
    with FakeServer(answering(200, answer), tls=context) as server:
        client = AsyncBackendClient(
            f"https://localhost:{server.port}", ca_file=str(authority.ca_file)
        )
        for _ in range(2):
            assert to_wire(asyncio.run(client.get_me())) == answer
        assert server.connections == 2


FORK = """
import asyncio, os, sys
from calyx_client_sdk import AsyncBackendClient, BackendClient

url, ca_file = sys.argv[1:]
client = BackendClient(url, ca_file=ca_file)
async_client = AsyncBackendClient(url, ca_file=ca_file)

def call():
    return client.get_me(), asyncio.run(async_client.get_me())

before = call()
child = os.fork()
if child == 0:
    os._exit(0 if call() == before else 7)
assert call() == before
_, status = os.waitpid(child, 0)
assert os.waitstatus_to_exitcode(status) == 0
"""


# In another process: this one has the threads of the fake server, and Python warns about a fork
# with threads.
def test_one_client_in_a_forked_process(tmp_path: Path) -> None:
    authority = certificates.make_authority(tmp_path / "ca")
    answer = sample(OPERATIONS["getMe"].answer or {})
    context = authority.server_context("DNS:localhost")
    with FakeServer(answering(200, answer), tls=context) as server:
        arguments = [f"https://localhost:{server.port}", str(authority.ca_file)]
        done = subprocess.run(
            [sys.executable, "-W", "error", "-c", FORK, *arguments],
            capture_output=True,
            text=True,
            timeout=8,
        )
        assert (done.returncode, done.stderr) == (0, "")
        assert server.connections == 6
