package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var ErrTimeout = errors.New("timeout")

type delivery struct {
	Channel string `json:"channel"`
	Payload string `json:"payload"`
}

type waiter struct {
	channels []string
	reply    chan delivery
}

type Broker struct {
	mu       sync.Mutex
	queues   map[string][]string
	waiters  []*waiter
	maxBytes int
}

func NewBroker(maxBytes int) *Broker {
	return &Broker{
		queues:   make(map[string][]string),
		maxBytes: maxBytes,
	}
}

func (b *Broker) Push(channel, payload string) error {
	if err := validateChannel(channel); err != nil {
		return err
	}
	if err := validatePayload(payload, b.maxBytes); err != nil {
		return err
	}

	b.mu.Lock()
	for i, w := range b.waiters {
		if !waiterMatches(w, channel) {
			continue
		}

		b.waiters = append(b.waiters[:i], b.waiters[i+1:]...)
		b.mu.Unlock()
		w.reply <- delivery{Channel: channel, Payload: payload}
		return nil
	}

	b.queues[channel] = append(b.queues[channel], payload)
	b.mu.Unlock()
	return nil
}

func (b *Broker) Pop(ctx context.Context, channels []string) (delivery, error) {
	if len(channels) == 0 {
		return delivery{}, fmt.Errorf("at least one channel is required")
	}
	for _, channel := range channels {
		if err := validateChannel(channel); err != nil {
			return delivery{}, err
		}
	}

	b.mu.Lock()
	if msg, ok := b.popQueuedLocked(channels); ok {
		b.mu.Unlock()
		return msg, nil
	}
	if err := ctx.Err(); err != nil {
		b.mu.Unlock()
		return delivery{}, timeoutFromContext(err)
	}

	w := &waiter{
		channels: append([]string(nil), channels...),
		reply:    make(chan delivery, 1),
	}
	b.waiters = append(b.waiters, w)
	b.mu.Unlock()

	select {
	case msg := <-w.reply:
		return msg, nil
	case <-ctx.Done():
		b.mu.Lock()
		removed := b.removeWaiterLocked(w)
		b.mu.Unlock()
		if !removed {
			msg := <-w.reply
			return msg, nil
		}

		select {
		case msg := <-w.reply:
			return msg, nil
		default:
			return delivery{}, timeoutFromContext(ctx.Err())
		}
	}
}

func (b *Broker) popQueuedLocked(channels []string) (delivery, bool) {
	for _, channel := range channels {
		queue := b.queues[channel]
		if len(queue) == 0 {
			continue
		}

		payload := queue[0]
		if len(queue) == 1 {
			delete(b.queues, channel)
		} else {
			b.queues[channel] = queue[1:]
		}
		return delivery{Channel: channel, Payload: payload}, true
	}
	return delivery{}, false
}

func (b *Broker) removeWaiterLocked(target *waiter) bool {
	for i, w := range b.waiters {
		if w != target {
			continue
		}

		b.waiters = append(b.waiters[:i], b.waiters[i+1:]...)
		return true
	}
	return false
}

func waiterMatches(w *waiter, channel string) bool {
	for _, candidate := range w.channels {
		if candidate == channel {
			return true
		}
	}
	return false
}

func timeoutFromContext(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrTimeout
	}
	return err
}

func timeoutContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(context.Background())
	}
	return context.WithTimeout(context.Background(), timeout)
}
