#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
python_stress="$repo_dir/examples/python/stress_runner.py"
export ATOMIC_QUEUE_BIN="${ATOMIC_QUEUE_BIN:-$repo_dir/atomic-queue}"

if ! command -v parallel >/dev/null 2>&1; then
  echo "gnu parallel is required" >&2
  exit 1
fi

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required" >&2
  exit 1
fi

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

export ATOMIC_QUEUE_SOCKET="${ATOMIC_QUEUE_SOCKET:-$tmpdir/atomic-queue.sock}"
export instances="${INSTANCES:-100}"
export duration="${DURATION:-10}"
export publishers="${PUBLISHERS:-5}"
export consumers="${CONSUMERS:-5}"
export channels="${CHANNELS:-stress-a,stress-b,stress-c,stress-d}"
export pop_timeout_ms="${POP_TIMEOUT_MS:-200}"
export payload_size="${PAYLOAD_SIZE:-128}"
export parallel_jobs="${PARALLEL_JOBS:-$instances}"
export verbose="${VERBOSE:-0}"
export python_stress

echo "socket: $ATOMIC_QUEUE_SOCKET"
echo "instances: $instances"
echo "publishers per instance: $publishers"
echo "consumers per instance: $consumers"
echo "duration: ${duration}s"
echo "parallel jobs: $parallel_jobs"

warm_channel="${channels%%,*}"
"$ATOMIC_QUEUE_BIN" pop "$warm_channel" --timeout 1ms >/dev/null 2>/dev/null || true

start_ns="$(date +%s%N)"
json_lines="$(
  seq 1 "$instances" | parallel --jobs "$parallel_jobs" --env ATOMIC_QUEUE_SOCKET --env ATOMIC_QUEUE_BIN --env python_stress --env duration --env publishers --env consumers --env channels --env pop_timeout_ms --env payload_size '
    instance_id={}
    : "$instance_id"
    python3 "$python_stress" \
      --duration "$duration" \
      --publishers "$publishers" \
      --consumers "$consumers" \
      --channels "$channels" \
      --pop-timeout-ms "$pop_timeout_ms" \
      --payload-size "$payload_size" \
      --format json
  '
)"
end_ns="$(date +%s%N)"

if [[ -z "$json_lines" ]]; then
  echo "no stress results produced" >&2
  exit 1
fi

if [[ "$verbose" == "1" ]]; then
  printf '%s\n' "$json_lines" | python3 -c '
import json, sys
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    row = json.loads(line)
    print(
        "instance: workers=%d pushed=%d served=%d timeouts=%d failures=%d push_rate=%.2f serve_rate=%.2f"
        % (
            row["threads"],
            row["messages_pushed"],
            row["messages_served"],
            row["pop_timeouts"],
            row["client_failures"],
            row["push_rate"],
            row["serve_rate"],
        )
    )
'
fi

elapsed="$(awk -v start="$start_ns" -v end="$end_ns" 'BEGIN { printf "%.3f", (end - start) / 1000000000 }')"

printf '%s\n' "$json_lines" | PYTHON_PARALLEL_STRESS_ELAPSED="$elapsed" \
  INSTANCES="$instances" PUBLISHERS="$publishers" CONSUMERS="$consumers" CHANNELS="$channels" \
  python3 -c '
import json, os, sys

elapsed = float(os.environ["PYTHON_PARALLEL_STRESS_ELAPSED"])
instances = int(os.environ["INSTANCES"])
publishers = int(os.environ["PUBLISHERS"])
consumers = int(os.environ["CONSUMERS"])
channels = os.environ["CHANNELS"].replace(",", ", ")

summary = {
    "messages_pushed": 0,
    "messages_served": 0,
    "pop_timeouts": 0,
    "client_failures": 0,
    "instances": 0,
}

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    row = json.loads(line)
    summary["messages_pushed"] += int(row["messages_pushed"])
    summary["messages_served"] += int(row["messages_served"])
    summary["pop_timeouts"] += int(row["pop_timeouts"])
    summary["client_failures"] += int(row["client_failures"])
    summary["instances"] += 1

if elapsed <= 0:
    elapsed = 1.0

print(f"stress duration: {elapsed:.3f}s")
print(
    "workers: %d (%d producers, %d consumers) across %d instances"
    % (
        (publishers + consumers) * instances,
        publishers * instances,
        consumers * instances,
        summary["instances"],
    )
)
print(f"channels: {channels}")
print("messages pushed: %d" % summary["messages_pushed"])
print("messages served: %d" % summary["messages_served"])
print("pop timeouts: %d" % summary["pop_timeouts"])
print("client failures: %d" % summary["client_failures"])
print("push rate: %.2f msg/s" % (summary["messages_pushed"] / elapsed))
print("serve rate: %.2f msg/s" % (summary["messages_served"] / elapsed))
'
