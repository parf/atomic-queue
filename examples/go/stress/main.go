package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	var (
		socket      = flag.String("socket", defaultSocketPath(), "unix socket path")
		duration    = flag.Duration("duration", 10*time.Second, "stress duration")
		threads     = flag.Int("threads", 100, "total worker count")
		publishers  = flag.Int("publishers", 0, "publisher worker count")
		consumers   = flag.Int("consumers", 0, "consumer worker count")
		channelsCSV = flag.String("channels", "stress-a,stress-b,stress-c,stress-d", "comma-separated channels")
		timeout     = flag.Duration("pop-timeout", 200*time.Millisecond, "pop timeout")
		payloadSize = flag.Int("payload-size", 128, "payload size")
		format      = flag.String("format", "text", "output format: text or json")
	)
	flag.Parse()

	channels := splitCSV(*channelsCSV)
	pubCount, conCount := stressCounts(*threads, *publishers, *consumers)

	warm, err := Dial(*socket)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_, _ = warm.Pop(channels, time.Millisecond)
	_ = warm.Close()

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()

	start := time.Now()
	var pushed atomic.Uint64
	var served atomic.Uint64
	var timeouts atomic.Uint64
	var failures atomic.Uint64

	var wg sync.WaitGroup
	for workerID := 0; workerID < pubCount; workerID++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			client, err := Dial(*socket)
			if err != nil {
				failures.Add(1)
				return
			}
			defer client.Close()

			channelIndex := workerID % len(channels)
			seq := 0
			for ctx.Err() == nil {
				if err := client.Push(channels[channelIndex], makePayload(workerID, seq, *payloadSize)); err != nil {
					failures.Add(1)
					return
				}
				channelIndex = (channelIndex + 1) % len(channels)
				seq++
				pushed.Add(1)
			}
		}(workerID)
	}

	for i := 0; i < conCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client, err := Dial(*socket)
			if err != nil {
				failures.Add(1)
				return
			}
			defer client.Close()

			for ctx.Err() == nil {
				_, err := client.Pop(channels, *timeout)
				if err == nil {
					served.Add(1)
					continue
				}
				if err.Error() == "timeout" {
					timeouts.Add(1)
					continue
				}
				failures.Add(1)
				return
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 {
		elapsed = 1
	}

	result := map[string]any{
		"duration_seconds": elapsed,
		"threads":          pubCount + conCount,
		"publishers":       pubCount,
		"consumers":        conCount,
		"channels":         channels,
		"messages_pushed":  pushed.Load(),
		"messages_served":  served.Load(),
		"pop_timeouts":     timeouts.Load(),
		"client_failures":  failures.Load(),
		"push_rate":        float64(pushed.Load()) / elapsed,
		"serve_rate":       float64(served.Load()) / elapsed,
	}

	if *format == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(result)
		return
	}

	fmt.Printf("stress duration: %.3fs\n", elapsed)
	fmt.Printf("threads: %d (%d producers, %d consumers)\n", pubCount+conCount, pubCount, conCount)
	fmt.Printf("channels: %s\n", strings.Join(channels, ", "))
	fmt.Printf("messages pushed: %d\n", pushed.Load())
	fmt.Printf("messages served: %d\n", served.Load())
	fmt.Printf("pop timeouts: %d\n", timeouts.Load())
	fmt.Printf("client failures: %d\n", failures.Load())
	fmt.Printf("push rate: %.2f msg/s\n", float64(pushed.Load())/elapsed)
	fmt.Printf("serve rate: %.2f msg/s\n", float64(served.Load())/elapsed)
}

func stressCounts(threads, publishers, consumers int) (int, int) {
	if publishers > 0 && consumers > 0 {
		return publishers, consumers
	}
	publishers = threads / 2
	if publishers < 1 {
		publishers = 1
	}
	consumers = threads - publishers
	if consumers < 1 {
		consumers = 1
	}
	return publishers, consumers
}

func makePayload(workerID, seq, size int) []byte {
	prefix := []byte(fmt.Sprintf("worker=%d seq=%d ", workerID, seq))
	if len(prefix) >= size {
		return prefix[:size]
	}
	payload := make([]byte, size)
	copy(payload, prefix)
	const alphabet = "0123456789abcdef"
	for i := len(prefix); i < size; i++ {
		payload[i] = alphabet[(workerID+seq+i)&0x0f]
	}
	return payload
}

func splitCSV(value string) []string {
	if value == "" {
		return []string{"stress-a"}
	}
	var out []string
	start := 0
	for i := 0; i <= len(value); i++ {
		if i != len(value) && value[i] != ',' {
			continue
		}
		part := value[start:i]
		for len(part) > 0 && part[0] == ' ' {
			part = part[1:]
		}
		for len(part) > 0 && part[len(part)-1] == ' ' {
			part = part[:len(part)-1]
		}
		if part != "" {
			out = append(out, part)
		}
		start = i + 1
	}
	if len(out) == 0 {
		return []string{"stress-a"}
	}
	return out
}

func defaultSocketPath() string {
	if value := os.Getenv("ATOMIC_QUEUE_SOCKET"); value != "" {
		return value
	}
	return filepath.Join("/run/user", fmt.Sprint(os.Getuid()), "atomic-queue", "atomic-queue.sock")
}

func binaryPath() string {
	if value := os.Getenv("ATOMIC_QUEUE_BIN"); value != "" {
		return value
	}
	return filepath.Clean(filepath.Join(filepath.Dir(os.Args[0]), "..", "..", "..", "..", "atomic-queue"))
}

type Client struct {
	conn net.Conn
	r    *bufio.Reader
	w    *bufio.Writer
}

func Dial(socketPath string) (*Client, error) {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		if ensureErr := ensureDaemon(socketPath, err); ensureErr != nil {
			return nil, ensureErr
		}
		conn, err = net.DialTimeout("unix", socketPath, 2*time.Second)
		if err != nil {
			return nil, err
		}
	}
	return &Client{
		conn: conn,
		r:    bufio.NewReader(conn),
		w:    bufio.NewWriter(conn),
	}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) Push(channel string, payload []byte) error {
	if err := c.writeRequest(1, []string{channel}, payload, 0); err != nil {
		return err
	}
	ok, _, _, errText, err := c.readResponse()
	if err != nil {
		return err
	}
	if !ok {
		return errors.New(errText)
	}
	return nil
}

func (c *Client) Pop(channels []string, timeout time.Duration) ([]byte, error) {
	if err := c.writeRequest(2, channels, nil, timeout.Milliseconds()); err != nil {
		return nil, err
	}
	ok, _, payload, errText, err := c.readResponse()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New(errText)
	}
	return payload, nil
}

func (c *Client) writeRequest(op byte, channels []string, payload []byte, timeoutMS int64) error {
	if err := c.w.WriteByte(op); err != nil {
		return err
	}
	if err := binary.Write(c.w, binary.BigEndian, timeoutMS); err != nil {
		return err
	}
	if err := binary.Write(c.w, binary.BigEndian, uint16(len(channels))); err != nil {
		return err
	}
	for _, channel := range channels {
		if err := binary.Write(c.w, binary.BigEndian, uint16(len(channel))); err != nil {
			return err
		}
		if _, err := io.WriteString(c.w, channel); err != nil {
			return err
		}
	}
	if err := binary.Write(c.w, binary.BigEndian, uint32(len(payload))); err != nil {
		return err
	}
	if _, err := c.w.Write(payload); err != nil {
		return err
	}
	return c.w.Flush()
}

func (c *Client) readResponse() (bool, string, []byte, string, error) {
	status, err := c.r.ReadByte()
	if err != nil {
		return false, "", nil, "", err
	}
	channel, err := readString16(c.r)
	if err != nil {
		return false, "", nil, "", err
	}
	payload, err := readBytes32(c.r)
	if err != nil {
		return false, "", nil, "", err
	}
	errText, err := readString32(c.r)
	if err != nil {
		return false, "", nil, "", err
	}
	return status == 1, channel, payload, errText, nil
}

func readString16(r io.Reader) (string, error) {
	var n uint16
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return "", err
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func readString32(r io.Reader) (string, error) {
	buf, err := readBytes32(r)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

func readBytes32(r io.Reader) ([]byte, error) {
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func ensureDaemon(socketPath string, cause error) error {
	var netErr *net.OpError
	if !errors.As(cause, &netErr) {
		return cause
	}
	cmd := exec.Command(binaryPath(), "serve", "--socket", socketPath)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", socketPath, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not start at %s", socketPath)
}
