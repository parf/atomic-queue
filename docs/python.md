# Python

Python support is stdlib-only: one small client library and one standalone stress example.

## Files

- Client library: [`examples/python/atomic_queue.py`](../examples/python/atomic_queue.py)
- Stress example: [`examples/python/stress_test.py`](../examples/python/stress_test.py)

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
