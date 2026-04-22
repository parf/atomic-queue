# PHP

Use the native Unix socket client instead of shelling out to `atomic-queue` for each message.

## Files

- Example client: [`examples/php/client.php`](../examples/php/client.php)
- Reusable PHP library: [`scripts/php_atomic_queue_lib.php`](../scripts/php_atomic_queue_lib.php)
- PHP stress runner: [`scripts/php-stress-test.php`](../scripts/php-stress-test.php)
- Parallel PHP stress wrapper: [`scripts/php-parallel-stress.sh`](../scripts/php-parallel-stress.sh)
- PHP smoke test: [`scripts/php-integration-smoke.sh`](../scripts/php-integration-smoke.sh)

## Example

Run the example client:

```bash
php ./examples/php/client.php
```

Run the direct PHP smoke test:

```bash
./scripts/php-integration-smoke.sh
```

Run the PHP stress test:

```bash
php ./scripts/php-stress-test.php --duration 10 --publishers 500 --consumers 500
```

Machine-readable output:

```bash
php ./scripts/php-stress-test.php --duration 10 --publishers 500 --consumers 500 --format json
```

Run several PHP stress instances in parallel:

```bash
./scripts/php-parallel-stress.sh
```

That wrapper keeps the hot path inside persistent PHP socket clients and prints one combined summary at the end.
By default it now targets `10s` and `1000` total workers across `10` instances.

## Performance On This Machine

Single PHP stress runner:

```text
❯ ATOMIC_QUEUE_SOCKET=/tmp/atomic-queue.sock ATOMIC_QUEUE_BIN=./atomic-queue \
  php ./scripts/php-stress-test.php --duration 10 --publishers 500 --consumers 500
stress duration: 10.692s
workers: 1000 (500 producers, 500 consumers)
channels: stress-a, stress-b, stress-c, stress-d
messages pushed: 3060477
messages served: 2610164
pop timeouts: 0
client failures: 0
push rate: 286252.99 msg/s
serve rate: 244134.25 msg/s
```

Parallel PHP wrapper with the standard `10` instances:

```text
❯ ATOMIC_QUEUE_SOCKET=/tmp/atomic-queue.sock ATOMIC_QUEUE_BIN=./atomic-queue \
  INSTANCES=10 DURATION=10 PUBLISHERS=50 CONSUMERS=50 PARALLEL_JOBS=10 ./scripts/php-parallel-stress.sh
stress duration: 10.946s
workers: 1000 (500 producers, 500 consumers) across 10 instances
channels: stress-a, stress-b, stress-c, stress-d
messages pushed: 2477453
messages served: 2426689
pop timeouts: 0
client failures: 0
push rate: 226334.09 msg/s
serve rate: 221696.42 msg/s
```
