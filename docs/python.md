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
python3 ./examples/python/stress_test.py --duration 10 --threads 100
```

JSON output mode:

```bash
python3 ./examples/python/stress_test.py --duration 10 --publishers 50 --consumers 50 --format json
```

Run `100` Python stress instances with GNU `parallel`:

```bash
./scripts/python-parallel-stress.sh
```

Override with env vars:

```bash
INSTANCES=100 DURATION=5 PUBLISHERS=1 CONSUMERS=1 PARALLEL_JOBS=100 ./scripts/python-parallel-stress.sh
```

## Performance On This Machine

```text
❯ ATOMIC_QUEUE_SOCKET=/tmp/atomic-queue.sock ATOMIC_QUEUE_BIN=./atomic-queue \
  python3 ./examples/python/stress_test.py --duration 3 --publishers 20 --consumers 20
stress duration: 2.952s
threads: 40 (20 producers, 20 consumers)
channels: stress-a, stress-b, stress-c, stress-d
messages pushed: 24691
messages served: 20067
pop timeouts: 0
client failures: 0
push rate: 8363.18 msg/s
serve rate: 6796.97 msg/s
```

This path is stdlib-only and easy to embed, but it is much slower than the Go binary and slower than the PHP socket client under load.
