# Atomic local message queue for Linux

`atomic-queue` is a small local queue with one binary and no external dependencies.

- Multiple producers can push to a named channel.
- Multiple consumers can pop from one or more channels.
- `pop` can block with a timeout.
- Each message goes to one consumer only.
- Messages are UTF-8 strings. JSON is optional, not required.
- `pop` prints the raw message to stdout.

Internally it uses a tiny Unix socket daemon with in-memory per-channel FIFO queues. The CLI auto-starts the daemon if the socket does not exist yet.

## Build instructions

```bash
go build -o atomic-queue .
```

Run tests:

```bash
go test ./...
```

## Usage examples

Push a message:

```bash
./atomic-queue --run push jobs '{"foo":123,"bar":"x"}'
```

Push plain text:

```bash
./atomic-queue --run push logs 'worker started'
```

Blocking pop from one channel:

```bash
./atomic-queue --run pop jobs
```

Pop with timeout:

```bash
./atomic-queue --run pop jobs --timeout 5s
```

Wait on several channels:

```bash
./atomic-queue --run pop jobs highprio lowprio --timeout 1500ms
```

Start the daemon explicitly instead of auto-start:

```bash
./atomic-queue --run serve
```

Override the socket path:

```bash
ATOMIC_QUEUE_SOCKET=/tmp/atomic-queue.sock ./atomic-queue --run pop jobs
./atomic-queue --run push --socket /tmp/atomic-queue.sock jobs '{"id":1}'
```

Default socket:

```text
/run/$USER-atomic-queue.sock
```

If `/run/$USER-atomic-queue.sock` is not usable for the current user, the CLI falls back to a user-writable runtime path such as `$XDG_RUNTIME_DIR/atomic-queue/atomic-queue.sock`.

Read a raw message into shell:

```bash
msg="$(./atomic-queue --run pop jobs --timeout 5s)"
printf '%s\n' "$msg"
```

## systemd user service

Install and start the user service:

```bash
./scripts/install-systemd-service.sh
```

The service listens on:

```text
$XDG_RUNTIME_DIR/atomic-queue/atomic-queue.sock
```

Use clients against the service socket:

```bash
export ATOMIC_QUEUE_SOCKET="$XDG_RUNTIME_DIR/atomic-queue/atomic-queue.sock"
./atomic-queue --run push jobs 'hello'
./atomic-queue --run pop jobs --timeout 5s
```

## Exit codes

- `0`: success
- `1`: runtime error
- `2`: timeout on `pop`
- `64`: CLI usage error

## Limitations

- Linux only.
- Local machine only. No network protocol.
- In-memory only. Messages are lost if the daemon exits or the machine reboots.
- Payload must be valid UTF-8.
- Maximum payload size is `1 MiB`; larger messages are rejected cleanly.
- Channel names are limited to `[A-Za-z0-9._-]` and max length 128.
- If several requested channels already have queued data, `pop` checks them in the order you passed on the command line.

## Why this design instead of POSIX message queues / FIFO / SQLite

- POSIX message queues are fine for one queue at a time, but multi-channel blocking reads are awkward and usually need polling or extra machinery.
- FIFOs are just byte streams, so once you want named channels, atomic dequeue, and waiting on several channels, you end up rebuilding a broker anyway.
- SQLite would work, but it is heavier than needed for a local ephemeral queue.
