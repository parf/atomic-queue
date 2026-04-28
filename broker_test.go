package main

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

func TestBrokerFIFO(t *testing.T) {
	b := NewBroker(defaultMaxBytes, defaultMaxQueuedBytes)

	if err := b.Push(context.Background(), "jobs", []byte(`{"n":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := b.Push(context.Background(), "jobs", []byte(`{"n":2}`)); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	msg1, err := b.Pop(ctx, []string{"jobs"})
	if err != nil {
		t.Fatal(err)
	}
	msg2, err := b.Pop(ctx, []string{"jobs"})
	if err != nil {
		t.Fatal(err)
	}

	if string(msg1.Payload) != `{"n":1}` || string(msg2.Payload) != `{"n":2}` {
		t.Fatalf("expected FIFO order, got %q then %q", msg1.Payload, msg2.Payload)
	}
}

func TestBrokerMultiChannelWait(t *testing.T) {
	b := NewBroker(defaultMaxBytes, defaultMaxQueuedBytes)

	done := make(chan delivery, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		msg, err := b.Pop(ctx, []string{"alpha", "beta"})
		if err != nil {
			t.Errorf("pop failed: %v", err)
			return
		}
		done <- msg
	}()

	time.Sleep(50 * time.Millisecond)
	if err := b.Push(context.Background(), "beta", []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-done:
		if msg.Channel != "beta" {
			t.Fatalf("expected beta, got %s", msg.Channel)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pop")
	}
}

func TestBrokerConcurrentSingleDelivery(t *testing.T) {
	b := NewBroker(defaultMaxBytes, defaultMaxQueuedBytes)
	const count = 50

	type result struct {
		payload string
		err     error
	}

	results := make(chan result, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			msg, err := b.Pop(ctx, []string{"shared"})
			if err != nil {
				results <- result{err: err}
				return
			}
			results <- result{payload: string(msg.Payload)}
		}()
	}

	time.Sleep(50 * time.Millisecond)
	for i := 0; i < count; i++ {
		if err := b.Push(context.Background(), "shared", []byte(fmt.Sprintf(`{"id":%d}`, i))); err != nil {
			t.Fatal(err)
		}
	}

	wg.Wait()
	close(results)

	seen := make(map[string]int)
	for res := range results {
		if res.err != nil {
			t.Fatalf("consumer failed: %v", res.err)
		}
		seen[res.payload]++
	}

	if len(seen) != count {
		t.Fatalf("expected %d unique messages, got %d", count, len(seen))
	}
	for payload, n := range seen {
		if n != 1 {
			t.Fatalf("payload %s seen %d times", payload, n)
		}
	}
}

func TestBrokerTimeout(t *testing.T) {
	b := NewBroker(defaultMaxBytes, defaultMaxQueuedBytes)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := b.Pop(ctx, []string{"empty"})
	if err != ErrTimeout {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
}

func TestBrokerAllowsPlainStringPayload(t *testing.T) {
	b := NewBroker(defaultMaxBytes, defaultMaxQueuedBytes)

	if err := b.Push(context.Background(), "logs", []byte("plain text line")); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	msg, err := b.Pop(ctx, []string{"logs"})
	if err != nil {
		t.Fatal(err)
	}
	if string(msg.Payload) != "plain text line" {
		t.Fatalf("unexpected payload %q", msg.Payload)
	}
}

func TestBrokerAllowsBinaryPayload(t *testing.T) {
	b := NewBroker(defaultMaxBytes, defaultMaxQueuedBytes)
	payload := []byte{0x00, 0x01, 0x02, 0xff, 0x10}

	if err := b.Push(context.Background(), "bin", payload); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	msg, err := b.Pop(ctx, []string{"bin"})
	if err != nil {
		t.Fatal(err)
	}
	if string(msg.Payload) != string(payload) {
		t.Fatalf("unexpected binary payload %v", msg.Payload)
	}
}

func TestBrokerStressMultiProducerMultiConsumer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	b := NewBroker(defaultMaxBytes, defaultMaxQueuedBytes)

	channels := []string{"alpha", "beta", "gamma", "delta"}
	producersPerChannel := 8
	messagesPerProducer := 125
	totalMessages := len(channels) * producersPerChannel * messagesPerProducer
	consumerCount := 24

	results := make(chan delivery, totalMessages)
	errs := make(chan error, consumerCount)

	var consumerWG sync.WaitGroup
	for i := 0; i < consumerCount; i++ {
		consumerWG.Add(1)
		go func() {
			defer consumerWG.Done()
			for {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				msg, err := b.Pop(ctx, channels)
				cancel()
				if err == ErrTimeout {
					return
				}
				if err != nil {
					errs <- err
					return
				}
				results <- msg
			}
		}()
	}

	var producerWG sync.WaitGroup
	for _, channel := range channels {
		channel := channel
		for producerID := 0; producerID < producersPerChannel; producerID++ {
			producerID := producerID
			producerWG.Add(1)
			go func() {
				defer producerWG.Done()
				for seq := 0; seq < messagesPerProducer; seq++ {
					payload := fmt.Sprintf("%s:%02d:%03d", channel, producerID, seq)
					if err := b.Push(context.Background(), channel, []byte(payload)); err != nil {
						errs <- err
						return
					}
				}
			}()
		}
	}

	producerWG.Wait()
	consumerWG.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("stress test failed: %v", err)
		}
	}

	seen := make(map[string]int, totalMessages)
	byChannel := make(map[string][]string, len(channels))
	for msg := range results {
		key := msg.Channel + "\x00" + string(msg.Payload)
		seen[key]++
		byChannel[msg.Channel] = append(byChannel[msg.Channel], string(msg.Payload))
	}

	if len(seen) != totalMessages {
		t.Fatalf("expected %d unique deliveries, got %d", totalMessages, len(seen))
	}
	for key, count := range seen {
		if count != 1 {
			t.Fatalf("message %q delivered %d times", key, count)
		}
	}

	for _, channel := range channels {
		payloads := byChannel[channel]
		expectedCount := producersPerChannel * messagesPerProducer
		if len(payloads) != expectedCount {
			t.Fatalf("channel %s expected %d messages, got %d", channel, expectedCount, len(payloads))
		}

		sort.Strings(payloads)
		for producerID := 0; producerID < producersPerChannel; producerID++ {
			for seq := 0; seq < messagesPerProducer; seq++ {
				expected := fmt.Sprintf("%s:%02d:%03d", channel, producerID, seq)
				index := producerID*messagesPerProducer + seq
				if payloads[index] != expected {
					t.Fatalf("channel %s missing or out of place message %q at sorted index %d; got %q", channel, expected, index, payloads[index])
				}
			}
		}
	}
}

func TestBrokerPushBlocksAtCap(t *testing.T) {
	b := NewBroker(defaultMaxBytes, 200)

	if err := b.Push(context.Background(), "ch", make([]byte, 100)); err != nil {
		t.Fatal(err)
	}
	if err := b.Push(context.Background(), "ch", make([]byte, 100)); err != nil {
		t.Fatal(err)
	}

	pushDone := make(chan error, 1)
	go func() {
		pushDone <- b.Push(context.Background(), "ch", make([]byte, 50))
	}()

	select {
	case err := <-pushDone:
		t.Fatalf("expected push to block, got %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	popCtx, popCancel := context.WithTimeout(context.Background(), time.Second)
	defer popCancel()
	if _, err := b.Pop(popCtx, []string{"ch"}); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-pushDone:
		if err != nil {
			t.Fatalf("blocked push failed after pop: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("push did not unblock after pop")
	}
}

func TestBrokerPushCancelDuringWait(t *testing.T) {
	b := NewBroker(defaultMaxBytes, 100)
	if err := b.Push(context.Background(), "ch", make([]byte, 100)); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := b.Push(ctx, "ch", make([]byte, 50))
	if err != ErrTimeout {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
}

func TestBrokerPushImmediateCancelWhenFull(t *testing.T) {
	b := NewBroker(defaultMaxBytes, 100)
	if err := b.Push(context.Background(), "ch", make([]byte, 100)); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := b.Push(ctx, "ch", make([]byte, 50))
	if err != ErrTimeout {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
}

func TestBrokerWaiterHandoffSkipsAccounting(t *testing.T) {
	b := NewBroker(defaultMaxBytes, 100)

	received := make(chan delivery, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		msg, err := b.Pop(ctx, []string{"ch"})
		if err != nil {
			t.Errorf("pop failed: %v", err)
			return
		}
		received <- msg
	}()

	time.Sleep(50 * time.Millisecond)

	if err := b.Push(context.Background(), "ch", make([]byte, 200)); err != nil {
		t.Fatalf("waiter handoff push failed: %v", err)
	}

	select {
	case msg := <-received:
		if len(msg.Payload) != 200 {
			t.Fatalf("expected 200-byte payload, got %d", len(msg.Payload))
		}
	case <-time.After(time.Second):
		t.Fatal("waiter never received message")
	}

	b.mu.Lock()
	queued := b.queuedBytes
	b.mu.Unlock()
	if queued != 0 {
		t.Fatalf("waiter handoff must not count toward queuedBytes, got %d", queued)
	}
}

func TestBrokerConcurrentPushersAtCap(t *testing.T) {
	b := NewBroker(defaultMaxBytes, 1024)

	const pushers = 20
	const payloadSize = 256
	const messagesPerPusher = 50

	var pushWG sync.WaitGroup
	for i := 0; i < pushers; i++ {
		pushWG.Add(1)
		go func(id int) {
			defer pushWG.Done()
			for j := 0; j < messagesPerPusher; j++ {
				payload := make([]byte, payloadSize)
				payload[0] = byte(id)
				if err := b.Push(context.Background(), "ch", payload); err != nil {
					t.Errorf("push failed: %v", err)
					return
				}
			}
		}(i)
	}

	totalMessages := pushers * messagesPerPusher
	for received := 0; received < totalMessages; received++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := b.Pop(ctx, []string{"ch"})
		cancel()
		if err != nil {
			t.Fatalf("pop %d failed: %v", received, err)
		}

		b.mu.Lock()
		queued := b.queuedBytes
		b.mu.Unlock()
		if queued > 1024 {
			t.Fatalf("queuedBytes exceeded cap: %d > 1024", queued)
		}
	}
	pushWG.Wait()

	b.mu.Lock()
	finalQueued := b.queuedBytes
	b.mu.Unlock()
	if finalQueued != 0 {
		t.Fatalf("queuedBytes should be 0 after draining, got %d", finalQueued)
	}
}
