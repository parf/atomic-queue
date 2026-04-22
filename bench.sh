#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
bin_path="$repo_dir/atomic-queue"
duration="${DURATION:-10s}"
duration_seconds="${DURATION_SECONDS:-10}"
threads="${THREADS:-1000}"
publishers="${PUBLISHERS:-500}"
consumers="${CONSUMERS:-500}"
parallel_jobs_python="${PYTHON_PARALLEL_JOBS:-100}"
parallel_jobs_php="${PHP_PARALLEL_JOBS:-10}"

min_int() {
  if (( $1 < $2 )); then
    echo "$1"
  else
    echo "$2"
  fi
}

if [[ ! -x "$bin_path" ]]; then
  (cd "$repo_dir" && go build -o atomic-queue .)
fi

if ! command -v php >/dev/null 2>&1; then
  echo "php is required" >&2
  exit 1
fi

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required" >&2
  exit 1
fi

if ! command -v parallel >/dev/null 2>&1; then
  echo "gnu parallel is required" >&2
  exit 1
fi

cleanup_socket_daemon() {
  local sock="$1"
  pgrep -af "/home/parf/src/atomic-queue/atomic-queue serve --socket $sock" | awk '{print $1}' | xargs -r kill >/dev/null 2>&1 || true
}

run_with_socket() {
  local label="$1"
  shift
  local tmpdir sock
  tmpdir="$(mktemp -d)"
  sock="$tmpdir/atomic-queue.sock"
  echo
  echo "== $label =="
  (
    export ATOMIC_QUEUE_SOCKET="$sock"
    export ATOMIC_QUEUE_BIN="$bin_path"
    "$@"
  )
  cleanup_socket_daemon "$sock"
  rm -rf "$tmpdir"
}

echo "benchmark profile:"
echo "  duration: $duration"
echo "  threads: $threads ($publishers producers, $consumers consumers)"

run_with_socket "built-in" \
  "$bin_path" stress --duration "$duration" --threads "$threads"

tmp_go_bin="$(mktemp)"
rm -f "$tmp_go_bin"
go build -o "$tmp_go_bin" "$repo_dir/examples/go/stress"
run_with_socket "go-standalone" \
  "$tmp_go_bin" --duration "$duration" --threads "$threads"
rm -f "$tmp_go_bin"

run_with_socket "php-single" \
  php "$repo_dir/scripts/php-stress-test.php" --duration "$duration_seconds" --publishers "$publishers" --consumers "$consumers"

php_instances="$(min_int 10 "$(min_int "$publishers" "$consumers")")"
php_instance_publishers=$((publishers / php_instances))
php_instance_consumers=$((consumers / php_instances))
run_with_socket "php-parallel" \
  env INSTANCES="$php_instances" DURATION="$duration_seconds" PUBLISHERS="$php_instance_publishers" CONSUMERS="$php_instance_consumers" PARALLEL_JOBS="$parallel_jobs_php" \
  "$repo_dir/scripts/php-parallel-stress.sh"

run_with_socket "python-single" \
  python3 "$repo_dir/examples/python/stress_test.py" --duration "$duration_seconds" --threads "$threads"

python_instances="$(min_int 100 "$(min_int "$publishers" "$consumers")")"
python_instance_publishers=$((publishers / python_instances))
python_instance_consumers=$((consumers / python_instances))
run_with_socket "python-parallel" \
  env INSTANCES="$python_instances" DURATION="$duration_seconds" PUBLISHERS="$python_instance_publishers" CONSUMERS="$python_instance_consumers" PARALLEL_JOBS="$parallel_jobs_python" \
  "$repo_dir/scripts/python-parallel-stress.sh"
