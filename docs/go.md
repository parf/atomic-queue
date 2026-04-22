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
go run ./examples/go/stress --duration 10s --threads 1000
```

Explicit publishers and consumers:

```bash
go run ./examples/go/stress --duration 10s --publishers 500 --consumers 500
```

JSON output:

```bash
go run ./examples/go/stress --duration 10s --publishers 500 --consumers 500 --format json
```

If you already depend on Go, the built-in binary stress runner is still the fastest path:

```bash
atomic-queue stress --duration 10s --threads 1000
```

## Performance On This Machine

Built-in binary stress command:

```text
❯ ./atomic-queue stress --duration 10s --threads 1000
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

Standalone Go stress example:

```text
❯ ATOMIC_QUEUE_SOCKET=/tmp/atomic-queue.sock ATOMIC_QUEUE_BIN=./atomic-queue \
  go run ./examples/go/stress --duration 10s --threads 1000
stress duration: 10.003s
threads: 1000 (500 producers, 500 consumers)
channels: stress-a, stress-b, stress-c, stress-d
messages pushed: 4316805
messages served: 4280757
pop timeouts: 0
client failures: 0
push rate: 431570.33 msg/s
serve rate: 427966.45 msg/s
```

On this machine the standalone Go stress example is in the same class as the built-in runner and slightly edged it in one `10s/1000-thread` run, but the built-in `atomic-queue stress` command is still the canonical benchmark path.
