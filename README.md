# 👉 Atomic local JSON message queue with blocking multi-channel reads and timeouts (Linux, no dependencies)

`atomic-queue` is a small Linux-only local message queue. It builds to a single Go binary named `atomic-queue`. The binary contains both the CLI client and a tiny Unix domain socket daemon. Messages are UTF-8 JSON strings, delivery is at-most-once, and each message goes to only one consumer.

## Short design decision

Chosen design: `Unix domain sockets + a very small daemon`.

Why:

- Atomic multi-producer enqueue and atomic consumer dequeue are easy to make correct with one in-memory broker protected by a mutex.
- Blocking reads with timeout are straightforward.
- Waiting on several named channels is natural inside one broker; no polling loop or extra helper process is required.
- Deployment stays simple: one binary, no external database, no background dependencies other than the tiny daemon the binary can auto-start itself.

## Internal architecture

- `atomic-queue push ...` and `atomic-queue pop ...` are clients that talk to a Unix socket.
- If the socket is missing, the client auto-starts `atomic-queue serve` in the background.
- `atomic-queue serve` keeps per-channel FIFO queues in memory.
- A push either appends to the channel queue or hands the message directly to the oldest waiting consumer interested in that channel.
- A pop checks all requested channels immediately; if none has data, it waits until a matching push arrives or timeout expires.

## Build instructions

```bash
cd /rd/go/atomic-queue
go build -o atomic-queue .
```

Run tests:

```bash
go test ./...
```

## Usage examples

Push one JSON message:

```bash
./atomic-queue push jobs '{"foo":123,"bar":"x"}'
```

Blocking pop from one channel:

```bash
./atomic-queue pop jobs
```

Pop with timeout:

```bash
./atomic-queue pop jobs --timeout 5s
```

Wait on several channels:

```bash
./atomic-queue pop jobs highprio lowprio --timeout 1500ms
```

Start the daemon explicitly instead of auto-start:

```bash
./atomic-queue serve
```

Override the socket path:

```bash
Q_SOCKET=/tmp/atomic-queue.sock ./atomic-queue pop jobs
./atomic-queue push --socket /tmp/atomic-queue.sock jobs '{"id":1}'
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
- Payload must be valid UTF-8 JSON.
- Maximum payload size is `1 MiB`; larger messages are rejected cleanly.
- Channel names are limited to `[A-Za-z0-9._-]` and max length 128.
- If several requested channels already have queued data, `pop` checks channels in the order you passed on the command line.

## Simple concurrent test examples

One producer / one consumer:

```bash
./atomic-queue pop demo &
pid=$!
sleep 0.1
./atomic-queue push demo '{"msg":"hello"}'
wait "$pid"
```

Many producers / many consumers on one channel:

```bash
for i in $(seq 1 20); do
  ./atomic-queue pop work --timeout 5s >"/tmp/atomic-queue-out-$i" &
done

for i in $(seq 1 20); do
  ./atomic-queue push work "{\"id\":$i}" &
done

wait
grep -h . /tmp/atomic-queue-out-* | sort
```

## Why this design instead of POSIX message queues / FIFO / SQLite

### POSIX message queues

POSIX mqueues are close for single-channel blocking FIFO, but they are awkward for `pop channel1 channel2 channel3`. There is no clean native `select`/`poll` story across many queues, so multi-channel waiting usually turns into polling, signal-notify tricks, or an extra helper layer. That adds exactly the complexity this tool is trying to avoid.

### FIFOs / pipes

FIFOs preserve byte streams, not named-channel queue semantics. Handling many producers, many consumers, blocking reads, fair dequeue, message boundaries, and waiting across several channels becomes fragile quickly. You end up rebuilding a broker in user space anyway.

### SQLite

SQLite would work technically, but it is not the simplest Linux-native IPC path here. It adds a file-backed database engine, locking behavior, SQL schema, and operational concerns that are unnecessary for a local ephemeral queue.
