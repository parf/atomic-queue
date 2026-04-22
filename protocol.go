package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	opPush byte = 1
	opPop  byte = 2

	statusOK    byte = 1
	statusError byte = 2

	protocolMaxChannels  = 1024
	protocolMaxErrorSize = 64 << 10
)

var protocolByteOrder = binary.BigEndian

func writeRequest(w *bufio.Writer, req request) error {
	var op byte
	switch req.Op {
	case "push":
		op = opPush
	case "pop":
		op = opPop
	default:
		return fmt.Errorf("unknown request op %q", req.Op)
	}

	if len(req.Channels) > protocolMaxChannels {
		return fmt.Errorf("too many channels: %d", len(req.Channels))
	}
	if len(req.Payload) > defaultMaxBytes {
		return fmt.Errorf("payload exceeds maximum size of %d bytes", defaultMaxBytes)
	}

	if err := w.WriteByte(op); err != nil {
		return err
	}
	if err := binary.Write(w, protocolByteOrder, req.TimeoutMS); err != nil {
		return err
	}
	if err := binary.Write(w, protocolByteOrder, uint16(len(req.Channels))); err != nil {
		return err
	}
	for _, channel := range req.Channels {
		if len(channel) > 0xffff {
			return fmt.Errorf("channel too long: %d", len(channel))
		}
		if err := binary.Write(w, protocolByteOrder, uint16(len(channel))); err != nil {
			return err
		}
		if _, err := io.WriteString(w, channel); err != nil {
			return err
		}
	}
	if err := binary.Write(w, protocolByteOrder, uint32(len(req.Payload))); err != nil {
		return err
	}
	if _, err := w.Write(req.Payload); err != nil {
		return err
	}
	return w.Flush()
}

func readRequest(r *bufio.Reader) (request, error) {
	op, err := r.ReadByte()
	if err != nil {
		return request{}, err
	}

	var timeoutMS int64
	if err := binary.Read(r, protocolByteOrder, &timeoutMS); err != nil {
		return request{}, err
	}

	var channelCount uint16
	if err := binary.Read(r, protocolByteOrder, &channelCount); err != nil {
		return request{}, err
	}
	if int(channelCount) > protocolMaxChannels {
		return request{}, fmt.Errorf("too many channels: %d", channelCount)
	}

	channels := make([]string, 0, channelCount)
	for i := 0; i < int(channelCount); i++ {
		channel, err := readString16(r)
		if err != nil {
			return request{}, err
		}
		channels = append(channels, channel)
	}

	payload, err := readBytes32(r, defaultMaxBytes)
	if err != nil {
		return request{}, err
	}

	req := request{
		Channels:  channels,
		Payload:   payload,
		TimeoutMS: timeoutMS,
	}
	switch op {
	case opPush:
		req.Op = "push"
	case opPop:
		req.Op = "pop"
	default:
		return request{}, fmt.Errorf("unknown request op %d", op)
	}
	return req, nil
}

func writeResponse(w *bufio.Writer, resp response) error {
	status := statusOK
	if !resp.OK {
		status = statusError
	}
	if len(resp.Payload) > defaultMaxBytes {
		return fmt.Errorf("payload exceeds maximum size of %d bytes", defaultMaxBytes)
	}
	if len(resp.Channel) > 0xffff {
		return fmt.Errorf("channel too long: %d", len(resp.Channel))
	}
	if len(resp.Error) > protocolMaxErrorSize {
		return fmt.Errorf("error message too long: %d", len(resp.Error))
	}

	if err := w.WriteByte(status); err != nil {
		return err
	}
	if err := writeString16(w, resp.Channel); err != nil {
		return err
	}
	if err := writeBytes32(w, resp.Payload); err != nil {
		return err
	}
	if err := writeString32(w, resp.Error); err != nil {
		return err
	}
	return w.Flush()
}

func readResponse(r *bufio.Reader) (response, error) {
	status, err := r.ReadByte()
	if err != nil {
		return response{}, err
	}

	channel, err := readString16(r)
	if err != nil {
		return response{}, err
	}
	payload, err := readBytes32(r, defaultMaxBytes)
	if err != nil {
		return response{}, err
	}
	errorText, err := readString32(r, protocolMaxErrorSize)
	if err != nil {
		return response{}, err
	}

	resp := response{
		Channel: channel,
		Payload: payload,
		Error:   errorText,
	}
	switch status {
	case statusOK:
		resp.OK = true
	case statusError:
		resp.OK = false
	default:
		return response{}, fmt.Errorf("unknown response status %d", status)
	}
	return resp, nil
}

func writeString16(w io.Writer, value string) error {
	if len(value) > 0xffff {
		return fmt.Errorf("string too long: %d", len(value))
	}
	if err := binary.Write(w, protocolByteOrder, uint16(len(value))); err != nil {
		return err
	}
	_, err := io.WriteString(w, value)
	return err
}

func writeString32(w io.Writer, value string) error {
	if err := binary.Write(w, protocolByteOrder, uint32(len(value))); err != nil {
		return err
	}
	_, err := io.WriteString(w, value)
	return err
}

func writeBytes32(w io.Writer, value []byte) error {
	if err := binary.Write(w, protocolByteOrder, uint32(len(value))); err != nil {
		return err
	}
	_, err := w.Write(value)
	return err
}

func readString16(r io.Reader) (string, error) {
	var n uint16
	if err := binary.Read(r, protocolByteOrder, &n); err != nil {
		return "", err
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func readString32(r io.Reader, max int) (string, error) {
	buf, err := readBytes32(r, max)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

func readBytes32(r io.Reader, max int) ([]byte, error) {
	var n uint32
	if err := binary.Read(r, protocolByteOrder, &n); err != nil {
		return nil, err
	}
	if int64(n) > int64(max) {
		return nil, fmt.Errorf("frame too large: %d > %d", n, max)
	}
	if n == 0 {
		return nil, nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return buf, nil
}
