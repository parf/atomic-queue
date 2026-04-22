package main

import (
	"fmt"
	"os"
	"testing"
)

func TestDefaultSocketPathUsesEnvOverride(t *testing.T) {
	t.Setenv("ATOMIC_QUEUE_SOCKET", "/tmp/custom.sock")
	t.Setenv("USER", "parf")

	got := defaultSocketPath()
	if got != "/tmp/custom.sock" {
		t.Fatalf("expected env override, got %q", got)
	}
}

func TestDefaultSocketPathUsesRunUser(t *testing.T) {
	t.Setenv("ATOMIC_QUEUE_SOCKET", "")

	got := defaultSocketPath()
	want := fmt.Sprintf("/run/user/%d/atomic-queue/atomic-queue.sock", os.Getuid())
	if got != want {
		t.Fatalf("unexpected default socket path %q", got)
	}
}
