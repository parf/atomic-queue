package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type stressConfig struct {
	socket     string
	duration   time.Duration
	threads    int
	channels   []string
	popTimeout time.Duration
	payloadLen int
}

func runStress(args []string) int {
	cfg, err := parseStressArgs(args)
	if err != nil {
		return usageError("stress", err)
	}
	for _, channel := range cfg.channels {
		if err := validateChannel(channel); err != nil {
			return runtimeError(err)
		}
	}

	// Start the daemon/socket path once before the timed run without adding a queued message.
	resp, err := roundTrip(cfg.socket, request{
		Op:        "pop",
		Channels:  cfg.channels,
		TimeoutMS: 1,
	}, true)
	if err != nil {
		return clientError(err)
	}
	if !resp.OK && resp.Error != ErrTimeout.Error() {
		return runtimeError(errors.New(resp.Error))
	}

	start := time.Now()
	ctx, cancel := context.WithDeadline(context.Background(), start.Add(cfg.duration))
	defer cancel()

	producerCount := cfg.threads / 2
	if producerCount < 1 {
		producerCount = 1
	}
	consumerCount := cfg.threads - producerCount
	if consumerCount < 1 {
		consumerCount = 1
	}

	var pushed atomic.Uint64
	var served atomic.Uint64
	var popTimeouts atomic.Uint64
	var failures atomic.Uint64

	var wg sync.WaitGroup
	for i := 0; i < producerCount; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			client, err := newRPCClient(cfg.socket, false)
			if err != nil {
				failures.Add(1)
				return
			}
			defer client.Close()

			rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID+1)))
			seq := 0
			for ctx.Err() == nil {
				channel := cfg.channels[rng.Intn(len(cfg.channels))]
				resp, err := client.Do(request{
					Op:       "push",
					Channels: []string{channel},
					Payload:  makeStressPayload(workerID, seq, cfg.payloadLen, rng),
				})
				seq++
				if err != nil {
					failures.Add(1)
					return
				}
				if !resp.OK {
					failures.Add(1)
					continue
				}
				pushed.Add(1)
			}
		}(i)
	}

	for i := 0; i < consumerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client, err := newRPCClient(cfg.socket, false)
			if err != nil {
				failures.Add(1)
				return
			}
			defer client.Close()

			for ctx.Err() == nil {
				resp, err := client.Do(request{
					Op:        "pop",
					Channels:  cfg.channels,
					TimeoutMS: cfg.popTimeout.Milliseconds(),
				})
				if err != nil {
					failures.Add(1)
					return
				}
				if !resp.OK {
					if resp.Error == ErrTimeout.Error() {
						popTimeouts.Add(1)
						continue
					}
					failures.Add(1)
					continue
				}
				served.Add(1)
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)
	seconds := elapsed.Seconds()
	if seconds == 0 {
		seconds = 1
	}

	fmt.Fprintf(
		os.Stdout,
		"stress duration: %s\nthreads: %d (%d producers, %d consumers)\nchannels: %s\nmessages pushed: %d\nmessages served: %d\npop timeouts: %d\nclient failures: %d\npush rate: %.2f msg/s\nserve rate: %.2f msg/s\n",
		elapsed.Round(time.Millisecond),
		cfg.threads,
		producerCount,
		consumerCount,
		strings.Join(cfg.channels, ", "),
		pushed.Load(),
		served.Load(),
		popTimeouts.Load(),
		failures.Load(),
		float64(pushed.Load())/seconds,
		float64(served.Load())/seconds,
	)
	return exitOK
}

func parseStressArgs(args []string) (stressConfig, error) {
	cfg := stressConfig{
		socket:     defaultSocketPath(),
		duration:   10 * time.Second,
		threads:    1000,
		channels:   []string{"stress-a", "stress-b", "stress-c", "stress-d"},
		popTimeout: 200 * time.Millisecond,
		payloadLen: 128,
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--socket":
			value, next, err := nextArgValue(args, i, "--socket")
			if err != nil {
				return stressConfig{}, err
			}
			cfg.socket = value
			i = next
		case arg == "--duration":
			value, next, err := nextArgValue(args, i, "--duration")
			if err != nil {
				return stressConfig{}, err
			}
			cfg.duration, err = time.ParseDuration(value)
			if err != nil {
				return stressConfig{}, fmt.Errorf("invalid duration: %w", err)
			}
			i = next
		case arg == "--threads":
			value, next, err := nextArgValue(args, i, "--threads")
			if err != nil {
				return stressConfig{}, err
			}
			cfg.threads, err = parsePositiveInt(value, "threads")
			if err != nil {
				return stressConfig{}, err
			}
			i = next
		case arg == "--channels":
			value, next, err := nextArgValue(args, i, "--channels")
			if err != nil {
				return stressConfig{}, err
			}
			cfg.channels = splitCSV(value)
			i = next
		case arg == "--pop-timeout":
			value, next, err := nextArgValue(args, i, "--pop-timeout")
			if err != nil {
				return stressConfig{}, err
			}
			cfg.popTimeout, err = time.ParseDuration(value)
			if err != nil {
				return stressConfig{}, fmt.Errorf("invalid pop-timeout: %w", err)
			}
			i = next
		case arg == "--payload-size":
			value, next, err := nextArgValue(args, i, "--payload-size")
			if err != nil {
				return stressConfig{}, err
			}
			cfg.payloadLen, err = parsePositiveInt(value, "payload-size")
			if err != nil {
				return stressConfig{}, err
			}
			i = next
		case hasLongOption(arg, "--socket"):
			cfg.socket = mustOptionValue(arg, "--socket")
		case hasLongOption(arg, "--duration"):
			value := mustOptionValue(arg, "--duration")
			d, err := time.ParseDuration(value)
			if err != nil {
				return stressConfig{}, fmt.Errorf("invalid duration: %w", err)
			}
			cfg.duration = d
		case hasLongOption(arg, "--threads"):
			value := mustOptionValue(arg, "--threads")
			n, err := parsePositiveInt(value, "threads")
			if err != nil {
				return stressConfig{}, err
			}
			cfg.threads = n
		case hasLongOption(arg, "--channels"):
			cfg.channels = splitCSV(mustOptionValue(arg, "--channels"))
		case hasLongOption(arg, "--pop-timeout"):
			value := mustOptionValue(arg, "--pop-timeout")
			d, err := time.ParseDuration(value)
			if err != nil {
				return stressConfig{}, fmt.Errorf("invalid pop-timeout: %w", err)
			}
			cfg.popTimeout = d
		case hasLongOption(arg, "--payload-size"):
			value := mustOptionValue(arg, "--payload-size")
			n, err := parsePositiveInt(value, "payload-size")
			if err != nil {
				return stressConfig{}, err
			}
			cfg.payloadLen = n
		default:
			return stressConfig{}, fmt.Errorf("unknown stress option %q", arg)
		}
	}

	if cfg.duration <= 0 {
		return stressConfig{}, errors.New("duration must be > 0")
	}
	if cfg.threads <= 0 {
		return stressConfig{}, errors.New("threads must be > 0")
	}
	if cfg.popTimeout <= 0 {
		return stressConfig{}, errors.New("pop-timeout must be > 0")
	}
	if cfg.payloadLen <= 0 {
		return stressConfig{}, errors.New("payload-size must be > 0")
	}
	if len(cfg.channels) == 0 {
		return stressConfig{}, errors.New("at least one channel is required")
	}
	return cfg, nil
}

func nextArgValue(args []string, index int, name string) (string, int, error) {
	if index+1 >= len(args) {
		return "", index, fmt.Errorf("missing value for %s", name)
	}
	return args[index+1], index + 1, nil
}

func mustOptionValue(arg, name string) string {
	value, _ := trimOption(arg, name)
	return value
}

func parsePositiveInt(value, name string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", name, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s must be > 0", name)
	}
	return n, nil
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func makeStressPayload(workerID, seq, size int, rng *rand.Rand) []byte {
	prefix := fmt.Sprintf("worker=%d seq=%d ", workerID, seq)
	if len(prefix) >= size {
		return []byte(prefix[:size])
	}
	raw := make([]byte, (size-len(prefix)+1)/2)
	_, _ = rng.Read(raw)
	body := hex.EncodeToString(raw)
	if len(body) > size-len(prefix) {
		body = body[:size-len(prefix)]
	}
	return []byte(prefix + body)
}
