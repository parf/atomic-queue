#!/usr/bin/env php
<?php

declare(strict_types=1);

require_once __DIR__ . '/php_atomic_queue_lib.php';

if (!function_exists('pcntl_fork') || !function_exists('stream_socket_pair')) {
    fwrite(STDERR, "php-stress-test.php requires pcntl and stream_socket_pair support\n");
    exit(1);
}

function php_stress_usage(): never
{
    fwrite(STDERR, <<<TXT
usage:
  php-stress-test.php [--duration 10] [--workers 100] [--channels jobs,fast,slow] [--pop-timeout-ms 200] [--payload-size 128]

TXT);
    exit(64);
}

function parse_php_stress_args(array $args): array
{
    $cfg = [
        'duration' => 10,
        'workers' => 100,
        'channels' => ['stress-a', 'stress-b', 'stress-c', 'stress-d'],
        'pop_timeout_ms' => 200,
        'payload_size' => 128,
    ];

    for ($i = 0; $i < count($args); $i++) {
        $arg = $args[$i];
        switch ($arg) {
            case '--duration':
                if (!isset($args[$i + 1])) {
                    php_stress_usage();
                }
                $cfg['duration'] = max(1, (int) $args[++$i]);
                break;
            case '--workers':
                if (!isset($args[$i + 1])) {
                    php_stress_usage();
                }
                $cfg['workers'] = max(1, (int) $args[++$i]);
                break;
            case '--channels':
                if (!isset($args[$i + 1])) {
                    php_stress_usage();
                }
                $cfg['channels'] = array_values(array_filter(array_map('trim', explode(',', $args[++$i]))));
                break;
            case '--pop-timeout-ms':
                if (!isset($args[$i + 1])) {
                    php_stress_usage();
                }
                $cfg['pop_timeout_ms'] = max(1, (int) $args[++$i]);
                break;
            case '--payload-size':
                if (!isset($args[$i + 1])) {
                    php_stress_usage();
                }
                $cfg['payload_size'] = max(16, (int) $args[++$i]);
                break;
            default:
                php_stress_usage();
        }
    }

    if ($cfg['channels'] === []) {
        $cfg['channels'] = ['stress-a'];
    }

    return $cfg;
}

function build_php_stress_payload(int $workerId, int $seq, int $payloadSize): string
{
    $prefix = sprintf('worker=%d seq=%d ', $workerId, $seq);
    if (strlen($prefix) >= $payloadSize) {
        return substr($prefix, 0, $payloadSize);
    }
    $bodyBytes = random_bytes((int) ceil(($payloadSize - strlen($prefix)) / 2));
    $body = substr(bin2hex($bodyBytes), 0, $payloadSize - strlen($prefix));
    return $prefix . $body;
}

function spawn_php_stress_worker(string $role, int $workerId, array $cfg, string $socketPath, int $deadlineNs)
{
    $pair = stream_socket_pair(STREAM_PF_UNIX, STREAM_SOCK_STREAM, 0);
    if ($pair === false) {
        throw new RuntimeException('stream_socket_pair failed');
    }

    $pid = pcntl_fork();
    if ($pid === -1) {
        throw new RuntimeException('pcntl_fork failed');
    }

    if ($pid === 0) {
        fclose($pair[0]);
        $counts = ['pushed' => 0, 'served' => 0, 'timeouts' => 0, 'failures' => 0];
        try {
            $client = new AtomicQueueClient($socketPath);
            $channels = $cfg['channels'];
            $seq = 0;
            while (hrtime(true) < $deadlineNs) {
                if ($role === 'producer') {
                    $channel = $channels[array_rand($channels)];
                    $client->push($channel, build_php_stress_payload($workerId, $seq, $cfg['payload_size']));
                    $counts['pushed']++;
                    $seq++;
                    continue;
                }

                try {
                    $client->pop($channels, $cfg['pop_timeout_ms']);
                    $counts['served']++;
                } catch (AtomicQueueTimeoutException) {
                    $counts['timeouts']++;
                }
            }
        } catch (Throwable) {
            $counts['failures']++;
        }

        fwrite($pair[1], json_encode($counts, JSON_THROW_ON_ERROR));
        fclose($pair[1]);
        exit(0);
    }

    fclose($pair[1]);
    return [$pid, $pair[0]];
}

$args = $argv;
array_shift($args);
$cfg = parse_php_stress_args($args);
$socketPath = atomic_queue_default_socket_path();

// Warm/start the daemon once.
$warm = new AtomicQueueClient($socketPath);
try {
    $warm->pop($cfg['channels'], 1);
} catch (AtomicQueueTimeoutException) {
}
unset($warm);

$start = hrtime(true);
$deadlineNs = $start + ($cfg['duration'] * 1_000_000_000);

$producerCount = max(1, intdiv($cfg['workers'], 2));
$consumerCount = max(1, $cfg['workers'] - $producerCount);

$children = [];
for ($i = 0; $i < $producerCount; $i++) {
    $children[] = spawn_php_stress_worker('producer', $i, $cfg, $socketPath, $deadlineNs);
}
for ($i = 0; $i < $consumerCount; $i++) {
    $children[] = spawn_php_stress_worker('consumer', $i, $cfg, $socketPath, $deadlineNs);
}

$totals = ['pushed' => 0, 'served' => 0, 'timeouts' => 0, 'failures' => 0];
foreach ($children as [$pid, $stream]) {
    $json = stream_get_contents($stream);
    fclose($stream);
    pcntl_waitpid($pid, $status);
    if (!is_string($json) || $json === '') {
        $totals['failures']++;
        continue;
    }
    $counts = json_decode($json, true, flags: JSON_THROW_ON_ERROR);
    $totals['pushed'] += (int) ($counts['pushed'] ?? 0);
    $totals['served'] += (int) ($counts['served'] ?? 0);
    $totals['timeouts'] += (int) ($counts['timeouts'] ?? 0);
    $totals['failures'] += (int) ($counts['failures'] ?? 0);
}

$elapsed = (hrtime(true) - $start) / 1_000_000_000;
if ($elapsed <= 0) {
    $elapsed = 1.0;
}

printf("stress duration: %.3fs\n", $elapsed);
printf("workers: %d (%d producers, %d consumers)\n", $cfg['workers'], $producerCount, $consumerCount);
printf("channels: %s\n", implode(', ', $cfg['channels']));
printf("messages pushed: %d\n", $totals['pushed']);
printf("messages served: %d\n", $totals['served']);
printf("pop timeouts: %d\n", $totals['timeouts']);
printf("client failures: %d\n", $totals['failures']);
printf("push rate: %.2f msg/s\n", $totals['pushed'] / $elapsed);
printf("serve rate: %.2f msg/s\n", $totals['served'] / $elapsed);
