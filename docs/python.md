# Python

Python support is stdlib-only: one small client library and one standalone stress example.

## Files

- Client library: [`examples/python/atomic_queue.py`](../examples/python/atomic_queue.py)
- Stress example: [`examples/python/stress_test.py`](../examples/python/stress_test.py)
- Parallel wrapper: [`scripts/python-parallel-stress.sh`](../scripts/python-parallel-stress.sh)

## Example

Push and pop from Python:

```bash
python3 ./examples/python/stress_test.py --duration 2 --publishers 2 --consumers 2
```

Use the client library directly:

```python
from atomic_queue import AtomicQueueClient

client = AtomicQueueClient()
client.push("jobs", b'{"job":"reindex","id":123}')
message = client.pop(["jobs"], 5000)
print(message)
client.close()
```

Run the standalone Python stress example:

```bash
python3 ./examples/python/stress_test.py --duration 10 --threads 1000
```

JSON output mode:

```bash
python3 ./examples/python/stress_test.py --duration 10 --publishers 500 --consumers 500 --format json
```

Run `100` Python stress instances with GNU `parallel`:

```bash
./scripts/python-parallel-stress.sh
```

Override with env vars:

```bash
INSTANCES=100 DURATION=10 PUBLISHERS=5 CONSUMERS=5 PARALLEL_JOBS=100 ./scripts/python-parallel-stress.sh
```

By default it now targets `10s` and `1000` total workers across `100` instances.

## Performance On This Machine

```text
❯ ATOMIC_QUEUE_SOCKET=/tmp/atomic-queue.sock ATOMIC_QUEUE_BIN=./atomic-queue \
  python3 ./examples/python/stress_test.py --duration 3 --publishers 20 --consumers 20
stress duration: 1.951s
threads: 40 (20 producers, 20 consumers)
channels: stress-a, stress-b, stress-c, stress-d
messages pushed: 20400
messages served: 14595
pop timeouts: 0
client failures: 0
push rate: 10454.24 msg/s
serve rate: 7479.40 msg/s
```

GNU `parallel`, `100` instances, `1` producer and `1` consumer per instance:

```text
❯ ATOMIC_QUEUE_SOCKET=/tmp/atomic-queue.sock ATOMIC_QUEUE_BIN=./atomic-queue \
  INSTANCES=100 DURATION=2 PUBLISHERS=1 CONSUMERS=1 PARALLEL_JOBS=100 ./scripts/python-parallel-stress.sh
stress duration: 3.483s
workers: 200 (100 producers, 100 consumers) across 100 instances
channels: stress-a, stress-b, stress-c, stress-d
messages pushed: 496378
messages served: 494401
pop timeouts: 0
client failures: 0
push rate: 142514.50 msg/s
serve rate: 141946.88 msg/s
```

Python note:

- The single-process threaded stress example is GIL-bound and does a lot of small Python-level socket/frame work, so it does not scale like the Go or PHP runners.
- If you want higher Python-side throughput, run many processes with GNU `parallel` or another process-based launcher instead of piling on more threads in one interpreter.
