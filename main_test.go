package main

import "testing"

func TestResolveSocketPathPrefersFirstUsableCandidate(t *testing.T) {
	t.Setenv("ATOMIC_QUEUE_SOCKET", "")

	first := t.TempDir() + "/first.sock"
	second := t.TempDir() + "/second.sock"

	got := resolveSocketPath([]string{first, second})
	if got != first {
		t.Fatalf("expected first usable socket path %q, got %q", first, got)
	}
}

func TestResolveSocketPathFallsBackWhenPreferredDirIsNotWritable(t *testing.T) {
	t.Setenv("ATOMIC_QUEUE_SOCKET", "")

	unusable := "/definitely-not-writable/atomic-queue.sock"
	fallback := t.TempDir() + "/atomic-queue.sock"

	got := resolveSocketPath([]string{unusable, fallback})
	if got != fallback {
		t.Fatalf("expected fallback socket path %q, got %q", fallback, got)
	}
}
