#!/usr/bin/env php
<?php

declare(strict_types=1);

require_once __DIR__ . '/php_atomic_queue_lib.php';

function usage(): never
{
    fwrite(STDERR, <<<TXT
usage:
  php-socket-client.php push channel payload
  php-socket-client.php pop channel [channel2 ...] [--timeout-ms 5000]

TXT);
    exit(64);
}

$args = $argv;
array_shift($args);
if ($args === []) {
    usage();
}

$command = array_shift($args);
$socketPath = atomic_queue_default_socket_path();
$client = new AtomicQueueClient($socketPath);

try {
    if ($command === 'push') {
        if (count($args) !== 2) {
            usage();
        }
        [$channel, $payload] = $args;
        $client->push($channel, $payload);
        exit(0);
    }

    if ($command === 'pop') {
        $timeoutMs = 0;
        $channels = [];
        for ($i = 0; $i < count($args); $i++) {
            if ($args[$i] === '--timeout-ms') {
                if (!isset($args[$i + 1])) {
                    usage();
                }
                $timeoutMs = (int) $args[$i + 1];
                $i++;
                continue;
            }
            $channels[] = $args[$i];
        }

        if ($channels === []) {
            usage();
        }

        $payload = $client->pop($channels, $timeoutMs);
        fwrite(STDOUT, $payload);
        exit(0);
    }

    usage();
} catch (AtomicQueueTimeoutException $e) {
    fwrite(STDERR, "timeout\n");
    exit(2);
} catch (Throwable $e) {
    fwrite(STDERR, $e->getMessage() . "\n");
    exit(1);
}
