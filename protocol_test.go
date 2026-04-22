package main

import (
	"bufio"
	"bytes"
	"testing"
)

func TestProtocolRequestRoundTrip(t *testing.T) {
	original := request{
		Op:        "push",
		Channels:  []string{"alpha", "beta"},
		Payload:   []byte{0x00, 0x01, 0xfe, 0xff},
		TimeoutMS: 1234,
	}

	var buf bytes.Buffer
	writer := bufio.NewWriter(&buf)
	if err := writeRequest(writer, original); err != nil {
		t.Fatal(err)
	}

	got, err := readRequest(bufio.NewReader(&buf))
	if err != nil {
		t.Fatal(err)
	}
	if got.Op != original.Op {
		t.Fatalf("op mismatch: %q != %q", got.Op, original.Op)
	}
	if len(got.Channels) != len(original.Channels) {
		t.Fatalf("channel count mismatch: %d != %d", len(got.Channels), len(original.Channels))
	}
	for i := range got.Channels {
		if got.Channels[i] != original.Channels[i] {
			t.Fatalf("channel[%d] mismatch: %q != %q", i, got.Channels[i], original.Channels[i])
		}
	}
	if string(got.Payload) != string(original.Payload) {
		t.Fatalf("payload mismatch: %v != %v", got.Payload, original.Payload)
	}
	if got.TimeoutMS != original.TimeoutMS {
		t.Fatalf("timeout mismatch: %d != %d", got.TimeoutMS, original.TimeoutMS)
	}
}

func TestProtocolResponseRoundTrip(t *testing.T) {
	original := response{
		OK:      true,
		Channel: "jobs",
		Payload: []byte{0x10, 0x20, 0x30},
	}

	var buf bytes.Buffer
	writer := bufio.NewWriter(&buf)
	if err := writeResponse(writer, original); err != nil {
		t.Fatal(err)
	}

	got, err := readResponse(bufio.NewReader(&buf))
	if err != nil {
		t.Fatal(err)
	}
	if got.OK != original.OK {
		t.Fatalf("ok mismatch: %v != %v", got.OK, original.OK)
	}
	if got.Channel != original.Channel {
		t.Fatalf("channel mismatch: %q != %q", got.Channel, original.Channel)
	}
	if string(got.Payload) != string(original.Payload) {
		t.Fatalf("payload mismatch: %v != %v", got.Payload, original.Payload)
	}
}
