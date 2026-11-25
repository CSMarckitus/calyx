import copy
import json
import pickle
import traceback
from collections.abc import Callable, Iterator
from contextlib import contextmanager
from dataclasses import FrozenInstanceError
from pathlib import Path
from typing import Any, cast

import pytest

import calyx_client_sdk
from calyx_client_sdk import (
    BackendCode,
    CalyxError,
    ClientCode,
    CoreCode,
    FieldError,
    RetryAdvice,
    Source,
)
from calyx_client_sdk._generated.advice import ADVICE

TESTDATA = Path(__file__).parents[3] / "testdata"
ROWS = json.loads((TESTDATA / "retry-advice.json").read_text())["rows"]
FIELDS = (
    "source",
    "code",
    "status",
    "detail",
    "retry",
    "errors",
    "request_id",
    "retry_after",
    "idempotency_key",
    "metadata",
)


def full_error() -> CalyxError:
    # RETRY_NOW is not the advice of this code, so a test sees when the value is lost.
    return CalyxError(
        source=Source.BACKEND,
        code=BackendCode.INVALID_REQUEST,
        status=422,
        detail="The request is not valid.",
        retry=RetryAdvice.RETRY_NOW,
        errors=[FieldError(location="body.text", message="expected length >= 1")],
        request_id="req-1",
        retry_after=7,
        idempotency_key="key-1",
        metadata={"at": "2026-11-22T10:00:00Z"},
    )


def small_error() -> CalyxError:
    return CalyxError(
        source=Source.CLIENT, code=ClientCode.TIMEOUT, status=0, detail="The time limit passed."
    )


def fields(error: CalyxError) -> dict[str, object]:
    return {name: getattr(error, name) for name in FIELDS}


@pytest.mark.parametrize("row", ROWS, ids=lambda row: f"{row['source']}-{row['code']}")
def test_the_advice_of_a_code_is_the_one_of_retry_advice_json(row: dict[str, str]) -> None:
    error = CalyxError(source=Source(row["source"]), code=row["code"], status=0, detail="")
    assert error.retry is RetryAdvice(row["retry"])


def test_the_map_has_no_code_that_retry_advice_json_does_not_have() -> None:
    assert dict(ADVICE) == {(row["source"], row["code"]): row["retry"] for row in ROWS}


@pytest.mark.parametrize(
    ("source", "constants"),
    [(Source.BACKEND, BackendCode), (Source.CORE, CoreCode), (Source.CLIENT, ClientCode)],
)
def test_the_code_constants_are_the_codes_of_retry_advice_json(
    source: Source, constants: type
) -> None:
    values = {value for name, value in vars(constants).items() if name.isupper()}
    assert values == {row["code"] for row in ROWS if row["source"] == source}


def test_the_advice_and_source_values_are_those_of_the_contract() -> None:
    assert {advice.name: advice.value for advice in RetryAdvice} == {
        "RETRY_NOW": "retry_now",
        "RETRY_LATER": "retry_later",
        "CHECK_IF_SENT": "check_if_sent",
        "DO_NOT_RETRY": "do_not_retry",
        "UNKNOWN": "unknown",
    }
    assert [source.value for source in Source] == ["backend", "core", "client"]


@pytest.mark.parametrize(
    ("source", "code"),
    [
        (Source.BACKEND, "a_new_code"),
        # A known code with another source is an unknown pair.
        (Source.CORE, ClientCode.TIMEOUT),
    ],
)
def test_an_unknown_pair_of_source_and_code_has_the_advice_unknown(
    source: Source, code: str
) -> None:
    error = CalyxError(source=source, code=code, status=500, detail="")
    assert error.code == code
    assert error.retry is RetryAdvice.UNKNOWN


def test_the_error_keeps_each_field() -> None:
    assert fields(full_error()) == {
        "source": Source.BACKEND,
        "code": "invalid_request",
        "status": 422,
        "detail": "The request is not valid.",
        "retry": RetryAdvice.RETRY_NOW,
        "errors": (FieldError(location="body.text", message="expected length >= 1"),),
        "request_id": "req-1",
        "retry_after": 7,
        "idempotency_key": "key-1",
        "metadata": {"at": "2026-11-22T10:00:00Z"},
    }


def test_the_optional_fields_have_a_default() -> None:
    assert fields(small_error()) == {
        "source": Source.CLIENT,
        "code": "timeout",
        "status": 0,
        "detail": "The time limit passed.",
        "retry": RetryAdvice.RETRY_NOW,
        "errors": (),
        "request_id": None,
        "retry_after": 0,
        "idempotency_key": None,
        "metadata": {},
    }


def test_a_later_change_of_the_arguments_does_not_change_the_error() -> None:
    errors = [FieldError(location="body", message="first")]
    metadata = {"at": "first"}
    error = CalyxError(
        source=Source.CORE,
        code="UNKNOWN",
        status=2,
        detail="",
        errors=errors,
        metadata=metadata,
    )
    errors.clear()
    metadata["at"] = "second"
    assert error.errors == (FieldError(location="body", message="first"),)
    assert error.metadata == {"at": "first"}


def test_a_field_error_is_read_only() -> None:
    error = FieldError(location="body.text", message="expected length >= 1")
    with pytest.raises(FrozenInstanceError):
        error.message = "other"  # type: ignore[misc]


def test_str_gives_the_code_the_status_the_request_id_and_the_key() -> None:
    assert str(full_error()) == (
        "invalid_request (status 422, request id req-1, idempotency key key-1)"
    )


@pytest.mark.parametrize("source", [Source.BACKEND, Source.CORE])
def test_str_has_no_detail_of_a_server(source: Source) -> None:
    error = CalyxError(source=source, code="new", status=5, detail="The text of a server.")
    assert str(error) == "new (status 5)"


def test_str_also_gives_the_detail_of_the_sdk() -> None:
    assert str(small_error()) == "timeout (status 0): The time limit passed."
    error = CalyxError(
        source=Source.CLIENT,
        code=ClientCode.TIMEOUT,
        status=0,
        detail="The time limit passed.",
        idempotency_key="key-1",
    )
    assert str(error) == "timeout (status 0, idempotency key key-1): The time limit passed."
    assert str(error) in "".join(traceback.format_exception_only(error))


def test_str_of_an_error_of_the_sdk_with_no_detail() -> None:
    error = CalyxError(source=Source.CLIENT, code=ClientCode.TIMEOUT, status=0, detail="")
    assert str(error) == "timeout (status 0)"


def test_repr_also_gives_the_source_and_the_advice() -> None:
    assert repr(full_error()) == (
        "<CalyxError backend: invalid_request"
        " (status 422, request id req-1, idempotency key key-1), retry_now>"
    )
    assert repr(small_error()) == "<CalyxError client: timeout (status 0), retry_now>"


@pytest.mark.parametrize("source", [Source.BACKEND, Source.CORE])
def test_str_and_repr_of_a_server_error_show_only_the_identifiers(source: Source) -> None:
    error = CalyxError(
        source=source,
        code=BackendCode.INVALID_REQUEST,
        status=422,
        detail="SECRET",
        errors=[FieldError(location="SECRET", message="SECRET")],
        metadata={"SECRET": "SECRET"},
    )
    error.__cause__ = ValueError("SECRET")
    assert "SECRET" not in str(error)
    assert "SECRET" not in repr(error)
    assert "SECRET" not in "".join(traceback.format_exception_only(error))


@pytest.mark.parametrize("make", [full_error, small_error])
@pytest.mark.parametrize(
    "clone",
    [lambda error: pickle.loads(pickle.dumps(error)), copy.copy, copy.deepcopy],
    ids=["pickle", "copy", "deepcopy"],
)
def test_pickle_and_copy_keep_each_field(
    make: Callable[[], CalyxError], clone: Callable[[CalyxError], CalyxError]
) -> None:
    error = make()
    result = clone(error)
    assert type(result) is CalyxError
    assert result is not error
    assert fields(result) == fields(error)
    assert result.args == error.args
    assert str(result) == str(error)


def test_pickle_leaves_the_cause_out() -> None:
    error = small_error()
    # A lambda cannot be pickled, as many errors of an HTTP library.
    error.__cause__ = ValueError(lambda: None)
    assert pickle.loads(pickle.dumps(error)).__cause__ is None


@contextmanager
def pass_through() -> Iterator[None]:
    yield


def test_raise_sets_the_cause_the_traceback_and_the_notes() -> None:
    cause = ConnectionResetError("reset")
    with pytest.raises(CalyxError) as caught:
        # contextlib sets __traceback__ with a normal assignment.
        with pass_through():
            try:
                raise small_error() from cause
            except CalyxError as error:
                error.add_note("a note")
                raise
    assert caught.value.__cause__ is cause
    assert caught.value.__traceback__ is not None
    assert caught.value.__notes__ == ["a note"]


def test_the_public_names_of_the_errors() -> None:
    assert set(calyx_client_sdk.__all__) >= {
        "BackendCode",
        "CalyxError",
        "ClientCode",
        "CoreCode",
        "FieldError",
        "RetryAdvice",
        "Source",
    }


def test_the_constructor_takes_keywords_only() -> None:
    with pytest.raises(TypeError):
        cast(Any, CalyxError)("x")
