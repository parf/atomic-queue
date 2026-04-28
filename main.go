package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	exitOK                = 0
	exitRuntime           = 1
	exitTimeout           = 2
	exitUsage             = 64
	defaultMaxBytes       = 256 << 20
	defaultMaxQueuedBytes = int64(4) << 30
	clientDialTimeout     = 200 * time.Millisecond
	daemonStartTimeout    = 2 * time.Second
	socketProbeDelay      = 50 * time.Millisecond
)

var channelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type request struct {
	Op        string
	Channels  []string
	Payload   []byte
	TimeoutMS int64
}

type response struct {
	OK      bool
	Channel string
	Payload []byte
	Error   string
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		printUsage(os.Stdout)
		return exitOK
	}

	switch args[0] {
	case "push":
		return runPush(args[1:])
	case "pop":
		return runPop(args[1:])
	case "serve":
		return runServe(args[1:])
	case "stress":
		return runStress(args[1:])
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return exitOK
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", args[0])
		printUsage(os.Stderr)
		return exitUsage
	}
}

func runPush(args []string) int {
	socket, positionals, readStdin, err := parsePushArgs(args)
	if err != nil {
		return usageError("push", err)
	}
	if readStdin {
		if len(positionals) != 1 {
			return usageError("push", errors.New("usage: atomic-queue push [--socket path] [--stdin] channel [payload]"))
		}
	} else if len(positionals) != 2 {
		return usageError("push", errors.New("usage: atomic-queue push [--socket path] [--stdin] channel [payload]"))
	}

	var payload []byte
	if readStdin {
		payload, err = io.ReadAll(os.Stdin)
		if err != nil {
			return runtimeError(fmt.Errorf("read stdin: %w", err))
		}
	} else {
		payload = []byte(positionals[1])
	}

	req := request{
		Op:       "push",
		Channels: []string{positionals[0]},
		Payload:  payload,
	}
	if err := validateChannel(req.Channels[0]); err != nil {
		return runtimeError(err)
	}
	if err := validatePayload(req.Payload, defaultMaxBytes); err != nil {
		return runtimeError(err)
	}

	resp, err := roundTrip(socket, req, true)
	if err != nil {
		return clientError(err)
	}
	if !resp.OK {
		return runtimeError(errors.New(resp.Error))
	}
	return exitOK
}

func runPop(args []string) int {
	socket, positionals, timeout, err := parsePopArgs(args)
	if err != nil {
		return usageError("pop", err)
	}
	if len(positionals) == 0 {
		return usageError("pop", errors.New("usage: atomic-queue pop [--socket path] [--timeout d] channel..."))
	}

	for _, channel := range positionals {
		if err := validateChannel(channel); err != nil {
			return runtimeError(err)
		}
	}

	req := request{
		Op:       "pop",
		Channels: positionals,
	}
	if timeout > 0 {
		req.TimeoutMS = timeout.Milliseconds()
	}

	resp, err := roundTrip(socket, req, true)
	if err != nil {
		return clientError(err)
	}
	if !resp.OK {
		if resp.Error == ErrTimeout.Error() {
			fmt.Fprintln(os.Stderr, resp.Error)
			return exitTimeout
		}
		return runtimeError(errors.New(resp.Error))
	}

	if _, err := os.Stdout.Write(resp.Payload); err != nil {
		return runtimeError(fmt.Errorf("write stdout: %w", err))
	}
	return exitOK
}

func runServe(args []string) int {
	socket, maxQueuedBytes, err := parseServeArgs(args)
	if err != nil {
		return usageError("serve", err)
	}

	if err := serve(socket, maxQueuedBytes); err != nil {
		if hint := socketSuggestion(socket); hint != "" {
			return runtimeError(fmt.Errorf("%w\ntry:\n  atomic-queue serve --socket %q\nor:\n  ATOMIC_QUEUE_SOCKET=%q atomic-queue serve", err, hint, hint))
		}
		return runtimeError(err)
	}
	return exitOK
}

func serve(socketPath string, maxQueuedBytes int64) error {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}
	if err := removeStaleSocket(socketPath); err != nil {
		return err
	}

	// Limit socket permissions at creation time before any local peer can connect.
	prevUmask := syscall.Umask(0o077)
	listener, err := net.Listen("unix", socketPath)
	syscall.Umask(prevUmask)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", socketPath, err)
	}
	defer os.Remove(socketPath)
	defer listener.Close()
	if err := os.Chmod(socketPath, 0o600); err != nil {
		return fmt.Errorf("chmod socket: %w", err)
	}

	broker := NewBroker(defaultMaxBytes, maxQueuedBytes)

	watcher, err := newConnWatcher()
	if err != nil {
		return fmt.Errorf("conn watcher: %w", err)
	}
	defer watcher.close()
	go watcher.run()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept: %w", err)
		}
		go handleConn(ctx, watcher, conn, broker)
	}
}

func handleConn(serverCtx context.Context, watcher *connWatcher, conn net.Conn, broker *Broker) {
	connCtx, cancel := context.WithCancel(serverCtx)
	defer cancel()
	defer conn.Close()

	// Register epoll watch AFTER deferring conn.Close() so the unregister
	// defer runs first (LIFO), guaranteeing we deregister the fd while
	// it is still valid and not yet reusable by the kernel.
	fd, fdErr := connFD(conn)
	if fdErr == nil {
		if err := watcher.register(fd, cancel); err == nil {
			defer watcher.unregister(fd)
		}
	}

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	for {
		req, err := readRequest(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			_ = writeResponse(writer, response{Error: fmt.Sprintf("decode request: %v", err)})
			return
		}

		switch req.Op {
		case "push":
			if len(req.Channels) != 1 {
				_ = writeResponse(writer, response{Error: "push requires exactly one channel"})
				return
			}
			if err := broker.PushOwned(connCtx, req.Channels[0], req.Payload); err != nil {
				_ = writeResponse(writer, response{Error: err.Error()})
				return
			}
			if err := writeResponse(writer, response{OK: true}); err != nil {
				return
			}
		case "pop":
			timeout := time.Duration(req.TimeoutMS) * time.Millisecond
			ctx, cancel := timeoutContext(timeout)
			msg, err := broker.Pop(ctx, req.Channels)
			cancel()
			if err != nil {
				_ = writeResponse(writer, response{Error: err.Error()})
				return
			}
			if err := writeResponse(writer, response{
				OK:      true,
				Channel: msg.Channel,
				Payload: msg.Payload,
			}); err != nil {
				return
			}
		default:
			_ = writeResponse(writer, response{Error: fmt.Sprintf("unknown op %q", req.Op)})
			return
		}
	}
}

func roundTrip(socketPath string, req request, autoStart bool) (response, error) {
	client, err := newRPCClient(socketPath, autoStart)
	if err != nil {
		return response{}, err
	}
	defer client.Close()

	return client.Do(req)
}

type rpcClient struct {
	conn net.Conn
	r    *bufio.Reader
	w    *bufio.Writer
}

func newRPCClient(socketPath string, autoStart bool) (*rpcClient, error) {
	conn, err := net.DialTimeout("unix", socketPath, clientDialTimeout)
	if err != nil && autoStart && shouldStartDaemon(err) {
		if startErr := ensureDaemon(socketPath); startErr != nil {
			return nil, startErr
		}
		conn, err = net.DialTimeout("unix", socketPath, clientDialTimeout)
	}
	if err != nil {
		return nil, fmt.Errorf("connect to daemon: %w", err)
	}
	return &rpcClient{
		conn: conn,
		r:    bufio.NewReader(conn),
		w:    bufio.NewWriter(conn),
	}, nil
}

func (c *rpcClient) Do(req request) (response, error) {
	if err := writeRequest(c.w, req); err != nil {
		return response{}, fmt.Errorf("send request: %w", err)
	}
	resp, err := readResponse(c.r)
	if err != nil {
		return response{}, fmt.Errorf("read response: %w", err)
	}
	return resp, nil
}

func (c *rpcClient) Push(channel string, payload []byte) error {
	if err := writeRequest(c.w, request{
		Op:       "push",
		Channels: []string{channel},
		Payload:  payload,
	}); err != nil {
		return fmt.Errorf("send push: %w", err)
	}
	resp, err := readResponse(c.r)
	if err != nil {
		return fmt.Errorf("read push response: %w", err)
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}
	return nil
}

func (c *rpcClient) Pop(channels []string, timeoutMS int64) (response, error) {
	if err := writeRequest(c.w, request{
		Op:        "pop",
		Channels:  channels,
		TimeoutMS: timeoutMS,
	}); err != nil {
		return response{}, fmt.Errorf("send pop: %w", err)
	}
	resp, err := readResponse(c.r)
	if err != nil {
		return response{}, fmt.Errorf("read pop response: %w", err)
	}
	return resp, nil
}

func (c *rpcClient) Close() error {
	return c.conn.Close()
}

func ensureDaemon(socketPath string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	cmd := exec.Command(self, "serve", "--socket", socketPath)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(daemonStartTimeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", socketPath, clientDialTimeout)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(socketProbeDelay)
	}

	return fmt.Errorf(
		"daemon did not start at %s\ntry:\n  %s serve --socket %q\nor:\n  ATOMIC_QUEUE_SOCKET=%q %s serve",
		socketPath,
		filepath.Base(self),
		suggestedSocketPath(socketPath),
		suggestedSocketPath(socketPath),
		filepath.Base(self),
	)
}

func shouldStartDaemon(err error) bool {
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		return false
	}
	return errors.Is(opErr.Err, syscall.ENOENT) || errors.Is(opErr.Err, syscall.ECONNREFUSED)
}

func removeStaleSocket(socketPath string) error {
	info, err := os.Stat(socketPath)
	if err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("%s exists and is not a socket", socketPath)
		}
		conn, dialErr := net.DialTimeout("unix", socketPath, 100*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			return fmt.Errorf("daemon already running at %s", socketPath)
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, syscall.ENOENT) {
			return fmt.Errorf("check existing socket: %w", dialErr)
		}
		if err := os.Remove(socketPath); err != nil {
			return fmt.Errorf("remove stale socket: %w", err)
		}
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("stat socket: %w", err)
}

func validateChannel(channel string) error {
	if !channelPattern.MatchString(channel) {
		return fmt.Errorf("invalid channel %q: use [A-Za-z0-9._-], max length 128", channel)
	}
	return nil
}

func validatePayload(payload []byte, maxBytes int) error {
	if len(payload) > maxBytes {
		return fmt.Errorf("payload exceeds maximum size of %d bytes", maxBytes)
	}
	return nil
}

func defaultSocketPath() string {
	if socket := os.Getenv("ATOMIC_QUEUE_SOCKET"); socket != "" {
		return socket
	}
	return filepath.Join("/run/user", fmt.Sprintf("%d", os.Getuid()), "atomic-queue", "atomic-queue.sock")
}

func suggestedSocketPath(current string) string {
	if hint := socketSuggestion(current); hint != "" {
		return hint
	}
	return current
}

func socketSuggestion(current string) string {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return ""
	}
	hint := filepath.Join(runtimeDir, "atomic-queue", "atomic-queue.sock")
	if hint == current {
		return ""
	}
	return hint
}

func usageError(cmd string, err error) int {
	fmt.Fprintln(os.Stderr, err)
	switch cmd {
	case "push":
		fmt.Fprintln(os.Stderr, "usage: atomic-queue push [--socket path] [--stdin] channel [payload]")
	case "pop":
		fmt.Fprintln(os.Stderr, "usage: atomic-queue pop [--socket path] [--timeout d] channel...")
	case "serve":
		fmt.Fprintln(os.Stderr, "usage: atomic-queue serve [--socket path] [--max-queued-bytes N]")
	case "stress":
		fmt.Fprintln(os.Stderr, "usage: atomic-queue stress [--socket path] [--duration 10s] [--threads 1000] [--publishers n] [--consumers n] [--channels a,b,c] [--pop-timeout 200ms] [--payload-size 128] [--format text|json]")
	}
	return exitUsage
}

func runtimeError(err error) int {
	fmt.Fprintln(os.Stderr, err)
	return exitRuntime
}

func clientError(err error) int {
	if errors.Is(err, ErrTimeout) {
		fmt.Fprintln(os.Stderr, err)
		return exitTimeout
	}
	fmt.Fprintln(os.Stderr, err)
	return exitRuntime
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "atomic-queue: small local message queue")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  atomic-queue")
	fmt.Fprintln(w, "  atomic-queue help")
	fmt.Fprintln(w, "  atomic-queue push channel '{\"foo\":123}'")
	fmt.Fprintln(w, "  cat file.msgpack | atomic-queue push --stdin channel")
	fmt.Fprintln(w, "  atomic-queue pop channel")
	fmt.Fprintln(w, "  atomic-queue pop channel1 channel2 --timeout 1500ms")
	fmt.Fprintln(w, "  atomic-queue serve")
	fmt.Fprintln(w, "  atomic-queue stress --duration 10s --threads 1000")
	fmt.Fprintln(w, "  atomic-queue stress --duration 10s --publishers 500 --consumers 500 --format json")
	fmt.Fprintln(w, "")
	fmt.Fprintf(w, "Default socket: %s\n", defaultSocketPath())
	fmt.Fprintln(w, "Override socket: ATOMIC_QUEUE_SOCKET=/path/to.sock or --socket /path/to.sock")
	fmt.Fprintln(w, "GitHub: https://github.com/parf/atomic-queue")
}

func parseServeArgs(args []string) (string, int64, error) {
	socket := defaultSocketPath()
	maxQueuedBytes := defaultMaxQueuedBytes

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--socket":
			if i+1 >= len(args) {
				return "", 0, errors.New("missing value for --socket")
			}
			socket = args[i+1]
			i++
		case hasLongOption(arg, "--socket"):
			socket, _ = trimOption(arg, "--socket")
		case arg == "--max-queued-bytes":
			if i+1 >= len(args) {
				return "", 0, errors.New("missing value for --max-queued-bytes")
			}
			n, err := parsePositiveInt64(args[i+1], "max-queued-bytes")
			if err != nil {
				return "", 0, err
			}
			maxQueuedBytes = n
			i++
		case hasLongOption(arg, "--max-queued-bytes"):
			value, _ := trimOption(arg, "--max-queued-bytes")
			n, err := parsePositiveInt64(value, "max-queued-bytes")
			if err != nil {
				return "", 0, err
			}
			maxQueuedBytes = n
		default:
			return "", 0, fmt.Errorf("unknown serve option %q", arg)
		}
	}

	if maxQueuedBytes < int64(defaultMaxBytes) {
		return "", 0, fmt.Errorf("--max-queued-bytes must be >= %d (per-message max)", defaultMaxBytes)
	}
	return socket, maxQueuedBytes, nil
}

func parsePositiveInt64(value, name string) (int64, error) {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", name, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s must be > 0", name)
	}
	return n, nil
}

func parsePushArgs(args []string) (string, []string, bool, error) {
	socket := defaultSocketPath()
	positionals := make([]string, 0, len(args))
	readStdin := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--socket":
			if i+1 >= len(args) {
				return "", nil, false, errors.New("missing value for --socket")
			}
			socket = args[i+1]
			i++
		case arg == "--stdin":
			readStdin = true
		case hasLongOption(arg, "--socket"):
			value, _ := trimOption(arg, "--socket")
			socket = value
		default:
			positionals = append(positionals, arg)
		}
	}
	return socket, positionals, readStdin, nil
}

func parsePopArgs(args []string) (string, []string, time.Duration, error) {
	socket, positionals, err := parseArgs(args, true)
	if err != nil {
		return "", nil, 0, err
	}

	var timeout time.Duration
	filtered := positionals[:0]
	for i := 0; i < len(positionals); i++ {
		arg := positionals[i]
		if arg == "--timeout" {
			if i+1 >= len(positionals) {
				return "", nil, 0, errors.New("missing value for --timeout")
			}
			timeout, err = time.ParseDuration(positionals[i+1])
			if err != nil {
				return "", nil, 0, fmt.Errorf("invalid timeout: %w", err)
			}
			i++
			continue
		}
		if value, ok := trimOption(arg, "--timeout"); ok {
			timeout, err = time.ParseDuration(value)
			if err != nil {
				return "", nil, 0, fmt.Errorf("invalid timeout: %w", err)
			}
			continue
		}
		filtered = append(filtered, arg)
	}
	if timeout < 0 {
		return "", nil, 0, errors.New("timeout must be >= 0")
	}
	return socket, filtered, timeout, nil
}

func parseArgs(args []string, allowTimeout bool) (string, []string, error) {
	socket := defaultSocketPath()
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--socket" {
			if i+1 >= len(args) {
				return "", nil, errors.New("missing value for --socket")
			}
			socket = args[i+1]
			i++
			continue
		}
		if value, ok := trimOption(arg, "--socket"); ok {
			socket = value
			continue
		}
		if allowTimeout && (arg == "--timeout" || hasLongOption(arg, "--timeout")) {
			positionals = append(positionals, arg)
			continue
		}
		positionals = append(positionals, arg)
	}
	return socket, positionals, nil
}

func trimOption(arg, name string) (string, bool) {
	return strings.CutPrefix(arg, name+"=")
}

func hasLongOption(arg, name string) bool {
	_, ok := trimOption(arg, name)
	return ok
}
