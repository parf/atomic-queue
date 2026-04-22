package main

import "testing"

func TestParseStressArgsExplicitWorkers(t *testing.T) {
	cfg, err := parseStressArgs([]string{
		"--duration", "5s",
		"--publishers", "7",
		"--consumers", "9",
		"--format", "json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.publishers != 7 || cfg.consumers != 9 {
		t.Fatalf("unexpected worker counts: %+v", cfg)
	}
	if cfg.threads != 16 {
		t.Fatalf("expected threads to match publishers+consumers, got %d", cfg.threads)
	}
	if cfg.format != "json" {
		t.Fatalf("unexpected format %q", cfg.format)
	}
}

func TestMakeStressPayloadSize(t *testing.T) {
	payload := makeStressPayload(12, 34, 128)
	if len(payload) != 128 {
		t.Fatalf("unexpected payload size %d", len(payload))
	}
}
