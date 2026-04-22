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
php ./scripts/php-stress-test.php --duration 10 --publishers 50 --consumers 50
```

Machine-readable output:

```bash
php ./scripts/php-stress-test.php --duration 10 --publishers 50 --consumers 50 --format json
```

Run several PHP stress instances in parallel:

```bash
INSTANCES=4 DURATION=10 PUBLISHERS=25 CONSUMERS=25 PARALLEL_JOBS=4 ./scripts/php-parallel-stress.sh
```

That wrapper keeps the hot path inside persistent PHP socket clients and prints one combined summary at the end.

## Performance On This Machine

Single PHP stress runner:

```text
❯ ATOMIC_QUEUE_SOCKET=/tmp/atomic-queue.sock ATOMIC_QUEUE_BIN=./atomic-queue \
  php ./scripts/php-stress-test.php --duration 3 --publishers 50 --consumers 50
stress duration: 3.075s
workers: 100 (50 producers, 50 consumers)
channels: stress-a, stress-b, stress-c, stress-d
messages pushed: 444843
messages served: 407903
pop timeouts: 0
client failures: 0
push rate: 144679.83 msg/s
serve rate: 132665.54 msg/s
```

Parallel PHP wrapper with two stress instances:

```text
❯ ATOMIC_QUEUE_SOCKET=/tmp/atomic-queue.sock ATOMIC_QUEUE_BIN=./atomic-queue \
  INSTANCES=2 DURATION=3 PUBLISHERS=25 CONSUMERS=25 PARALLEL_JOBS=2 ./scripts/php-parallel-stress.sh
stress duration: 3.222s
workers: 100 (50 producers, 50 consumers) across 2 instances
channels: stress-a, stress-b, stress-c, stress-d
messages pushed: 836027
messages served: 783078
pop timeouts: 0
client failures: 0
push rate: 259474.55 msg/s
serve rate: 243040.97 msg/s
```
