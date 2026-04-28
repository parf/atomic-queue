package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

var ErrTimeout = errors.New("timeout")

type delivery struct {
	Channel string `json:"channel"`
	Payload []byte `json:"payload"`
}

type waiter struct {
	channels []string
	reply    chan delivery
}

type Broker struct {
	mu             sync.Mutex
	queues         map[string][][]byte
	waiters        []*waiter
	maxBytes       int
	queuedBytes    int64
	maxQueuedBytes int64
	drainSignal    chan struct{}
	onBlock        func(channel string, queuedBytes, maxQueuedBytes int64)
}

// SetOnBlock installs a callback invoked each time a Push has to wait
// for queue capacity. The callback is invoked outside the broker
// mutex; it must not block. Set once before serving — the field is
// not goroutine-safe to mutate after Push calls begin.
func (b *Broker) SetOnBlock(fn func(channel string, queuedBytes, maxQueuedBytes int64)) {
	b.onBlock = fn
}

func NewBroker(maxBytes int, maxQueuedBytes int64) *Broker {
	return &Broker{
		queues:         make(map[string][][]byte),
		maxBytes:       maxBytes,
		maxQueuedBytes: maxQueuedBytes,
		drainSignal:    make(chan struct{}),
	}
}

func (b *Broker) Push(ctx context.Context, channel string, payload []byte) error {
	return b.push(ctx, channel, payload, true)
}

func (b *Broker) PushOwned(ctx context.Context, channel string, payload []byte) error {
	return b.push(ctx, channel, payload, false)
}

func (b *Broker) push(ctx context.Context, channel string, payload []byte, clonePayload bool) error {
	if err := validateChannel(channel); err != nil {
		return err
	}
	if err := validatePayload(payload, b.maxBytes); err != nil {
		return err
	}
	if clonePayload {
		payload = slices.Clone(payload)
	}
	size := int64(len(payload))

	b.mu.Lock()
	for {
		for i, w := range b.waiters {
			if !waiterMatches(w, channel) {
				continue
			}

			b.removeWaiterAtLocked(i)
			b.mu.Unlock()
			w.reply <- delivery{Channel: channel, Payload: payload}
			return nil
		}

		if b.queuedBytes+size <= b.maxQueuedBytes {
			break
		}

		sig := b.drainSignal
		queued := b.queuedBytes
		max := b.maxQueuedBytes
		notify := b.onBlock
		b.mu.Unlock()
		if notify != nil {
			notify(channel, queued, max)
		}
		select {
		case <-sig:
		case <-ctx.Done():
			return timeoutFromContext(ctx.Err())
		}
		b.mu.Lock()
	}

	b.queues[channel] = append(b.queues[channel], payload)
	b.queuedBytes += size
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
		channels: slices.Clone(channels),
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
		b.queuedBytes -= int64(len(payload))
		close(b.drainSignal)
		b.drainSignal = make(chan struct{})
		return delivery{Channel: channel, Payload: payload}, true
	}
	return delivery{}, false
}

func (b *Broker) removeWaiterLocked(target *waiter) bool {
	for i, w := range b.waiters {
		if w != target {
			continue
		}

		b.removeWaiterAtLocked(i)
		return true
	}
	return false
}

func (b *Broker) removeWaiterAtLocked(i int) {
	last := len(b.waiters) - 1
	copy(b.waiters[i:], b.waiters[i+1:])
	b.waiters[last] = nil
	b.waiters = b.waiters[:last]
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
