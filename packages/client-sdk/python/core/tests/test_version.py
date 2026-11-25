from importlib.metadata import files, requires, version

import calyx_client_sdk
import pytest

import calyx_client_sdk_core


def test_version_is_the_version_of_the_distribution() -> None:
    assert calyx_client_sdk_core.__version__ == version("calyx-client-sdk-core")


def test_both_packages_have_the_same_version() -> None:
    assert calyx_client_sdk_core.__version__ == calyx_client_sdk.__version__


def test_core_requires_exactly_the_same_version_of_the_backend_package() -> None:
    wanted = f"calyx-client-sdk=={calyx_client_sdk_core.__version__}"
    assert wanted in (requires("calyx-client-sdk-core") or [])


@pytest.mark.parametrize("name", ["calyx-client-sdk", "calyx-client-sdk-core"])
def test_wheel_has_no_other_top_level_name(name: str) -> None:
    package = name.replace("-", "_")
    top = {path.parts[0] for path in files(name) or []}
    assert top == {package, f"{package}-{version(name)}.dist-info"}
