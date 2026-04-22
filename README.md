# ✦ Atomic local message queue for Linux

Fast enough for aggressive local IPC workloads: on this machine the built-in stress test sustains roughly `400k+` messages per second.

`atomic-queue` is a small Linux-only local queue with one binary and no external dependencies.

- Multiple producers can push to a named channel.
- Multiple consumers can pop from one or more channels.
- `pop` can block with a timeout.
- Each message goes to exactly one consumer.
- Any payload is supported: plain text, JSON, msgpack, binary blobs, anything up to `1 MiB`.
- `pop` writes raw message bytes to stdout.

Internally it uses one small Unix socket daemon with in-memory per-channel FIFO queues. If no daemon is running, `push` and `pop` auto-start one on first use.

## ⚙ Install

Install with Go:

```bash
go install github.com/parf/atomic-queue@latest
```

Build from source:

```bash
go build -o atomic-queue .
```

Run tests:

```bash
go test ./...
```

## ▶ Usage

Show help:

```bash
atomic-queue
atomic-queue help
```

Push a message:

```bash
atomic-queue push jobs '{"foo":123,"bar":"x"}'
```

Push raw bytes from stdin:

```bash
cat payload.msgpack | atomic-queue push --stdin jobs
```

Blocking pop:

```bash
atomic-queue pop jobs
atomic-queue pop jobs --timeout 5s
atomic-queue pop jobs highprio lowprio --timeout 1500ms
```

Start the daemon explicitly:

```bash
atomic-queue serve
```

Override the socket path:

```bash
ATOMIC_QUEUE_SOCKET=/tmp/atomic-queue.sock atomic-queue pop jobs
atomic-queue push --socket /tmp/atomic-queue.sock jobs 'hello'
```

Default socket:

```text
/run/user/$UID/atomic-queue/atomic-queue.sock
```

## ⚡ Stress Test

Built-in stress test:

```bash
atomic-queue stress --duration 10s --threads 1000
```

Explicit producers and consumers:

```bash
atomic-queue stress --duration 10s --publishers 500 --consumers 500
```

Machine-readable output:

```bash
atomic-queue stress --duration 10s --publishers 500 --consumers 500 --format json
```

Run the full benchmark suite with the standard profile:

```bash
./bench.sh
```

Observed on this machine:

```text
❯ ./bench.sh
benchmark profile:
  duration: 10s
  threads: 1000 (500 producers, 500 consumers)

== built-in ==
stress duration: 10.004s
threads: 1000 (500 producers, 500 consumers)
channels: stress-a, stress-b, stress-c, stress-d
messages pushed: 4252105
messages served: 4234231
pop timeouts: 0
client failures: 0
push rate: 425056.05 msg/s
serve rate: 423269.30 msg/s
```

## ☰ Language Examples

- PHP: [docs/php.md](docs/php.md)
- Python: [docs/python.md](docs/python.md)
- Go: [docs/go.md](docs/go.md)

Each language has separate example files so you can copy the one you need without digging through the main README.

## ⚡ systemd User Service

Install and start the user service:

```bash
./install-systemd-service.sh
```

The service socket is:

```text
$XDG_RUNTIME_DIR/atomic-queue/atomic-queue.sock
```

## ↩ Exit Codes

- `0`: success
- `1`: runtime error
- `2`: timeout on `pop`
- `64`: CLI usage error

## ⚠ Limitations

- Linux only.
- Local machine only.
- In-memory only; messages are lost if the daemon exits or the machine reboots.
- Maximum payload size is `1 MiB`.
- Channel names are limited to `[A-Za-z0-9._-]` and max length `128`.
- If several requested channels already have queued data, `pop` checks them in the order you passed.

## ✧ Why This Design

- POSIX message queues are decent for one queue at a time, but blocking reads across several named channels are awkward and usually push you toward polling or extra machinery.
- FIFOs are byte streams, not message queues, so once you need atomic dequeue and multi-channel waiting, you end up rebuilding a broker.
- SQLite is heavier than needed for a local ephemeral queue and adds avoidable operational surface.
