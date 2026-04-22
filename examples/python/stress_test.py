#!/usr/bin/env python3

from __future__ import annotations

import argparse
import json
import threading
import time
from dataclasses import dataclass

from atomic_queue import AtomicQueueClient, AtomicQueueError, AtomicQueueTimeout, default_socket_path


@dataclass
class Counters:
    pushed: int = 0
    served: int = 0
    timeouts: int = 0
    failures: int = 0


def make_payload(worker_id: int, seq: int, size: int) -> bytes:
    prefix = f"worker={worker_id} seq={seq} ".encode()
    if len(prefix) >= size:
        return prefix[:size]
    payload = bytearray(size)
    payload[: len(prefix)] = prefix
    alphabet = b"0123456789abcdef"
    for i in range(len(prefix), size):
        payload[i] = alphabet[(worker_id + seq + i) & 0x0F]
    return bytes(payload)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--socket", default=default_socket_path())
    parser.add_argument("--duration", type=float, default=10.0)
    parser.add_argument("--threads", type=int, default=100)
    parser.add_argument("--publishers", type=int)
    parser.add_argument("--consumers", type=int)
    parser.add_argument("--channels", default="stress-a,stress-b,stress-c,stress-d")
    parser.add_argument("--pop-timeout-ms", type=int, default=200)
    parser.add_argument("--payload-size", type=int, default=128)
    parser.add_argument("--format", choices=("text", "json"), default="text")
    return parser.parse_args()


def worker_counts(args: argparse.Namespace) -> tuple[int, int]:
    if args.publishers and args.consumers:
        return args.publishers, args.consumers
    publishers = max(1, args.threads // 2)
    consumers = max(1, args.threads - publishers)
    return publishers, consumers


def main() -> int:
    args = parse_args()
    channels = [value.strip() for value in args.channels.split(",") if value.strip()]
    publishers, consumers = worker_counts(args)
    end_time = time.monotonic() + args.duration
    counters = Counters()
    lock = threading.Lock()

    warm = AtomicQueueClient(args.socket)
    try:
        try:
            warm.pop(channels, 1)
        except AtomicQueueTimeout:
            pass
    finally:
        warm.close()

    def producer(worker_id: int) -> None:
        client = AtomicQueueClient(args.socket)
        seq = 0
        next_channel = worker_id % len(channels)
        try:
            while time.monotonic() < end_time:
                client.push(channels[next_channel], make_payload(worker_id, seq, args.payload_size))
                next_channel = (next_channel + 1) % len(channels)
                seq += 1
                with lock:
                    counters.pushed += 1
        except AtomicQueueError:
            with lock:
                counters.failures += 1
        finally:
            client.close()

    def consumer() -> None:
        client = AtomicQueueClient(args.socket)
        try:
            while time.monotonic() < end_time:
                try:
                    client.pop(channels, args.pop_timeout_ms)
                    with lock:
                        counters.served += 1
                except AtomicQueueTimeout:
                    with lock:
                        counters.timeouts += 1
        except AtomicQueueError:
            with lock:
                counters.failures += 1
        finally:
            client.close()

    started = time.monotonic()
    threads: list[threading.Thread] = []
    for worker_id in range(publishers):
        threads.append(threading.Thread(target=producer, args=(worker_id,)))
    for _ in range(consumers):
        threads.append(threading.Thread(target=consumer))
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()

    elapsed = max(time.monotonic() - started, 1e-9)
    result = {
        "duration_seconds": elapsed,
        "threads": publishers + consumers,
        "publishers": publishers,
        "consumers": consumers,
        "channels": channels,
        "messages_pushed": counters.pushed,
        "messages_served": counters.served,
        "pop_timeouts": counters.timeouts,
        "client_failures": counters.failures,
        "push_rate": counters.pushed / elapsed,
        "serve_rate": counters.served / elapsed,
    }
    if args.format == "json":
        print(json.dumps(result, separators=(",", ":")))
        return 0

    print(f"stress duration: {elapsed:.3f}s")
    print(f"threads: {result['threads']} ({publishers} producers, {consumers} consumers)")
    print(f"channels: {', '.join(channels)}")
    print(f"messages pushed: {counters.pushed}")
    print(f"messages served: {counters.served}")
    print(f"pop timeouts: {counters.timeouts}")
    print(f"client failures: {counters.failures}")
    print(f"push rate: {result['push_rate']:.2f} msg/s")
    print(f"serve rate: {result['serve_rate']:.2f} msg/s")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
