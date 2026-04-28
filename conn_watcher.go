package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"syscall"
)

// connWatcher uses epoll EPOLLRDHUP/EPOLLHUP to detect peer-close on
// idle connections. When a peer closes a connection whose handler is
// blocked inside the broker (e.g. a Push waiting on capacity), we
// would otherwise not learn until something tries to read or write.
// The watcher cancels the connection's context on peer close so the
// broker call returns and the handler can exit.
type connWatcher struct {
	epfd    int
	mu      sync.Mutex
	cancels map[int32]context.CancelFunc
}

func newConnWatcher() (*connWatcher, error) {
	epfd, err := syscall.EpollCreate1(syscall.EPOLL_CLOEXEC)
	if err != nil {
		return nil, fmt.Errorf("epoll_create1: %w", err)
	}
	return &connWatcher{
		epfd:    epfd,
		cancels: make(map[int32]context.CancelFunc),
	}, nil
}

func (w *connWatcher) run() {
	events := make([]syscall.EpollEvent, 64)
	for {
		n, err := syscall.EpollWait(w.epfd, events, -1)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return
		}
		for i := 0; i < n; i++ {
			fd := events[i].Fd
			w.mu.Lock()
			cancel, ok := w.cancels[fd]
			if ok {
				delete(w.cancels, fd)
			}
			w.mu.Unlock()
			if ok {
				cancel()
			}
		}
	}
}

// epollEventMask is the set of epoll events we watch for: peer
// half-close, full close, error, and edge-triggered delivery.
// EPOLLET (1 << 31) is hard-coded because Go's syscall.EPOLLET is
// typed in a way that overflows uint32 when cast directly.
const epollEventMask = uint32(syscall.EPOLLRDHUP|syscall.EPOLLHUP|syscall.EPOLLERR) | 0x80000000

func (w *connWatcher) register(fd int32, cancel context.CancelFunc) error {
	ev := syscall.EpollEvent{
		Events: epollEventMask,
		Fd:     fd,
	}
	w.mu.Lock()
	w.cancels[fd] = cancel
	w.mu.Unlock()
	if err := syscall.EpollCtl(w.epfd, syscall.EPOLL_CTL_ADD, int(fd), &ev); err != nil {
		w.mu.Lock()
		delete(w.cancels, fd)
		w.mu.Unlock()
		return fmt.Errorf("epoll_ctl add: %w", err)
	}
	return nil
}

func (w *connWatcher) unregister(fd int32) {
	_ = syscall.EpollCtl(w.epfd, syscall.EPOLL_CTL_DEL, int(fd), nil)
	w.mu.Lock()
	delete(w.cancels, fd)
	w.mu.Unlock()
}

func (w *connWatcher) close() {
	syscall.Close(w.epfd)
}

// connFD extracts the underlying file descriptor from a net.Conn
// without disrupting it. Caller is responsible for ensuring the
// connection stays open while the fd is in use.
func connFD(conn net.Conn) (int32, error) {
	type syscallConn interface {
		SyscallConn() (syscall.RawConn, error)
	}
	sc, ok := conn.(syscallConn)
	if !ok {
		return -1, errors.New("conn does not support SyscallConn")
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return -1, err
	}
	var fd int32
	cerr := raw.Control(func(f uintptr) {
		fd = int32(f)
	})
	if cerr != nil {
		return -1, cerr
	}
	return fd, nil
}
