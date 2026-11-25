"""HTTP servers for the tests: raw sockets in threads, so a test controls each byte and each wait."""

import socket
import ssl
import threading
import time
from collections.abc import Callable
from dataclasses import dataclass
from types import TracebackType
from typing import Self

# The longest wait of a server. It is shorter than the limit of one test (10 s).
MAX_WAIT = 6.0


@dataclass(frozen=True, slots=True)
class Request:
    """One request with its body, as the server got it."""

    line: str
    headers: list[tuple[str, str]]
    body: bytes

    def all(self, name: str) -> list[str]:
        """Gives each value of a header."""
        return [value for key, value in self.headers if key.lower() == name.lower()]


class Connection:
    """One connection on the server side."""

    def __init__(self, server: "FakeServer", sock: socket.socket) -> None:
        self.server = server
        self.sock = sock

    def read_request(self) -> Request:
        """Reads one request and counts it."""
        data = b""
        while b"\r\n\r\n" not in data:
            data += self._receive()
        head, _, body = data.partition(b"\r\n\r\n")
        line, *lines = head.decode("latin-1").split("\r\n")
        headers = [
            (key, value.strip()) for key, _, value in (text.partition(":") for text in lines)
        ]
        request = Request(line, headers, b"")
        length = int((request.all("Content-Length") or ["0"])[0])
        while len(body) < length:
            body += self._receive()
        request = Request(line, headers, body)
        self.server.requests.append(request)
        return request

    def _receive(self) -> bytes:
        data = self.sock.recv(1 << 16)
        if not data:
            raise ConnectionError("The client closed the connection inside a request.")
        return data

    def answer(self, status: int = 200, body: bytes = b"{}", headers: str = "") -> None:
        head = f"HTTP/1.1 {status} Text\r\nContent-Length: {len(body)}\r\n{headers}\r\n"
        self.sock.sendall(head.encode() + body)

    def send_slowly(self, data: bytes, *, pause: float = 0.1) -> None:
        """Sends one byte, waits, and sends the next one."""
        for byte in data:
            self.sock.sendall(bytes([byte]))
            if self.wait(pause):
                return

    def wait(self, seconds: float = MAX_WAIT) -> bool:
        """Waits. It gives True when the call of the client ended before."""
        return self.server.call_ended.wait(seconds)


class FakeServer:
    """Runs `handle` in a thread for each connection. `handle` stops at its first OSError."""

    def __init__(
        self,
        handle: Callable[[Connection], None],
        *,
        tls: ssl.SSLContext | None = None,
        host: str = "127.0.0.1",
        receive_buffer: int | None = None,
    ) -> None:
        self.connections = 0
        self.requests: list[Request] = []
        self.call_ended = threading.Event()
        """A test sets it after the call, and a handler that waits stops."""

        self._handle = handle
        self._tls = tls
        self._threads: list[threading.Thread] = []
        self._changed = threading.Condition()
        self._open = 0
        family = socket.AF_INET6 if ":" in host else socket.AF_INET
        self._listener = socket.socket(family, socket.SOCK_STREAM)
        if receive_buffer is not None:
            self._listener.setsockopt(socket.SOL_SOCKET, socket.SO_RCVBUF, receive_buffer)
        self._listener.bind((host, 0))
        self._listener.listen()
        self.port: int = self._listener.getsockname()[1]

    def __enter__(self) -> Self:
        self._start(self._accept)
        return self

    def __exit__(
        self,
        kind: type[BaseException] | None,
        error: BaseException | None,
        trace: TracebackType | None,
    ) -> None:
        self.call_ended.set()
        # A shutdown makes the accept call of the other thread return.
        self._listener.shutdown(socket.SHUT_RDWR)
        for thread in self._threads:
            thread.join(MAX_WAIT)
        self._listener.close()

    def client_closed_all(self, seconds: float = 2.0) -> bool:
        """Waits until the client closed each connection."""
        self.call_ended.set()
        with self._changed:
            return self._changed.wait_for(lambda: self._open == 0, seconds)

    def _start(self, target: Callable[..., None], *args: object) -> None:
        thread = threading.Thread(target=target, args=args, daemon=True)
        self._threads.append(thread)
        thread.start()

    def _accept(self) -> None:
        while True:
            try:
                sock, _ = self._listener.accept()
            except OSError:
                return
            self.connections += 1
            with self._changed:
                self._open += 1
            self._start(self._serve, sock)

    def _serve(self, sock: socket.socket) -> None:
        sock.settimeout(MAX_WAIT)
        closed = True
        try:
            if self._tls is not None:
                sock = self._tls.wrap_socket(sock, server_side=True)
            self._handle(Connection(self, sock))
            # The end of the bytes shows that the client closed the connection.
            while sock.recv(1 << 20):
                pass
        except TimeoutError:
            closed = False
        except OSError:
            pass
        finally:
            sock.close()
        if closed:
            with self._changed:
                self._open -= 1
                self._changed.notify_all()


class SilentAddresses:
    """Addresses of the loopback network that never answer a connect.

    Linux drops each new connect when the accept queue of a listener is full. So this needs no
    network and no route.
    """

    HOSTS = ("127.0.0.11", "127.0.0.12", "127.0.0.13")

    def __init__(self, port: int = 0) -> None:
        self.port = port

    def __enter__(self) -> Self:
        self._sockets: list[socket.socket] = []
        for host in self.HOSTS:
            listener = socket.socket()
            listener.bind((host, self.port))
            listener.listen(0)
            self.port = listener.getsockname()[1]
            self._sockets.append(listener)
            for _ in range(3):
                client = socket.socket()
                client.setblocking(False)
                client.connect_ex((host, self.port))
                self._sockets.append(client)
        # The handshakes of the loopback network end at once, but not inside connect_ex.
        time.sleep(0.05)
        return self

    def __exit__(
        self,
        kind: type[BaseException] | None,
        error: BaseException | None,
        trace: TracebackType | None,
    ) -> None:
        for sock in self._sockets:
            sock.close()
