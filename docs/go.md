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
stress duration: 3.036s
threads: 1000 (500 producers, 500 consumers)
channels: stress-a, stress-b, stress-c, stress-d
messages pushed: 464652
messages served: 460088
pop timeouts: 0
client failures: 0
push rate: 153029.41 msg/s
serve rate: 151526.29 msg/s
```

Standalone Go stress example:

```text
❯ ATOMIC_QUEUE_SOCKET=/tmp/atomic-queue.sock ATOMIC_QUEUE_BIN=./atomic-queue \
  go run ./examples/go/stress --duration 3s --publishers 50 --consumers 50
stress duration: 3.020s
threads: 100 (50 producers, 50 consumers)
channels: stress-a, stress-b, stress-c, stress-d
messages pushed: 143766
messages served: 141728
pop timeouts: 0
client failures: 0
push rate: 47598.17 msg/s
serve rate: 46923.42 msg/s
```

The standalone example is useful for integration reference, but the built-in `atomic-queue stress` command is materially faster and should be used for real benchmarking.
