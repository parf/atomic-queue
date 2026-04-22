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
  php-stress-test.php [--duration 10] [--workers 100] [--publishers 50] [--consumers 50] [--channels jobs,fast,slow] [--pop-timeout-ms 200] [--payload-size 128] [--format text|json]

TXT);
    exit(64);
}

function parse_php_stress_args(array $args): array
{
    $cfg = [
        'duration' => 10,
        'workers' => 100,
        'publishers' => null,
        'consumers' => null,
        'channels' => ['stress-a', 'stress-b', 'stress-c', 'stress-d'],
        'pop_timeout_ms' => 200,
        'payload_size' => 128,
        'format' => 'text',
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
            case '--publishers':
                if (!isset($args[$i + 1])) {
                    php_stress_usage();
                }
                $cfg['publishers'] = max(1, (int) $args[++$i]);
                break;
            case '--consumers':
                if (!isset($args[$i + 1])) {
                    php_stress_usage();
                }
                $cfg['consumers'] = max(1, (int) $args[++$i]);
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
            case '--format':
                if (!isset($args[$i + 1])) {
                    php_stress_usage();
                }
                $cfg['format'] = $args[++$i];
                break;
            default:
                php_stress_usage();
        }
    }

    if ($cfg['channels'] === []) {
        $cfg['channels'] = ['stress-a'];
    }

    if ($cfg['publishers'] === null || $cfg['consumers'] === null) {
        $cfg['publishers'] = max(1, intdiv($cfg['workers'], 2));
        $cfg['consumers'] = max(1, $cfg['workers'] - $cfg['publishers']);
    } else {
        $cfg['workers'] = $cfg['publishers'] + $cfg['consumers'];
    }

    if ($cfg['format'] !== 'text' && $cfg['format'] !== 'json') {
        php_stress_usage();
    }

    return $cfg;
}

function build_php_stress_payload(int $workerId, int $seq, int $payloadSize): string
{
    $prefix = sprintf('worker=%d seq=%d ', $workerId, $seq);
    if (strlen($prefix) >= $payloadSize) {
        return substr($prefix, 0, $payloadSize);
    }

    $payload = str_pad($prefix, $payloadSize, '0');
    $hex = '0123456789abcdef';
    for ($i = strlen($prefix); $i < $payloadSize; $i++) {
        $payload[$i] = $hex[($workerId + $seq + $i) & 0x0f];
    }
    return $payload;
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
            $nextChannel = $workerId % count($channels);
            while (hrtime(true) < $deadlineNs) {
                if ($role === 'producer') {
                    $channel = $channels[$nextChannel];
                    $nextChannel++;
                    if ($nextChannel === count($channels)) {
                        $nextChannel = 0;
                    }
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

$producerCount = (int) $cfg['publishers'];
$consumerCount = (int) $cfg['consumers'];

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

$result = [
    'duration_seconds' => $elapsed,
    'workers' => (int) $cfg['workers'],
    'publishers' => $producerCount,
    'consumers' => $consumerCount,
    'channels' => array_values($cfg['channels']),
    'messages_pushed' => $totals['pushed'],
    'messages_served' => $totals['served'],
    'pop_timeouts' => $totals['timeouts'],
    'client_failures' => $totals['failures'],
    'push_rate' => $totals['pushed'] / $elapsed,
    'serve_rate' => $totals['served'] / $elapsed,
];

if ($cfg['format'] === 'json') {
    echo json_encode($result, JSON_THROW_ON_ERROR) . PHP_EOL;
    exit(0);
}

printf("stress duration: %.3fs\n", $result['duration_seconds']);
printf("workers: %d (%d producers, %d consumers)\n", $result['workers'], $result['publishers'], $result['consumers']);
printf("channels: %s\n", implode(', ', $result['channels']));
printf("messages pushed: %d\n", $result['messages_pushed']);
printf("messages served: %d\n", $result['messages_served']);
printf("pop timeouts: %d\n", $result['pop_timeouts']);
printf("client failures: %d\n", $result['client_failures']);
printf("push rate: %.2f msg/s\n", $result['push_rate']);
printf("serve rate: %.2f msg/s\n", $result['serve_rate']);
