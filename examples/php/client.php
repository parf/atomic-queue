#!/usr/bin/env php
<?php

declare(strict_types=1);

require_once __DIR__ . '/../../scripts/php_atomic_queue_lib.php';

$client = new AtomicQueueClient(atomic_queue_default_socket_path());

$payload = json_encode([
    'job' => 'reindex',
    'id' => 123,
], JSON_UNESCAPED_SLASHES | JSON_THROW_ON_ERROR);

$client->push('jobs', $payload);

$message = $client->pop(['jobs'], 5000);
$data = json_decode($message, true, 512, JSON_THROW_ON_ERROR);

var_dump($data);
