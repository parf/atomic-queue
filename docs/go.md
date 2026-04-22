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
