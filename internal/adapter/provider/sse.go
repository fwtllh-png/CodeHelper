package provider

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

const defaultMaxSSEEventBytes = 1 << 20

var ErrSSEEventTooLarge = errors.New("SSE event exceeds size limit")

type SSERecord struct {
	Event string
	Data  string
}

type SSEDecoder struct {
	scanner *bufio.Scanner
	event   string
	data    []string
	bytes   int
	done    bool
}

func NewSSEDecoder(reader io.Reader) *SSEDecoder {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), defaultMaxSSEEventBytes)
	return &SSEDecoder{scanner: scanner}
}

func (d *SSEDecoder) Next() (SSERecord, error) {
	if d.done {
		return SSERecord{}, io.EOF
	}
	for d.scanner.Scan() {
		line := strings.TrimSuffix(d.scanner.Text(), "\r")
		if line == "" {
			if record, ok := d.take(); ok {
				return record, nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			d.event = value
		case "data":
			d.bytes += len(value)
			if d.bytes > defaultMaxSSEEventBytes {
				d.done = true
				return SSERecord{}, ErrSSEEventTooLarge
			}
			d.data = append(d.data, value)
		}
	}
	d.done = true
	if err := d.scanner.Err(); err != nil {
		return SSERecord{}, err
	}
	if record, ok := d.take(); ok {
		return record, nil
	}
	return SSERecord{}, io.EOF
}

func (d *SSEDecoder) take() (SSERecord, bool) {
	if len(d.data) == 0 {
		d.event = ""
		return SSERecord{}, false
	}
	record := SSERecord{Event: d.event, Data: strings.Join(d.data, "\n")}
	d.event = ""
	d.data = d.data[:0]
	d.bytes = 0
	return record, true
}

func Drain(stream Stream) ([]StreamEvent, error) {
	defer stream.Close()
	var events []StreamEvent
	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return events, nil
		}
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
}
