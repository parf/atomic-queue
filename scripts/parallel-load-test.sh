#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bin_path="$repo_dir/atomic-queue"

if ! command -v parallel >/dev/null 2>&1; then
  echo "gnu parallel is required" >&2
  exit 1
fi

if [[ ! -x "$bin_path" ]]; then
  echo "building atomic-queue binary"
  (cd "$repo_dir" && go build -o atomic-queue .)
fi

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

export ATOMIC_QUEUE_SOCKET="${ATOMIC_QUEUE_SOCKET:-$tmpdir/atomic-queue.sock}"
export channel="${CHANNEL:-jobs}"
export producers="${PRODUCERS:-100}"
export consumers="${CONSUMERS:-100}"
export producer_messages="${PRODUCER_MESSAGES:-10000}"
export parallel_jobs="${PARALLEL_JOBS:-32}"
export consumer_timeout="${CONSUMER_TIMEOUT:-2s}"
export bin_path

export messages_per_producer=$((producer_messages / producers))
export extra_messages=$((producer_messages % producers))

echo "socket: $ATOMIC_QUEUE_SOCKET"
echo "channel: $channel"
echo "producers: $producers"
echo "consumers: $consumers"
echo "producer messages: $producer_messages"
echo "parallel jobs: $parallel_jobs"

coproc CONSUMERS_PROC {
  seq 1 "$consumers" | parallel --jobs "$parallel_jobs" \
    --env ATOMIC_QUEUE_SOCKET --env channel --env consumer_timeout --env bin_path '
      consumer_id={}
      : "$consumer_id"
      count=0
      while "$bin_path" pop "$channel" --timeout "$consumer_timeout" >/dev/null 2>/dev/null; do
        count=$((count + 1))
      done
      printf "%s\n" "$count"
    '
}

sleep 0.2

seq 1 "$producers" | parallel --jobs "$parallel_jobs" \
  --env ATOMIC_QUEUE_SOCKET --env channel --env bin_path --env messages_per_producer --env extra_messages '
    producer_id={}
    count="$messages_per_producer"
    if [[ "$producer_id" -le "$extra_messages" ]]; then
      count=$((count + 1))
    fi
    for n in $(seq 1 "$count"); do
      "$bin_path" push "$channel" "{\"producer\":$producer_id,\"msg\":$n}" >/dev/null
    done
  '

received_count="$(awk '{sum += $1} END {print sum+0}' <&"${CONSUMERS_PROC[0]}")"

echo "sent messages: $producer_messages"
echo "received messages: $received_count"
