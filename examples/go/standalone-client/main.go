package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	opPush      byte = 1
	opPop       byte = 2
	statusOK    byte = 1
	defaultPath      = "atomic-queue/atomic-queue.sock"
)

type Client struct {
	conn net.Conn
	r    *bufio.Reader
	w    *bufio.Writer
}

func main() {
	socket := defaultSocketPath()
	client, err := Dial(socket)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer client.Close()

	payload := []byte(`{"job":"reindex","id":123}`)
	if err := client.Push("jobs", payload); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	message, err := client.Pop([]string{"jobs"}, 5*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("received: %s\n", message)
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
	if err := c.writeRequest(opPush, []string{channel}, payload, 0); err != nil {
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
	if err := c.writeRequest(opPop, channels, nil, timeout.Milliseconds()); err != nil {
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
	return status == statusOK, channel, payload, errText, nil
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

func defaultSocketPath() string {
	if value := os.Getenv("ATOMIC_QUEUE_SOCKET"); value != "" {
		return value
	}
	return filepath.Join("/run/user", fmt.Sprint(os.Getuid()), defaultPath)
}

func binaryPath() string {
	if value := os.Getenv("ATOMIC_QUEUE_BIN"); value != "" {
		return value
	}
	return filepath.Clean(filepath.Join(filepath.Dir(os.Args[0]), "..", "..", "..", "atomic-queue"))
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
