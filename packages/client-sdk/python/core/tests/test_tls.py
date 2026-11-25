"""The certificates of the core clients."""

import os
import shutil
import subprocess
from pathlib import Path
from typing import Any

import pytest
from calyx_client_sdk import CalyxError, CoreCode, Source
from certificates import Authority
from fake_core import KEY, SEND, TOKEN, Call, FakeCore, answer_empty, check_config_error

from calyx_client_sdk_core import CoreClient


def test_ca_file_accepts_a_private_ca(call: Call, authority: Authority) -> None:
    files = authority.server_files("DNS:localhost")
    with FakeCore(answer_empty, files=files) as core:
        call(f"localhost:{core.port}", "list_instances", {"ca_file": authority.ca_file})
        (seen,) = core.calls
    assert seen.metadata["authorization"] == f"Bearer {TOKEN}"


@pytest.mark.parametrize(("host", "name"), [("127.0.0.1", "127.0.0.1"), ("[::1]", "::1")])
def test_tls_to_an_ip_address(call: Call, authority: Authority, host: str, name: str) -> None:
    with FakeCore(answer_empty, files=authority.server_files(f"IP:{name}"), host=host) as core:
        call(core.address, "list_instances", {"ca_file": authority.ca_file})


def test_a_certificate_of_another_host_is_refused(call: Call, authority: Authority) -> None:
    with FakeCore(answer_empty, files=authority.server_files("DNS:other.test")) as core:
        with pytest.raises(CalyxError) as caught:
            call(f"localhost:{core.port}", "list_instances", {"ca_file": authority.ca_file})
        assert core.calls == []
    assert (caught.value.source, caught.value.code) == (Source.CORE, CoreCode.UNAVAILABLE)


def test_tls_is_the_default(call: Call, authority: Authority) -> None:
    for files in (authority.server_files("DNS:localhost"), None):
        with FakeCore(answer_empty, files=files) as core:
            with pytest.raises(CalyxError) as caught:
                call(f"localhost:{core.port}", "list_instances", {"time_limit": 2})
            assert core.calls == []
        assert (caught.value.source, caught.value.code) == (Source.CORE, CoreCode.UNAVAILABLE)


def test_ca_file_adds_to_the_system_certificates(
    call: Call, authority: Authority, other_authority: Authority, monkeypatch: pytest.MonkeyPatch
) -> None:
    # With this variable, the file replaces the system certificates.
    monkeypatch.setenv("SSL_CERT_FILE", authority.ca_file)
    for signer, ca_file in [
        (authority, None),
        (authority, other_authority.ca_file),
        (other_authority, other_authority.ca_file),
    ]:
        with FakeCore(answer_empty, files=signer.server_files("IP:127.0.0.1")) as core:
            call(core.address, "list_instances", {"ca_file": ca_file})


def test_ca_file_can_be_the_certificate_of_the_server(call: Call, authority: Authority) -> None:
    files = authority.server_files("IP:127.0.0.1")
    with FakeCore(answer_empty, files=files) as core:
        call(core.address, "list_instances", {"ca_file": str(files[0])})


@pytest.mark.parametrize("bad", ["missing", "folder", "text", "key", "empty", "null", "number"])
def test_a_ca_file_that_cannot_be_loaded(
    call: Call, authority: Authority, tmp_path: Path, bad: str
) -> None:
    text = tmp_path / "text.pem"
    text.write_text("no certificate")
    ca_file: Any = {
        "missing": str(tmp_path / "nope.pem"),
        "folder": str(tmp_path),
        "text": str(text),
        "key": str(authority.folder / "ca.key"),
        "empty": "",
        "null": "a\0b",
        "number": 5,
    }[bad]
    # The client reads the file at the first call.
    CoreClient("localhost:50051", TOKEN, ca_file=ca_file)
    with pytest.raises(CalyxError) as caught:
        call("localhost:50051", "send_plain_text", {"ca_file": ca_file}, **SEND)
    check_config_error(caught.value, caught.value.idempotency_key)
    assert caught.value.idempotency_key
    assert "ca_file" in caught.value.detail
    assert caught.value.__cause__ is not None


# The folder of certificates of OpenSSL (SSL_CERT_DIR).


def only_folders(monkeypatch: pytest.MonkeyPatch, *folders: Path) -> None:
    """The system has no file with certificates, and only these folders."""
    monkeypatch.setenv("SSL_CERT_FILE", "/nope.pem")
    monkeypatch.setenv("SSL_CERT_DIR", os.pathsep.join(map(str, folders)))


def make_folder(folder: Path, *authorities: Authority) -> Path:
    folder.mkdir()
    for number, authority in enumerate(authorities):
        shutil.copy(authority.ca_file, folder / f"{number}.pem")
    subprocess.run(["openssl", "rehash", str(folder)], check=True, capture_output=True)
    return folder


def get_tls_error(call: Call, core: FakeCore, ca_file: str | None = None) -> CalyxError:
    with pytest.raises(CalyxError) as caught:
        call(core.address, "send_plain_text", {"ca_file": ca_file}, **SEND, idempotency_key=KEY)
    return caught.value


def test_the_system_folders_are_trusted(
    call: Call,
    authority: Authority,
    other_authority: Authority,
    monkeypatch: pytest.MonkeyPatch,
    tmp_path: Path,
) -> None:
    folder = make_folder(tmp_path / "a", authority)
    other = make_folder(tmp_path / "b", other_authority)
    with FakeCore(answer_empty, files=authority.server_files("IP:127.0.0.1")) as core:
        only_folders(monkeypatch, folder)
        call(core.address, "list_instances", {})
        call(core.address, "list_instances", {"ca_file": other_authority.ca_file})
        only_folders(monkeypatch, Path("/nope"), other, folder)
        call(core.address, "list_instances", {})
        only_folders(monkeypatch, other)
        call(core.address, "list_instances", {"ca_file": authority.ca_file})
        assert len(core.calls) == 4
        error = get_tls_error(call, core)
        assert (error.source, error.code) == (Source.CORE, CoreCode.UNAVAILABLE)
        assert len(core.calls) == 4


def test_a_folder_is_read_as_openssl_reads_it(
    call: Call, authority: Authority, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    # OpenSSL reads only the files with the name of a hash.
    folder = tmp_path / "folder"
    folder.mkdir()
    shutil.copy(authority.ca_file, folder / "ca.pem")
    (folder / "12345678.0").write_text("no certificate")
    (folder / "12345678.1").mkdir()
    (folder / "12345678.2").symlink_to("/nope.pem")
    with FakeCore(answer_empty, files=authority.server_files("IP:127.0.0.1")) as core:
        only_folders(monkeypatch, folder)
        check_config_error(get_tls_error(call, core), KEY)
        # The bad files do not stop the good one.
        shutil.copy(authority.ca_file, folder / "12345678.3")
        call(core.address, "list_instances", {})
        # An empty name in the list is not the current folder.
        monkeypatch.chdir(folder)
        monkeypatch.setenv("SSL_CERT_DIR", os.pathsep)
        check_config_error(get_tls_error(call, core), KEY)
        assert len(core.calls) == 1


def test_a_folder_that_cannot_be_read(
    call: Call, authority: Authority, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    closed = tmp_path / "closed"
    (closed / "folder").mkdir(parents=True)
    closed.chmod(0)
    folder = make_folder(tmp_path / "good", authority)
    files = authority.server_files("IP:127.0.0.1")
    try:
        with FakeCore(answer_empty, files=files) as core:
            # The name is too long for the system.
            for bad in (closed / "folder", closed, Path("/" + "a" * 300)):
                only_folders(monkeypatch, bad, folder)
                call(core.address, "list_instances", {})
                only_folders(monkeypatch, bad)
                check_config_error(get_tls_error(call, core), KEY)
    finally:
        closed.chmod(0o700)


def test_no_certificate_at_all(
    call: Call, authority: Authority, monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    only_folders(monkeypatch, tmp_path)
    with FakeCore(answer_empty, files=authority.server_files("IP:127.0.0.1")) as core:
        # gRPC must not use its own list.
        error = get_tls_error(call, core)
        check_config_error(error, KEY)
        assert "certificates" in error.detail
        assert core.calls == []
        call(core.address, "list_instances", {"ca_file": authority.ca_file})
