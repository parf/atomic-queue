<?php

declare(strict_types=1);

class AtomicQueueException extends RuntimeException
{
}

final class AtomicQueueTimeoutException extends AtomicQueueException
{
}

final class AtomicQueueClient
{
    private const OP_PUSH = 1;
    private const OP_POP = 2;
    private const STATUS_OK = 1;
    private const STATUS_ERROR = 2;

    /** @var resource */
    private $stream;

    public function __construct(private readonly string $socketPath)
    {
        $this->stream = $this->connect(true);
    }

    /**
     * @return resource
     */
    private function connect(bool $allowAutostart)
    {
        $stream = @stream_socket_client(
            'unix://' . $this->socketPath,
            $errno,
            $errstr,
            2.0,
            STREAM_CLIENT_CONNECT
        );

        if (!is_resource($stream)) {
            if ($allowAutostart && ($errno === 2 || $errno === 111)) {
                atomic_queue_ensure_daemon($this->socketPath);
                return $this->connect(false);
            }
            throw new AtomicQueueException(sprintf(
                'connect to daemon failed: %s (%d) at %s',
                $errstr ?: 'unknown error',
                $errno,
                $this->socketPath
            ));
        }

        stream_set_write_buffer($stream, 0);
        stream_set_read_buffer($stream, 0);
        return $stream;
    }

    public function __destruct()
    {
        if (is_resource($this->stream)) {
            fclose($this->stream);
        }
    }

    public function push(string $channel, string $payload): void
    {
        $this->writeRequest(self::OP_PUSH, [$channel], $payload, 0);
        $response = $this->readResponse();
        if (!$response['ok']) {
            throw new AtomicQueueException($response['error']);
        }
    }

    public function pop(array $channels, int $timeoutMs = 0): string
    {
        $this->writeRequest(self::OP_POP, $channels, '', $timeoutMs);
        $response = $this->readResponse();
        if ($response['ok']) {
            return $response['payload'];
        }
        if ($response['error'] === 'timeout') {
            throw new AtomicQueueTimeoutException('timeout');
        }
        throw new AtomicQueueException($response['error']);
    }

    private function writeRequest(int $op, array $channels, string $payload, int $timeoutMs): void
    {
        $frame = chr($op);
        $frame .= self::packInt64BE($timeoutMs);
        $frame .= pack('n', count($channels));
        foreach ($channels as $channel) {
            $frame .= pack('n', strlen($channel));
            $frame .= $channel;
        }
        $frame .= pack('N', strlen($payload));
        $frame .= $payload;
        $this->writeAll($frame);
    }

    /**
     * @return array{ok: bool, channel: string, payload: string, error: string}
     */
    private function readResponse(): array
    {
        $status = ord($this->readExact(1));
        $channel = $this->readString16();
        $payload = $this->readBytes32();
        $error = $this->readString32();

        return [
            'ok' => $status === self::STATUS_OK,
            'channel' => $channel,
            'payload' => $payload,
            'error' => $error,
        ];
    }

    private function readString16(): string
    {
        $header = unpack('nlen', $this->readExact(2));
        return $this->readExact($header['len']);
    }

    private function readString32(): string
    {
        return $this->readBytes32();
    }

    private function readBytes32(): string
    {
        $header = unpack('Nlen', $this->readExact(4));
        return $this->readExact($header['len']);
    }

    private function readExact(int $length): string
    {
        if ($length === 0) {
            return '';
        }

        $buffer = '';
        while (strlen($buffer) < $length) {
            $chunk = fread($this->stream, $length - strlen($buffer));
            if ($chunk === false || $chunk === '') {
                throw new AtomicQueueException('unexpected EOF from daemon');
            }
            $buffer .= $chunk;
        }
        return $buffer;
    }

    private function writeAll(string $data): void
    {
        $written = 0;
        $length = strlen($data);
        while ($written < $length) {
            $n = fwrite($this->stream, substr($data, $written));
            if ($n === false || $n === 0) {
                throw new AtomicQueueException('write to daemon failed');
            }
            $written += $n;
        }
    }

    private static function packInt64BE(int $value): string
    {
        $hi = ($value >> 32) & 0xffffffff;
        $lo = $value & 0xffffffff;
        return pack('NN', $hi, $lo);
    }
}

function atomic_queue_default_socket_path(): string
{
    $override = getenv('ATOMIC_QUEUE_SOCKET');
    if (is_string($override) && $override !== '') {
        return $override;
    }
    return sprintf('/run/user/%d/atomic-queue/atomic-queue.sock', posix_getuid());
}

function atomic_queue_binary_path(): string
{
    $override = getenv('ATOMIC_QUEUE_BIN');
    if (is_string($override) && $override !== '') {
        return $override;
    }
    return dirname(__DIR__) . '/atomic-queue';
}

function atomic_queue_ensure_daemon(string $socketPath): void
{
    $bin = atomic_queue_binary_path();
    if (!function_exists('pcntl_fork') || !function_exists('pcntl_exec')) {
        throw new AtomicQueueException(sprintf('cannot autostart daemon at %s: pcntl support required', $socketPath));
    }

    $pid = pcntl_fork();
    if ($pid === -1) {
        throw new AtomicQueueException(sprintf('start daemon failed at %s', $socketPath));
    }
    if ($pid === 0) {
        if (function_exists('posix_setsid')) {
            @posix_setsid();
        }
        pcntl_exec($bin, ['serve', '--socket', $socketPath]);
        exit(1);
    }

    $deadline = microtime(true) + 2.0;
    do {
        $stream = @stream_socket_client(
            'unix://' . $socketPath,
            $errno,
            $errstr,
            0.1,
            STREAM_CLIENT_CONNECT
        );
        if (is_resource($stream)) {
            fclose($stream);
            return;
        }
        usleep(50_000);
    } while (microtime(true) < $deadline);

    throw new AtomicQueueException(sprintf(
        "daemon did not start at %s\ntry:\n  atomic-queue serve --socket \"%s\"",
        $socketPath,
        $socketPath
    ));
}
