"""Checks each generated model against backend.json, with one sample for each schema."""

import copy
import inspect
import json
import pickle
from dataclasses import MISSING, FrozenInstanceError, asdict, fields, is_dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

import pytest

from calyx_client_sdk import PairingUpdate, models
from calyx_client_sdk._fields import WrongShapeError, read, to_json
from calyx_client_sdk._generated import models as generated

BACKEND_JSON = Path(__file__).parents[4] / "shared-types" / "openapi" / "backend.json"
SCHEMAS: dict[str, Any] = json.loads(BACKEND_JSON.read_text())["components"]["schemas"]
DATE = datetime(2026, 11, 22, 10, 30, 0, 250000, tzinfo=UTC)
WIRE_DATE = "2026-11-22T10:30:00.25Z"
INPUTS = (
    models.BulkMessageInput,
    models.BulkPacingInput,
    models.MetaCredentialsInput,
    models.RangeInput,
    models.WebhookInput,
)
PUBLIC: list[type[Any]] = [getattr(models, name) for name in models.__all__]
MODELS = [c for c in PUBLIC if is_dataclass(c)]
CONSTANTS = [c for c in PUBLIC if not is_dataclass(c)]
each_model = pytest.mark.parametrize("model", MODELS, ids=lambda c: c.__name__)


def schema_of(model: type[Any]) -> dict[str, Any]:
    return SCHEMAS["Client" if model is models.OrgClient else model.__name__]


def properties(model: type[Any]) -> dict[str, Any]:
    found = dict(schema_of(model)["properties"])
    if model is models.ApiKey:
        del found["token"]
    return found


def is_nullable(schema: dict[str, Any]) -> bool:
    return "null" in schema.get("type", []) or {"type": "null"} in schema.get("oneOf", [])


def sample(schema: dict[str, Any]) -> Any:
    """Gives a value as the backend writes it, with no null in it."""
    if "oneOf" in schema:
        return sample(schema["oneOf"][0])
    if "$ref" in schema:
        return sample(SCHEMAS[schema["$ref"].rpartition("/")[2]])
    if "enum" in schema:
        return schema["enum"][0]
    kinds = schema["type"] if isinstance(schema["type"], list) else [schema["type"]]
    match kinds[0]:
        case "object":
            return {name: sample(s) for name, s in schema["properties"].items()}
        case "array":
            return [sample(schema["items"])]
        case "string":
            return WIRE_DATE if schema.get("format") == "date-time" else "text"
        case "integer":
            return 7
        case "number":
            return 1.5
        case "boolean":
            return True
        case kind:
            raise AssertionError(kind)


def kind_of(schema: dict[str, Any]) -> str:
    """Gives the JSON type of a property, nullable or not. Gives "other" for a ref or an enum."""
    if "oneOf" in schema:
        return kind_of(schema["oneOf"][0])
    kinds = schema.get("type", "other")
    return next(k for k in kinds if k != "null") if isinstance(kinds, list) else kinds


def sample_of(model: type[Any]) -> dict[str, Any]:
    return {name: sample(schema) for name, schema in properties(model).items()}


def python(value: Any) -> Any:
    """Turns a JSON sample into the value of asdict(), without the omitted ApiKey.token."""
    if isinstance(value, dict):
        token = "prefix" in value
        return {k: python(v) for k, v in value.items() if not (token and k == "token")}
    if isinstance(value, list):
        return tuple(python(item) for item in value)
    return DATE if value == WIRE_DATE else value


def test_each_generated_class_is_public() -> None:
    made = [c for c in vars(generated).values() if inspect.isclass(c)]
    names = [c.__name__ for c in made if c.__module__ == generated.__name__]
    assert sorted(names) == sorted(models.__all__)
    for name in names:
        assert getattr(models, name) is getattr(generated, name)


def test_the_classes_that_are_not_public() -> None:
    assert not {"Client", "Problem", "ProblemField", "Done", "Failed"} & set(models.__all__)
    assert not [name for name in models.__all__ if name.endswith("Request")]


@each_model
def test_the_fields_are_the_properties(model: type[Any]) -> None:
    assert {f.name for f in fields(model)} == set(properties(model))


@each_model
def test_a_sample_becomes_an_object(model: type[Any]) -> None:
    data = sample_of(model)
    assert asdict(read(model, data)) == python(data)


@each_model
def test_an_unknown_field_is_ignored(model: type[Any]) -> None:
    data = sample_of(model)
    assert read(model, data | {"new": {"x": 1}}) == read(model, data)


@each_model
def test_a_field_can_be_absent_only_when_it_is_not_required(model: type[Any]) -> None:
    for name in properties(model):
        data = sample_of(model)
        del data[name]
        if name in schema_of(model).get("required", []):
            with pytest.raises(WrongShapeError, match=f"^{model.__name__}.{name} is missing"):
                read(model, data)
        else:
            assert getattr(read(model, data), name) is None


@each_model
def test_null(model: type[Any]) -> None:
    for name, schema in properties(model).items():
        data = sample_of(model) | {name: None}
        if name in schema_of(model).get("required", []) and not is_nullable(schema):
            with pytest.raises(WrongShapeError, match=f"^{model.__name__}.{name} is not"):
                read(model, data)
        else:
            assert getattr(read(model, data), name) is None


@each_model
def test_a_wrong_type_at_each_field(model: type[Any]) -> None:
    for name, schema in properties(model).items():
        wrong = 5 if kind_of(schema) == "string" else "text"
        with pytest.raises(WrongShapeError, match=f"^{model.__name__}.{name} is not"):
            read(model, sample_of(model) | {name: wrong})


@each_model
def test_a_number_keeps_its_python_type(model: type[Any]) -> None:
    for name, schema in properties(model).items():
        want = {"integer": int, "number": float}.get(kind_of(schema))
        if want:
            assert type(getattr(read(model, sample_of(model)), name)) is want


@each_model
def test_an_object_is_read_only_and_works_with_pickle_and_copy(model: type[Any]) -> None:
    value = read(model, sample_of(model))
    with pytest.raises(FrozenInstanceError):
        setattr(value, fields(model)[0].name, "other")
    assert not hasattr(value, "__dict__")
    assert pickle.loads(pickle.dumps(value)) == value
    assert copy.deepcopy(value) == value


@each_model
def test_the_constructor_takes_keywords_only(model: type[Any]) -> None:
    parameters = inspect.signature(model).parameters.values()
    assert {p.kind for p in parameters} == {inspect.Parameter.KEYWORD_ONLY}
    required = set(schema_of(model).get("required", []))
    assert {f.name for f in fields(model) if f.default is MISSING} == required


@pytest.mark.parametrize("model", INPUTS, ids=lambda c: c.__name__)
def test_a_request_object_becomes_the_json_of_the_schema(model: type[Any]) -> None:
    data = sample_of(model)
    assert to_json(read(model, data)) == data
    required = {n: v for n, v in data.items() if n in schema_of(model).get("required", [])}
    assert to_json(read(model, required)) == required


def test_an_unknown_enum_value_stays() -> None:
    data = sample_of(models.Instance) | {"status": "NEW_STATUS", "provider": "NEW_PROVIDER"}
    instance = read(models.Instance, data)
    assert instance.status == "NEW_STATUS"
    assert instance.provider == "NEW_PROVIDER"


@pytest.mark.parametrize(
    ("model", "field"),
    [
        (models.Instance, "provider"),
        (models.Instance, "state"),
        (models.Instance, "status"),
        (models.Pairing, "status"),
        (models.BulkJob, "status"),
    ],
)
def test_each_enum_value_of_an_answer_has_a_constant(model: type[Any], field: str) -> None:
    known = {value for c in CONSTANTS for value in vars(c).values() if isinstance(value, str)}
    assert known >= {value for value in properties(model)[field]["enum"] if value is not None}


def test_the_constants_are_the_values_of_the_wire() -> None:
    assert {c.__name__ for c in CONSTANTS} == {
        "Provider",
        "DeviceIdentity",
        "InstanceStatus",
        "InstanceState",
        "BulkJobStatus",
        "PairingFailureCode",
    }
    assert models.Provider.META_CLOUD == "META_CLOUD"
    assert models.DeviceIdentity.SAFARI_MAC_OS == "SAFARI_MAC_OS"
    assert models.InstanceStatus.CONNECTED == "CONNECTED"
    assert models.InstanceState.REMOVING == "removing"
    assert models.BulkJobStatus.RUNNING == "RUNNING"
    assert models.PairingFailureCode.PAIRING_CANCELLED == "pairing_cancelled"


@pytest.mark.parametrize(
    ("model", "secrets"),
    [
        (models.WebhookInput, {"secret"}),
        (models.MetaCredentialsInput, {"access_token", "app_secret"}),
        (models.MetaWebhook, {"verify_token"}),
        (models.Pairing, {"qr_text", "pairing_code"}),
    ],
)
def test_the_repr_has_no_secret(model: type[Any], secrets: set[str]) -> None:
    data: dict[str, Any] = {name: f"value of {name}" for name in properties(model)}
    text = repr(read(model, data))
    for name, value in data.items():
        assert (value in text) is (name not in secrets)


def test_the_repr_of_an_outer_object_has_no_secret() -> None:
    data = sample_of(models.Organization)
    data["meta_webhook"]["verify_token"] = "private"
    assert "private" not in repr(read(models.Organization, data))

    pairing = sample_of(models.Pairing) | {"qr_text": "private", "pairing_code": "private"}
    result = read(models.PairResult, sample_of(models.PairResult) | {"pairing": pairing})
    assert "private" not in repr(result)
    assert "private" not in repr(PairingUpdate(pairing=result.pairing))


@pytest.mark.parametrize("model", PUBLIC, ids=lambda c: c.__name__)
def test_each_class_has_a_text(model: type[Any]) -> None:
    text = " ".join((inspect.getdoc(model) or "").split())
    assert text.endswith(".")
    if is_dataclass(model):
        assert text == schema_of(model)["description"]
