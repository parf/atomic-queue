#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bin_path="$repo_dir/atomic-queue"

if [[ ! -x "$bin_path" ]]; then
  echo "building atomic-queue binary"
  (cd "$repo_dir" && go build -o atomic-queue .)
fi

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

export ATOMIC_QUEUE_SOCKET="$tmpdir/atomic-queue.sock"
export ATOMIC_QUEUE_BIN="$bin_path"
export ATOMIC_QUEUE_REPO_DIR="$repo_dir"

php <<'PHP'
<?php

$socket = getenv('ATOMIC_QUEUE_SOCKET');
$repoDir = getenv('ATOMIC_QUEUE_REPO_DIR');

if (!$socket || !$repoDir) {
    fwrite(STDERR, "missing ATOMIC_QUEUE_SOCKET or ATOMIC_QUEUE_REPO_DIR\n");
    exit(1);
}

require_once $repoDir . '/scripts/php_atomic_queue_lib.php';

$client = new AtomicQueueClient($socket);

$payload = json_encode([
    'job' => 'reindex',
    'id' => 123,
    'source' => 'php-integration-smoke',
], JSON_UNESCAPED_SLASHES | JSON_THROW_ON_ERROR);

try {
    $client->push('jobs', $payload);
    $message = $client->pop(['jobs'], 5000);
} catch (Throwable $e) {
    fwrite(STDERR, $e->getMessage() . "\n");
    exit(1);
}

$data = json_decode($message, true, 512, JSON_THROW_ON_ERROR);
if (($data['job'] ?? null) !== 'reindex' || ($data['id'] ?? null) !== 123) {
    fwrite(STDERR, "unexpected payload: " . $message . "\n");
    exit(1);
}

echo "php-integration-smoke: ok\n";
PHP
