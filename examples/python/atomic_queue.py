#!/usr/bin/env python3

from __future__ import annotations

import os
import socket
import struct
import subprocess
import time
from collections import OrderedDict
from pathlib import Path
from typing import Iterable

PACK_INT64 = struct.Struct(">q")
PACK_UINT16 = struct.Struct(">H")
PACK_UINT32 = struct.Struct(">I")
CLIENT_DIAL_TIMEOUT = 0.2
CHANNEL_CACHE_LIMIT = 256
READ_BUFFER_SIZE = 65536
_TIMEOUT_ERROR = b"timeout"


class AtomicQueueError(RuntimeError):
    pass


class AtomicQueueTimeout(AtomicQueueError):
    pass


def default_socket_path() -> str:
    override = os.getenv("ATOMIC_QUEUE_SOCKET")
    if override:
        return override
    return f"/run/user/{os.getuid()}/atomic-queue/atomic-queue.sock"


def binary_path() -> str:
    override = os.getenv("ATOMIC_QUEUE_BIN")
    if override:
        return override
    candidate = Path(__file__).resolve().parents[2] / "atomic-queue"
    if candidate.is_file():
        return str(candidate)
    raise AtomicQueueError(
        "ATOMIC_QUEUE_BIN is not set and the default atomic-queue binary was not found; "
        "set ATOMIC_QUEUE_BIN to the atomic-queue executable path"
    )


class AtomicQueueClient:
    OP_PUSH = 1
    OP_POP = 2
    STATUS_OK = 1

    def __init__(self, socket_path: str | None = None) -> None:
        self.socket_path = socket_path or default_socket_path()
        self.sock = self._connect(allow_autostart=True)
        self.rbuf = self.sock.makefile("rb", buffering=READ_BUFFER_SIZE)
        self._push_channel_cache: OrderedDict[str, bytes] = OrderedDict()
        self._pop_frame_cache: OrderedDict[tuple, bytes] = OrderedDict()

    def __enter__(self) -> "AtomicQueueClient":
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        self.close()

    def close(self) -> None:
        try:
            self.rbuf.close()
        finally:
            self.sock.close()

    def push(self, channel: str, payload: bytes) -> None:
        self._send_push(channel, payload)
        ok, _channel_bytes, _payload, error_bytes = self._read_response()
        if not ok:
            raise AtomicQueueError(error_bytes.decode("utf-8") if error_bytes else "")

    def pop(self, channels: Iterable[str], timeout_ms: int = 0) -> bytes:
        self._send_pop(channels, timeout_ms)
        ok, _channel_bytes, payload, error_bytes = self._read_response()
        if ok:
            return payload
        if error_bytes == _TIMEOUT_ERROR:
            raise AtomicQueueTimeout("timeout")
        raise AtomicQueueError(error_bytes.decode("utf-8") if error_bytes else "")

    def pop_message(self, channels: Iterable[str], timeout_ms: int = 0) -> tuple[str, bytes]:
        self._send_pop(channels, timeout_ms)
        ok, channel_bytes, payload, error_bytes = self._read_response()
        if ok:
            channel = channel_bytes.decode("utf-8") if channel_bytes else ""
            return channel, payload
        if error_bytes == _TIMEOUT_ERROR:
            raise AtomicQueueTimeout("timeout")
        raise AtomicQueueError(error_bytes.decode("utf-8") if error_bytes else "")

    def _connect(self, allow_autostart: bool) -> socket.socket:
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        sock.settimeout(CLIENT_DIAL_TIMEOUT)
        try:
            sock.connect(self.socket_path)
            sock.settimeout(None)
            return sock
        except FileNotFoundError:
            sock.close()
            if allow_autostart:
                ensure_daemon(self.socket_path)
                return self._connect(False)
            raise AtomicQueueError(f"connect to daemon failed at {self.socket_path}")
        except ConnectionRefusedError:
            sock.close()
            if allow_autostart:
                ensure_daemon(self.socket_path)
                return self._connect(False)
            raise AtomicQueueError(f"connect to daemon failed at {self.socket_path}")
        except socket.timeout:
            sock.close()
            raise AtomicQueueError(f"connect to daemon timed out at {self.socket_path}")

    def _send_push(self, channel: str, payload: bytes) -> None:
        encoded_channels = self._push_channel_cache.get(channel)
        if encoded_channels is None:
            encoded = channel.encode("utf-8")
            encoded_channels = PACK_UINT16.pack(1) + PACK_UINT16.pack(len(encoded)) + encoded
            self._cache_put(self._push_channel_cache, channel, encoded_channels)
        else:
            self._push_channel_cache.move_to_end(channel)
        frame = bytearray()
        frame.append(self.OP_PUSH)
        frame.extend(PACK_INT64.pack(0))
        frame.extend(encoded_channels)
        frame.extend(PACK_UINT32.pack(len(payload)))
        frame.extend(payload)
        self.sock.sendall(frame)

    def _send_pop(self, channels: Iterable[str], timeout_ms: int) -> None:
        channel_tuple = tuple(channels)
        cache_key = (channel_tuple, timeout_ms)
        frame = self._pop_frame_cache.get(cache_key)
        if frame is None:
            parts = [bytes((self.OP_POP,)), PACK_INT64.pack(timeout_ms), PACK_UINT16.pack(len(channel_tuple))]
            for channel in channel_tuple:
                encoded = channel.encode("utf-8")
                parts.append(PACK_UINT16.pack(len(encoded)))
                parts.append(encoded)
            parts.append(PACK_UINT32.pack(0))
            frame = b"".join(parts)
            self._cache_put(self._pop_frame_cache, cache_key, frame)
        else:
            self._pop_frame_cache.move_to_end(cache_key)
        self.sock.sendall(frame)

    def _read_response(self) -> tuple[bool, bytes, bytes, bytes]:
        read = self.rbuf.read
        try:
            status_byte = read(1)
            if len(status_byte) < 1:
                raise AtomicQueueError("unexpected EOF from daemon")
            channel_len = PACK_UINT16.unpack(read(2))[0]
            channel_bytes = read(channel_len) if channel_len else b""
            payload_len = PACK_UINT32.unpack(read(4))[0]
            payload = read(payload_len) if payload_len else b""
            error_len = PACK_UINT32.unpack(read(4))[0]
            error_bytes = read(error_len) if error_len else b""
        except struct.error as exc:
            raise AtomicQueueError("unexpected EOF from daemon") from exc
        if (
            len(channel_bytes) != channel_len
            or len(payload) != payload_len
            or len(error_bytes) != error_len
        ):
            raise AtomicQueueError("unexpected EOF from daemon")
        return status_byte[0] == self.STATUS_OK, channel_bytes, payload, error_bytes

    @staticmethod
    def _cache_put(cache: OrderedDict, key, value: bytes) -> None:
        cache[key] = value
        cache.move_to_end(key)
        if len(cache) > CHANNEL_CACHE_LIMIT:
            cache.popitem(last=False)


def ensure_daemon(socket_path: str) -> None:
    bin_path = binary_path()
    subprocess.Popen(
        [bin_path, "serve", "--socket", socket_path],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        start_new_session=True,
    )

    deadline = time.time() + 2.0
    while time.time() < deadline:
        probe = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        probe.settimeout(0.1)
        try:
            probe.connect(socket_path)
            probe.close()
            return
        except (FileNotFoundError, ConnectionRefusedError, socket.timeout):
            probe.close()
            time.sleep(0.05)

    raise AtomicQueueError(
        f'daemon did not start at {socket_path}\ntry:\n  atomic-queue serve --socket "{socket_path}"'
    )
