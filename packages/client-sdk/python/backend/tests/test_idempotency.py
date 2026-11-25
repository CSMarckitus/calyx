import copy
import pickle
import uuid
from dataclasses import FrozenInstanceError
from typing import Any

import pytest

import calyx_client_sdk
from calyx_client_sdk import CalyxError, ClientCode, RetryAdvice, Sent, Source, new_idempotency_key
from calyx_client_sdk.idempotency import resolve_idempotency_key


def test_a_new_key_is_a_uuid_version_4() -> None:
    key = new_idempotency_key()
    parsed = uuid.UUID(key)
    assert parsed.version == 4
    assert str(parsed) == key


def test_each_new_key_is_different() -> None:
    assert len({new_idempotency_key() for _ in range(100)}) == 100


def test_without_a_key_the_sdk_makes_one() -> None:
    assert uuid.UUID(resolve_idempotency_key(None)).version == 4


@pytest.mark.parametrize(
    "key",
    ["!", "~", "k" * 255, "".join(chr(code) for code in range(33, 127)), "order-1"],
)
def test_a_good_key_stays_as_it_is(key: str) -> None:
    assert resolve_idempotency_key(key) == key


@pytest.mark.parametrize(
    "key",
    [
        "",
        "k" * 256,
        "two words",
        " order-1",
        "key\n",
        "key\r\nX-Other: 1",
        "key\x7f",
        "key\x00",
        "clé",
        "١",
        pytest.param(b"", id="empty bytes"),
        pytest.param(b"order-1", id="bytes"),
        uuid.UUID(int=0),
        5,
        False,
    ],
)
def test_a_bad_key_gives_invalid_config(key: Any) -> None:
    with pytest.raises(CalyxError) as caught:
        resolve_idempotency_key(key)
    error = caught.value
    assert error.source is Source.CLIENT
    assert error.code == ClientCode.INVALID_CONFIG
    assert error.status == 0
    assert error.retry is RetryAdvice.DO_NOT_RETRY
    # The program can give a wrong value here, for example its API key.
    assert error.idempotency_key is None
    # 5 is also in "255" of the text.
    assert key in ("", 5) or str(key) not in error.detail


def test_the_detail_of_an_empty_key_tells_about_a_lost_key() -> None:
    with pytest.raises(CalyxError) as caught:
        resolve_idempotency_key("")
    assert "lost its stored key" in caught.value.detail


def test_sent_has_the_answer_and_the_key() -> None:
    sent = Sent(data={"message_id": "m1"}, idempotency_key="key-1")
    assert sent.data == {"message_id": "m1"}
    assert sent.idempotency_key == "key-1"
    assert sent == Sent[dict[str, str]](data={"message_id": "m1"}, idempotency_key="key-1")


def test_sent_is_read_only() -> None:
    sent = Sent(data="answer", idempotency_key="key-1")
    with pytest.raises(FrozenInstanceError):
        sent.idempotency_key = "key-2"  # type: ignore[misc]


def test_sent_works_with_pickle_and_copy() -> None:
    sent = Sent(data="answer", idempotency_key="key-1")
    assert pickle.loads(pickle.dumps(sent)) == sent
    assert copy.copy(sent) == sent


def test_the_public_names_of_the_keys() -> None:
    assert set(calyx_client_sdk.__all__) >= {"Sent", "new_idempotency_key"}
    assert "resolve_idempotency_key" not in calyx_client_sdk.__all__
