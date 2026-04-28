package main

import (
	"syscall"
	"testing"
	"time"
)

func TestConnWatcherCancelsOnPeerClose(t *testing.T) {
	w, err := newConnWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.close()
	go w.run()

	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}

	fired := make(chan struct{})
	if err := w.register(int32(fds[0]), func() { close(fired) }); err != nil {
		syscall.Close(fds[0])
		syscall.Close(fds[1])
		t.Fatal(err)
	}

	if err := syscall.Close(fds[1]); err != nil {
		t.Fatal(err)
	}

	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("watcher did not fire on peer close")
	}

	w.unregister(int32(fds[0]))
	syscall.Close(fds[0])
}

func TestConnWatcherDoesNotFireWhilePeerAlive(t *testing.T) {
	w, err := newConnWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.close()
	go w.run()

	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fds[0])
	defer syscall.Close(fds[1])

	fired := make(chan struct{}, 1)
	if err := w.register(int32(fds[0]), func() { fired <- struct{}{} }); err != nil {
		t.Fatal(err)
	}
	defer w.unregister(int32(fds[0]))

	if _, err := syscall.Write(fds[1], []byte("hello")); err != nil {
		t.Fatal(err)
	}

	select {
	case <-fired:
		t.Fatal("watcher fired while peer was still alive")
	case <-time.After(150 * time.Millisecond):
	}
}
