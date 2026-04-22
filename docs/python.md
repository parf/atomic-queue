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

Python note:

- The GNU `parallel` wrapper is the practical benchmark path for Python in this repo and should be used first when you want serious load.
- The single-process threaded stress example is GIL-bound and does a lot of small Python-level socket/frame work, so it does not scale like the Go or PHP runners.
- If you want higher Python-side throughput, run many processes with GNU `parallel` or another process-based launcher instead of piling on more threads in one interpreter.

## Performance On This Machine

GNU `parallel`, `100` instances, `5` producers and `5` consumers per instance:

```text
❯ ATOMIC_QUEUE_SOCKET=/tmp/atomic-queue.sock ATOMIC_QUEUE_BIN=./atomic-queue \
  INSTANCES=100 DURATION=10 PUBLISHERS=5 CONSUMERS=5 PARALLEL_JOBS=100 ./scripts/python-parallel-stress.sh
stress duration: 12.281s
workers: 1000 (500 producers, 500 consumers) across 100 instances
channels: stress-a, stress-b, stress-c, stress-d
messages pushed: 2168169
messages served: 2162480
pop timeouts: 0
client failures: 0
push rate: 176546.62 msg/s
serve rate: 176083.38 msg/s
```

Single-process threaded stress example:

```text
❯ ATOMIC_QUEUE_SOCKET=/tmp/atomic-queue.sock ATOMIC_QUEUE_BIN=./atomic-queue \
  python3 ./examples/python/stress_test.py --duration 10 --threads 1000
stress duration: 10.003s
threads: 1000 (500 producers, 500 consumers)
channels: stress-a, stress-b, stress-c, stress-d
messages pushed: 223324
messages served: 40256
pop timeouts: 0
client failures: 0
push rate: 22325.77 msg/s
serve rate: 4024.41 msg/s
```
