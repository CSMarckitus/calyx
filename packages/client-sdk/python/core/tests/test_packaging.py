"""Installs both wheels in clean environments, with the lowest and the highest versions of gRPC and
protobuf. It needs the network and more time than another test: run.sh test-core runs it alone."""

import re
import subprocess
from importlib.metadata import requires
from pathlib import Path

import pytest
from fake_core import EVENT, TOKEN, FakeCall, FakeCore

# run.sh builds the wheels there.
WHEELS = sorted(Path("/tmp/dist").glob("*.whl"))

# The package `calyx` of PyPI is not ours, and it has the proto package name as its top-level name.
# The hash makes sure that the code of another person does not change.
CALYX = (
    "calyx @ https://files.pythonhosted.org/packages/ac/21/"
    "ac01734c65768ea54b0a0c08e325b5820c86c7a7c9cc6c9ce7cb6d718faf/calyx-1.0.tar.gz"
    "#sha256=d2e7a929989adbec026e2de33b90e5c26db4a8fd692a5a9391d625bed8e852a1"
)

LOWEST = {"grpcio": "1.80.0", "protobuf": "6.33.5", "googleapis-common-protos": "1.76.0"}

PROGRAM = """
import asyncio, sys

import calyx
import calyx_client_sdk
import calyx_client_sdk_core
from calyx_client_sdk_core import AsyncCoreClient, CoreClient

address, token = sys.argv[1:]


async def main():
    async with AsyncCoreClient(address, token, insecure=True) as client:
        assert (await client.list_instances()).instances == ()
        async with client.subscribe() as stream:
            print((await anext(stream)).id)


with CoreClient(address, token, insecure=True) as client:
    assert client.list_instances().instances == ()
    with client.subscribe() as stream:
        print(next(stream).id)
asyncio.run(main())

assert "httpcore2" not in sys.modules
"""


def run(*command: str) -> str:
    done = subprocess.run(command, capture_output=True, text=True, check=False)
    assert (done.returncode, done.stderr) == (0, ""), done.stderr
    return done.stdout


def answer(seen: FakeCall) -> bytes:
    return EVENT.SerializeToString() if "Subscribe" in seen.path else b""


def as_numbers(version: str) -> tuple[int, ...]:
    return tuple(int(part) for part in version.split("."))


def test_the_lowest_versions_are_the_ones_of_the_package() -> None:
    texts = requires("calyx-client-sdk-core") or []
    found = [re.fullmatch(r"([\w-]+)\S*>=([\d.]+)", text) for text in texts]
    lowest = {match[1]: as_numbers(match[2]) for match in found if match}
    # 1.80 is the same version as 1.80.0.
    assert {name: (*numbers, 0)[:3] for name, numbers in lowest.items()} == {
        name: as_numbers(version) for name, version in LOWEST.items()
    }


@pytest.mark.parametrize("versions", ["lowest", "highest"])
def test_both_wheels_in_a_clean_environment(versions: str, tmp_path: Path) -> None:
    assert len(WHEELS) == 2
    python = str(tmp_path / "bin" / "python")
    install = ["uv", "pip", "install", "--quiet", "--python", python]
    run("uv", "venv", "--quiet", "--python", "3.12", str(tmp_path))
    # Without pins, uv takes the newest version of each range.
    pins = [f"{n}=={v}" for n, v in LOWEST.items()] if versions == "lowest" else []
    run(*install, *map(str, WHEELS), *pins, CALYX)

    lines = run("uv", "pip", "freeze", "--quiet", "--python", python).splitlines()
    installed = dict(line.split("==") for line in lines if "==" in line)
    # run.sh shows it: the newest versions change with time.
    print("", *(f"{name} {installed[name]}" for name in LOWEST), sep="\n")
    if versions == "lowest":
        assert installed.items() >= LOWEST.items()
    else:
        # The newest googleapis-common-protos can be the lowest one.
        assert installed["grpcio"] != LOWEST["grpcio"]
        assert installed["protobuf"].startswith("7.")

    with FakeCore(answer) as core:
        # A warning is an error. Without -I, Python would also look in the folder of the tests.
        output = run(python, "-I", "-W", "error", "-c", PROGRAM, core.address, TOKEN)
    assert output == "evt-1\nevt-1\n"
    assert [call.path.rpartition("/")[2] for call in core.calls] == [
        "ListInstances",
        "Subscribe",
    ] * 2
