# Go

There are two Go examples:

- Integration/client example: [`examples/go/standalone-client/main.go`](../examples/go/standalone-client/main.go)
- Standalone stress example: [`examples/go/stress/main.go`](../examples/go/stress/main.go)

## Integrate into existing Go code

The standalone client example shows the full binary protocol in one file:

```bash
go run ./examples/go/standalone-client
```

It keeps a persistent Unix socket connection open, pushes one payload, then pops it back.

## Standalone Go stress example

Run it directly:

```bash
go run ./examples/go/stress --duration 10s --threads 100
```

Explicit publishers and consumers:

```bash
go run ./examples/go/stress --duration 10s --publishers 50 --consumers 50
```

JSON output:

```bash
go run ./examples/go/stress --duration 10s --publishers 50 --consumers 50 --format json
```

If you already depend on Go, the built-in binary stress runner is still the fastest path:

```bash
atomic-queue stress --duration 10s --threads 1000
```

## Performance On This Machine

Built-in binary stress command:

```text
❯ ./atomic-queue stress --duration 3s --threads 1000
stress duration: 3.005s
threads: 1000 (500 producers, 500 consumers)
channels: stress-a, stress-b, stress-c, stress-d
messages pushed: 1204390
messages served: 1202933
pop timeouts: 0
client failures: 0
push rate: 400818.12 msg/s
serve rate: 400333.23 msg/s
```

Built-in binary stress at `50` producers and `50` consumers:

```text
❯ ./atomic-queue stress --duration 3s --publishers 50 --consumers 50
stress duration: 3s
threads: 100 (50 producers, 50 consumers)
channels: stress-a, stress-b, stress-c, stress-d
messages pushed: 863783
messages served: 861090
pop timeouts: 0
client failures: 0
push rate: 287897.27 msg/s
serve rate: 286999.69 msg/s
```

Standalone Go stress example:

```text
❯ ATOMIC_QUEUE_SOCKET=/tmp/atomic-queue.sock ATOMIC_QUEUE_BIN=./atomic-queue \
  go run ./examples/go/stress --duration 3s --publishers 50 --consumers 50
stress duration: 2.000s
threads: 100 (50 producers, 50 consumers)
channels: stress-a, stress-b, stress-c, stress-d
messages pushed: 551660
messages served: 538927
pop timeouts: 0
client failures: 0
push rate: 275783.78 msg/s
serve rate: 269418.35 msg/s
```

The standalone example is useful for integration reference, but the built-in `atomic-queue stress` command is still the fastest and should be used for real benchmarking.
