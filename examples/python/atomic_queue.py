#!/usr/bin/env python3

from __future__ import annotations

import os
import socket
import struct
import subprocess
import time
from pathlib import Path
from typing import Iterable

PACK_INT64 = struct.Struct(">q")
PACK_UINT16 = struct.Struct(">H")
PACK_UINT32 = struct.Struct(">I")


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
    return str(Path(__file__).resolve().parents[2] / "atomic-queue")


class AtomicQueueClient:
    OP_PUSH = 1
    OP_POP = 2
    STATUS_OK = 1

    def __init__(self, socket_path: str | None = None) -> None:
        self.socket_path = socket_path or default_socket_path()
        self.sock = self._connect(allow_autostart=True)
        self._channel_cache: dict[tuple[str, ...], bytes] = {}
        self._push_channel_cache: dict[str, bytes] = {}

    def close(self) -> None:
        self.sock.close()

    def push(self, channel: str, payload: bytes) -> None:
        self._write_request(self.OP_PUSH, [channel], payload, 0)
        ok, _payload, error = self._read_response()
        if not ok:
            raise AtomicQueueError(error)

    def pop(self, channels: Iterable[str], timeout_ms: int = 0) -> bytes:
        channel_list = list(channels)
        self._write_request(self.OP_POP, channel_list, b"", timeout_ms)
        ok, payload, error = self._read_response()
        if ok:
            return payload
        if error == "timeout":
            raise AtomicQueueTimeout(error)
        raise AtomicQueueError(error)

    def _connect(self, allow_autostart: bool) -> socket.socket:
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        try:
            sock.connect(self.socket_path)
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

    def _write_request(self, op: int, channels: list[str], payload: bytes, timeout_ms: int) -> None:
        frame = bytearray()
        frame.append(op)
        frame.extend(PACK_INT64.pack(timeout_ms))
        if op == self.OP_PUSH and len(channels) == 1:
            encoded_channels = self._push_channel_cache.get(channels[0])
            if encoded_channels is None:
                encoded = channels[0].encode("utf-8")
                encoded_channels = PACK_UINT16.pack(1) + PACK_UINT16.pack(len(encoded)) + encoded
                self._push_channel_cache[channels[0]] = encoded_channels
            frame.extend(encoded_channels)
        else:
            key = tuple(channels)
            encoded_channels = self._channel_cache.get(key)
            if encoded_channels is None:
                encoded_parts = [PACK_UINT16.pack(len(channels))]
                for channel in channels:
                    encoded = channel.encode("utf-8")
                    encoded_parts.append(PACK_UINT16.pack(len(encoded)))
                    encoded_parts.append(encoded)
                encoded_channels = b"".join(encoded_parts)
                self._channel_cache[key] = encoded_channels
            frame.extend(encoded_channels)
        frame.extend(PACK_UINT32.pack(len(payload)))
        frame.extend(payload)
        self.sock.sendall(frame)

    def _read_response(self) -> tuple[bool, bytes, str]:
        status = self._read_exact(1)[0]
        self._read_string16_bytes()
        payload = self._read_bytes32()
        error_bytes = self._read_bytes32()
        error = error_bytes.decode("utf-8") if error_bytes else ""
        return status == self.STATUS_OK, payload, error

    def _read_string16_bytes(self) -> bytes:
        length = PACK_UINT16.unpack(self._read_exact(2))[0]
        return self._read_exact(length)

    def _read_bytes32(self) -> bytes:
        length = PACK_UINT32.unpack(self._read_exact(4))[0]
        return self._read_exact(length)

    def _read_exact(self, length: int) -> bytes:
        if length == 0:
            return b""
        buf = bytearray(length)
        view = memoryview(buf)
        received = 0
        while received < length:
            chunk = self.sock.recv_into(view[received:], length - received)
            if chunk == 0:
                raise AtomicQueueError("unexpected EOF from daemon")
            received += chunk
        return bytes(buf)


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
        try:
            probe.connect(socket_path)
            probe.close()
            return
        except OSError:
            probe.close()
            time.sleep(0.05)

    raise AtomicQueueError(
        f'daemon did not start at {socket_path}\ntry:\n  atomic-queue serve --socket "{socket_path}"'
    )
