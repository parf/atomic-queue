# Python

Python support is stdlib-only: one small client library and one standalone stress example.

## Files

- Client library: [`examples/python/atomic_queue.py`](../examples/python/atomic_queue.py)
- Stress example: [`examples/python/stress_runner.py`](../examples/python/stress_runner.py)
- Parallel wrapper: [`scripts/python-parallel-stress.sh`](../scripts/python-parallel-stress.sh)

## Example

Push and pop from Python:

```bash
python3 ./examples/python/stress_runner.py --duration 2 --publishers 2 --consumers 2
```

Use the client library directly:

```python
from atomic_queue import AtomicQueueClient

client = AtomicQueueClient()
client.push("jobs", b'{"job":"reindex","id":123}')
channel, message = client.pop_message(["jobs"], 5000)
print(channel, message)
client.close()
```

Run the standalone Python stress example:

```bash
python3 ./examples/python/stress_runner.py --duration 10 --threads 1000
```

JSON output mode:

```bash
python3 ./examples/python/stress_runner.py --duration 10 --publishers 500 --consumers 500 --format json
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

Python note:

- The GNU `parallel` wrapper is the practical benchmark path for Python in this repo and should be used first when you want serious load.
- The single-process threaded stress example is GIL-bound and does a lot of small Python-level socket/frame work, so it does not scale like the Go or PHP runners.
- If you want higher Python-side throughput, run many processes with GNU `parallel` or another process-based launcher instead of piling on more threads in one interpreter.

GNU `parallel`, `100` instances, `5` producers and `5` consumers per instance:

```text
❯ ATOMIC_QUEUE_SOCKET=/tmp/atomic-queue.sock ATOMIC_QUEUE_BIN=./atomic-queue \
  INSTANCES=100 DURATION=10 PUBLISHERS=5 CONSUMERS=5 PARALLEL_JOBS=100 ./scripts/python-parallel-stress.sh
stress duration: 12.760s
workers: 1000 (500 producers, 500 consumers) across 100 instances
channels: stress-a, stress-b, stress-c, stress-d
messages pushed: 2492436
messages served: 2448212
pop timeouts: 0
client failures: 0
push rate: 195331.97 msg/s
serve rate: 191866.14 msg/s
```

Single-process threaded stress example:

```text
❯ ATOMIC_QUEUE_SOCKET=/tmp/atomic-queue.sock ATOMIC_QUEUE_BIN=./atomic-queue \
  python3 ./examples/python/stress_runner.py --duration 10 --threads 1000
stress duration: 10.004s
threads: 1000 (500 producers, 500 consumers)
channels: stress-a, stress-b, stress-c, stress-d
messages pushed: 233151
messages served: 45928
pop timeouts: 0
client failures: 0
push rate: 23305.82 msg/s
serve rate: 4590.97 msg/s
```
