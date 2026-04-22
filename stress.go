package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	randv2 "math/rand/v2"
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
	publishers int
	consumers  int
	channels   []string
	popTimeout time.Duration
	payloadLen int
	format     string
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

	producerCount, consumerCount := stressWorkerCounts(cfg)

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

			rng := randv2.New(randv2.NewPCG(uint64(time.Now().UnixNano()), uint64(workerID+1)))
			seq := 0
			for ctx.Err() == nil {
				channel := cfg.channels[rng.IntN(len(cfg.channels))]
				err := client.Push(channel, makeStressPayload(workerID, seq, cfg.payloadLen, rng))
				seq++
				if err != nil {
					failures.Add(1)
					return
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
				resp, err := client.Pop(cfg.channels, cfg.popTimeout.Milliseconds())
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

	result := stressResult{
		DurationSeconds: float64(elapsed) / float64(time.Second),
		Threads:         cfg.threads,
		Publishers:      producerCount,
		Consumers:       consumerCount,
		Channels:        cfg.channels,
		MessagesPushed:  pushed.Load(),
		MessagesServed:  served.Load(),
		PopTimeouts:     popTimeouts.Load(),
		ClientFailures:  failures.Load(),
		PushRate:        float64(pushed.Load()) / seconds,
		ServeRate:       float64(served.Load()) / seconds,
	}

	if cfg.format == "json" {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(result); err != nil {
			return runtimeError(fmt.Errorf("encode stress output: %w", err))
		}
		return exitOK
	}

	fmt.Fprintf(
		os.Stdout,
		"stress duration: %s\nthreads: %d (%d producers, %d consumers)\nchannels: %s\nmessages pushed: %d\nmessages served: %d\npop timeouts: %d\nclient failures: %d\npush rate: %.2f msg/s\nserve rate: %.2f msg/s\n",
		elapsed.Round(time.Millisecond),
		result.Threads,
		result.Publishers,
		result.Consumers,
		strings.Join(result.Channels, ", "),
		result.MessagesPushed,
		result.MessagesServed,
		result.PopTimeouts,
		result.ClientFailures,
		result.PushRate,
		result.ServeRate,
	)
	return exitOK
}

func parseStressArgs(args []string) (stressConfig, error) {
	cfg := stressConfig{
		socket:     defaultSocketPath(),
		duration:   10 * time.Second,
		threads:    1000,
		publishers: 0,
		consumers:  0,
		channels:   []string{"stress-a", "stress-b", "stress-c", "stress-d"},
		popTimeout: 200 * time.Millisecond,
		payloadLen: 128,
		format:     "text",
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
		case arg == "--publishers":
			value, next, err := nextArgValue(args, i, "--publishers")
			if err != nil {
				return stressConfig{}, err
			}
			cfg.publishers, err = parsePositiveInt(value, "publishers")
			if err != nil {
				return stressConfig{}, err
			}
			i = next
		case arg == "--consumers":
			value, next, err := nextArgValue(args, i, "--consumers")
			if err != nil {
				return stressConfig{}, err
			}
			cfg.consumers, err = parsePositiveInt(value, "consumers")
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
		case arg == "--format":
			value, next, err := nextArgValue(args, i, "--format")
			if err != nil {
				return stressConfig{}, err
			}
			cfg.format = value
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
		case hasLongOption(arg, "--publishers"):
			value := mustOptionValue(arg, "--publishers")
			n, err := parsePositiveInt(value, "publishers")
			if err != nil {
				return stressConfig{}, err
			}
			cfg.publishers = n
		case hasLongOption(arg, "--consumers"):
			value := mustOptionValue(arg, "--consumers")
			n, err := parsePositiveInt(value, "consumers")
			if err != nil {
				return stressConfig{}, err
			}
			cfg.consumers = n
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
		case hasLongOption(arg, "--format"):
			cfg.format = mustOptionValue(arg, "--format")
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
	if cfg.publishers != 0 || cfg.consumers != 0 {
		if cfg.publishers <= 0 || cfg.consumers <= 0 {
			return stressConfig{}, errors.New("publishers and consumers must both be > 0 when either is set")
		}
		cfg.threads = cfg.publishers + cfg.consumers
	}
	if cfg.format != "text" && cfg.format != "json" {
		return stressConfig{}, errors.New("format must be text or json")
	}
	return cfg, nil
}

type stressResult struct {
	DurationSeconds float64  `json:"duration_seconds"`
	Threads         int      `json:"threads"`
	Publishers      int      `json:"publishers"`
	Consumers       int      `json:"consumers"`
	Channels        []string `json:"channels"`
	MessagesPushed  uint64   `json:"messages_pushed"`
	MessagesServed  uint64   `json:"messages_served"`
	PopTimeouts     uint64   `json:"pop_timeouts"`
	ClientFailures  uint64   `json:"client_failures"`
	PushRate        float64  `json:"push_rate"`
	ServeRate       float64  `json:"serve_rate"`
}

func stressWorkerCounts(cfg stressConfig) (int, int) {
	if cfg.publishers > 0 && cfg.consumers > 0 {
		return cfg.publishers, cfg.consumers
	}

	producerCount := cfg.threads / 2
	if producerCount < 1 {
		producerCount = 1
	}
	consumerCount := cfg.threads - producerCount
	if consumerCount < 1 {
		consumerCount = 1
	}
	return producerCount, consumerCount
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

func makeStressPayload(workerID, seq, size int, rng *randv2.Rand) []byte {
	prefix := fmt.Sprintf("worker=%d seq=%d ", workerID, seq)
	if len(prefix) >= size {
		return []byte(prefix[:size])
	}
	raw := make([]byte, (size-len(prefix)+1)/2)
	for i := 0; i < len(raw); i += 8 {
		u := rng.Uint64()
		n := len(raw) - i
		if n > 8 {
			n = 8
		}
		for j := 0; j < n; j++ {
			raw[i+j] = byte(u >> (8 * j))
		}
	}
	body := hex.EncodeToString(raw)
	if len(body) > size-len(prefix) {
		body = body[:size-len(prefix)]
	}
	return []byte(prefix + body)
}
