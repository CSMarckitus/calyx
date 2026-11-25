from importlib.metadata import version

import calyx_client_sdk


def test_version_is_the_version_of_the_distribution() -> None:
    assert calyx_client_sdk.__version__ == version("calyx-client-sdk")
