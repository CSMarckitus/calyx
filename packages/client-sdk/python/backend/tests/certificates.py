"""Certificates for the tests. The openssl command makes them at run time, so no key is stored."""

import ssl
import subprocess
from dataclasses import dataclass
from pathlib import Path


@dataclass(frozen=True, slots=True)
class Authority:
    """A private certificate authority (CA)."""

    folder: Path

    @property
    def ca_file(self) -> str:
        return str(self.folder / "ca.pem")

    def server_context(self, name: str) -> ssl.SSLContext:
        """Makes the TLS context of a server. `name` is for example "DNS:localhost"."""
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(*self.server_files(name))
        return context

    def server_files(self, name: str) -> tuple[Path, Path]:
        """Makes the certificate and the key of a server."""
        _make_request(self.folder, "server", "/CN=server")
        extensions = self.folder / "server.ext"
        extensions.write_text(
            "basicConstraints=critical,CA:FALSE\n"
            "authorityKeyIdentifier=keyid\n"
            f"subjectAltName={name}\n"
        )
        _openssl(
            self.folder,
            *("x509", "-req", "-in", "server.csr", "-CA", "ca.pem", "-CAkey", "ca.key"),
            *("-extfile", "server.ext", "-days", "2", "-out", "server.pem"),
        )
        return self.folder / "server.pem", self.folder / "server.key"


def make_authority(folder: Path) -> Authority:
    folder.mkdir()
    # Python 3.13 and later refuse a CA with no key usage.
    _openssl(
        folder,
        *("req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes"),
        *("-subj", "/CN=Test CA", "-days", "2", "-keyout", "ca.key", "-out", "ca.pem"),
        *("-addext", "keyUsage=critical,keyCertSign"),
    )
    return Authority(folder)


def _make_request(folder: Path, name: str, subject: str) -> None:
    _openssl(
        folder,
        *("req", "-new", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes"),
        *("-subj", subject, "-keyout", f"{name}.key", "-out", f"{name}.csr"),
    )


def _openssl(folder: Path, *arguments: str) -> None:
    subprocess.run(["openssl", *arguments], cwd=folder, check=True, capture_output=True)
