package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestBrokerFIFO(t *testing.T) {
	b := NewBroker(defaultMaxBytes)

	if err := b.Push("jobs", `{"n":1}`); err != nil {
		t.Fatal(err)
	}
	if err := b.Push("jobs", `{"n":2}`); err != nil {
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

	if msg1.Payload != `{"n":1}` || msg2.Payload != `{"n":2}` {
		t.Fatalf("expected FIFO order, got %q then %q", msg1.Payload, msg2.Payload)
	}
}

func TestBrokerMultiChannelWait(t *testing.T) {
	b := NewBroker(defaultMaxBytes)

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
	if err := b.Push("beta", `{"ok":true}`); err != nil {
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
	b := NewBroker(defaultMaxBytes)
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
			results <- result{payload: msg.Payload}
		}()
	}

	time.Sleep(50 * time.Millisecond)
	for i := 0; i < count; i++ {
		if err := b.Push("shared", fmt.Sprintf(`{"id":%d}`, i)); err != nil {
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
	b := NewBroker(defaultMaxBytes)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := b.Pop(ctx, []string{"empty"})
	if err != ErrTimeout {
		t.Fatalf("expected ErrTimeout, got %v", err)
	}
}
