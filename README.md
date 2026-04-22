# ✦ Atomic local message queue for Linux

`atomic-queue` is a small local queue with one binary and no external dependencies.

Fast enough for aggressive local IPC workloads: on this machine it sustains roughly `400k+` messages per second in the built-in stress test.

- Multiple producers can push to a named channel.
- Multiple consumers can pop from one or more channels.
- `pop` can block with a timeout.
- Each message goes to exactly one consumer.
- Any payload is supported: plain text, JSON, msgpack, binary blobs, you name it.
- `pop` writes the raw message bytes to stdout.

Internally it uses a tiny Unix socket daemon with in-memory per-channel FIFO queues. If no daemon is running, `push` and `pop` auto-start one on first use.

## ⚙ Install

Install with Go:

```bash
go install github.com/parf/atomic-queue@latest
```

Then run:

```bash
atomic-queue help
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

Autostart behavior:

```text
If the daemon is not running yet, `atomic-queue push ...` and `atomic-queue pop ...`
will start it automatically.
```

Push a message:

```bash
atomic-queue push jobs '{"foo":123,"bar":"x"}'
```

Push plain text:

```bash
atomic-queue push logs 'worker started'
```

Push binary data from stdin:

```bash
cat payload.msgpack | atomic-queue push --stdin jobs
```

Blocking pop from one channel:

```bash
atomic-queue pop jobs
```

Pop with timeout:

```bash
atomic-queue pop jobs --timeout 5s
```

Wait on several channels:

```bash
atomic-queue pop jobs highprio lowprio --timeout 1500ms
```

Run the built-in stress test for 10 seconds with 1000 threads:

```bash
atomic-queue stress --duration 10s --threads 1000
```

Use custom channels and timeouts:

```bash
atomic-queue stress --duration 15s --threads 400 --channels jobs,fast,slow --pop-timeout 100ms
```

Observed on this machine:

```text
❯ atomic-queue stress --duration 10s --threads 1000
stress duration: 10.004s
threads: 1000 (500 producers, 500 consumers)
channels: stress-a, stress-b, stress-c, stress-d
messages pushed: 4278367
messages served: 4269714
pop timeouts: 0
client failures: 0
push rate: 427670.05 msg/s
serve rate: 426805.09 msg/s
```

Start the daemon explicitly instead of relying on auto-start:

```bash
atomic-queue serve
```

Override the socket path:

```bash
ATOMIC_QUEUE_SOCKET=/tmp/atomic-queue.sock atomic-queue pop jobs
atomic-queue push --socket /tmp/atomic-queue.sock jobs '{"id":1}'
```

Default socket:

```text
/run/user/$UID/atomic-queue/atomic-queue.sock
```

Read a raw message into shell:

```bash
msg="$(atomic-queue pop jobs --timeout 5s)"
printf '%s\n' "$msg"
```

Read binary data into a file:

```bash
atomic-queue pop jobs > payload.msgpack
```

Use from PHP:

```php
<?php

$payload = json_encode([
    'job' => 'reindex',
    'id' => 123,
], JSON_UNESCAPED_SLASHES);

$cmd = sprintf(
    './atomic-queue push jobs %s',
    escapeshellarg($payload),
);
exec($cmd, $output, $code);
if ($code !== 0) {
    throw new RuntimeException('push failed');
}

$message = shell_exec('./atomic-queue pop jobs --timeout 5s');
if ($message === null) {
    throw new RuntimeException('pop failed');
}

$data = json_decode($message, true, flags: JSON_THROW_ON_ERROR);
var_dump($data);
```

Run the PHP smoke test script:

```bash
./scripts/php-integration-smoke.sh
```

Simple PHP example client using the Unix socket protocol:

```bash
php ./scripts/php-socket-client.php push jobs '{"job":"reindex","id":123}'
php ./scripts/php-socket-client.php pop jobs --timeout-ms 5000
```

For a real PHP load/perf run, use the dedicated stress script with persistent socket connections:

```bash
php ./scripts/php-stress-test.php --duration 10 --workers 100
```

Start 100 producers and 100 consumers with GNU `parallel`:

```bash
seq 1 100 | parallel -j100 './atomic-queue pop jobs --timeout 10s > /tmp/aq-consumer-{#}.out'
```

```bash
seq 1 100 | parallel -j100 './atomic-queue push jobs "{\"producer\":{},\"msg\":\"hello\"}"'
```

Collect consumer output:

```bash
cat /tmp/aq-consumer-*.out
```

Run the bundled GNU `parallel` load script:

```bash
./scripts/parallel-load-test.sh
```

Defaults:

- `100` consumers
- `100` producers
- `10,000` producer messages total
- `32` parallel jobs

Override with env vars:

```bash
CONSUMERS=50 PRODUCER_MESSAGES=200000 PARALLEL_JOBS=50 ./scripts/parallel-load-test.sh
```

If you really want the huge shell-driven run:

```bash
PRODUCER_MESSAGES=1000000 PARALLEL_JOBS=100 ./scripts/parallel-load-test.sh
```

Run the same style of load test through the PHP socket client:

```bash
./scripts/php-parallel-load-test.sh
```

Override with env vars:

```bash
PRODUCERS=20 CONSUMERS=20 PRODUCER_MESSAGES=2000 PARALLEL_JOBS=20 ./scripts/php-parallel-load-test.sh
```

## ⚡ systemd User Service

Install and start the user service:

```bash
./install-systemd-service.sh
```

The service listens on:

```text
$XDG_RUNTIME_DIR/atomic-queue/atomic-queue.sock
```

Use clients against the service socket:

```bash
export ATOMIC_QUEUE_SOCKET="$XDG_RUNTIME_DIR/atomic-queue/atomic-queue.sock"
./atomic-queue push jobs 'hello'
./atomic-queue pop jobs --timeout 5s
```

## ↩ Exit Codes

- `0`: success
- `1`: runtime error
- `2`: timeout on `pop`
- `64`: CLI usage error

## ⚠ Limitations

- Linux only.
- Local machine only. No network protocol.
- In-memory only. Messages are lost if the daemon exits or the machine reboots.
- Maximum payload size is `1 MiB`; larger messages are rejected cleanly.
- Channel names are limited to `[A-Za-z0-9._-]` and max length 128.
- If several requested channels already have queued data, `pop` checks them in the order you passed on the command line.

## ✧ Why This Design

- POSIX message queues are fine for one queue at a time, but multi-channel blocking reads are awkward and usually need polling or extra machinery.
- FIFOs are just byte streams, so once you want named channels, atomic dequeue, and waiting on several channels, you end up rebuilding a broker anyway.
- SQLite would work, but it is heavier than needed for a local ephemeral queue.
