#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
php_stress="$repo_dir/scripts/php-stress-test.php"
export ATOMIC_QUEUE_BIN="${ATOMIC_QUEUE_BIN:-$repo_dir/atomic-queue}"

if ! command -v parallel >/dev/null 2>&1; then
  echo "gnu parallel is required" >&2
  exit 1
fi

if ! command -v php >/dev/null 2>&1; then
  echo "php is required" >&2
  exit 1
fi

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

export ATOMIC_QUEUE_SOCKET="${ATOMIC_QUEUE_SOCKET:-$tmpdir/atomic-queue.sock}"
export instances="${INSTANCES:-10}"
export duration="${DURATION:-10}"
export publishers="${PUBLISHERS:-50}"
export consumers="${CONSUMERS:-50}"
export channels="${CHANNELS:-stress-a,stress-b,stress-c,stress-d}"
export pop_timeout_ms="${POP_TIMEOUT_MS:-200}"
export payload_size="${PAYLOAD_SIZE:-128}"
export parallel_jobs="${PARALLEL_JOBS:-$instances}"
export verbose="${VERBOSE:-0}"
export php_stress

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
  seq 1 "$instances" | parallel --jobs "$parallel_jobs" --env ATOMIC_QUEUE_SOCKET --env ATOMIC_QUEUE_BIN --env php_stress --env duration --env publishers --env consumers --env channels --env pop_timeout_ms --env payload_size '
    instance_id={}
    : "$instance_id"
    php "$php_stress" \
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
  printf '%s\n' "$json_lines" | php -r '
    foreach (file("php://stdin", FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES) as $line) {
        $row = json_decode($line, true, 512, JSON_THROW_ON_ERROR);
        printf(
            "instance: workers=%d pushed=%d served=%d timeouts=%d failures=%d push_rate=%.2f serve_rate=%.2f\n",
            $row["workers"],
            $row["messages_pushed"],
            $row["messages_served"],
            $row["pop_timeouts"],
            $row["client_failures"],
            $row["push_rate"],
            $row["serve_rate"]
        );
    }
  '
fi

elapsed="$(awk -v start="$start_ns" -v end="$end_ns" 'BEGIN { printf "%.3f", (end - start) / 1000000000 }')"

printf '%s\n' "$json_lines" | php -r '
  $totals = [
      "messages_pushed" => 0,
      "messages_served" => 0,
      "pop_timeouts" => 0,
      "client_failures" => 0,
  ];
  $count = 0;
  foreach (file("php://stdin", FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES) as $line) {
      $row = json_decode($line, true, 512, JSON_THROW_ON_ERROR);
      $totals["messages_pushed"] += (int) $row["messages_pushed"];
      $totals["messages_served"] += (int) $row["messages_served"];
      $totals["pop_timeouts"] += (int) $row["pop_timeouts"];
      $totals["client_failures"] += (int) $row["client_failures"];
      $count++;
  }
  echo json_encode(["instances" => $count] + $totals, JSON_THROW_ON_ERROR), PHP_EOL;
' | PHP_PARALLEL_STRESS_ELAPSED="$elapsed" php -r '
  $summary = json_decode(trim(stream_get_contents(STDIN)), true, 512, JSON_THROW_ON_ERROR);
  $elapsed = (float) getenv("PHP_PARALLEL_STRESS_ELAPSED");
  if ($elapsed <= 0) {
      $elapsed = 1.0;
  }
  printf("stress duration: %.3fs\n", $elapsed);
  printf(
      "workers: %d (%d producers, %d consumers) across %d instances\n",
      ((int) getenv("publishers") + (int) getenv("consumers")) * (int) getenv("instances"),
      (int) getenv("publishers") * (int) getenv("instances"),
      (int) getenv("consumers") * (int) getenv("instances"),
      $summary["instances"]
  );
  printf("channels: %s\n", str_replace(",", ", ", (string) getenv("channels")));
  printf("messages pushed: %d\n", $summary["messages_pushed"]);
  printf("messages served: %d\n", $summary["messages_served"]);
  printf("pop timeouts: %d\n", $summary["pop_timeouts"]);
  printf("client failures: %d\n", $summary["client_failures"]);
  printf("push rate: %.2f msg/s\n", $summary["messages_pushed"] / $elapsed);
  printf("serve rate: %.2f msg/s\n", $summary["messages_served"] / $elapsed);
'
