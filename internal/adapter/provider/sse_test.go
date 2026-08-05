package provider

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestSSEDecoderIsIndependentOfChunkBoundaries(t *testing.T) {
	data := []byte(": comment\r\nevent: delta\r\ndata: {\"text\":\"hel\r\n")
	data = append(data, []byte("data: lo\"}\r\n\r\ndata: [DONE]\n\n")...)
	want := []SSERecord{
		{Event: "delta", Data: "{\"text\":\"hel\nlo\"}"},
		{Data: "[DONE]"},
	}

	for chunkSize := 1; chunkSize <= len(data); chunkSize++ {
		decoder := NewSSEDecoder(&chunkReader{reader: bytes.NewReader(data), size: chunkSize})
		var got []SSERecord
		for {
			record, err := decoder.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("chunk size %d: %v", chunkSize, err)
			}
			got = append(got, record)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("chunk size %d records = %#v, want %#v", chunkSize, got, want)
		}
	}
}

func TestSSEDecoderRejectsOversizedEvent(t *testing.T) {
	decoder := NewSSEDecoder(strings.NewReader("data: " + strings.Repeat("x", defaultMaxSSEEventBytes+1) + "\n\n"))
	if _, err := decoder.Next(); err == nil {
		t.Fatal("Next() error = nil, want size error")
	}
}

func FuzzStreamParser(f *testing.F) {
	f.Add([]byte("data: {\"ok\":true}\n\ndata: [DONE]\n\n"), uint8(1))
	f.Add([]byte("event: ping\r\ndata: {}\r\n\r\n"), uint8(7))
	f.Add([]byte("malformed\n\n"), uint8(3))

	f.Fuzz(func(t *testing.T, data []byte, chunk uint8) {
		if len(data) > 2<<20 {
			t.Skip()
		}
		size := int(chunk)%64 + 1
		decoder := NewSSEDecoder(&chunkReader{reader: bytes.NewReader(data), size: size})
		for range 10_000 {
			_, err := decoder.Next()
			if err != nil {
				return
			}
		}
		t.Fatal("decoder did not terminate")
	})
}

type chunkReader struct {
	reader io.Reader
	size   int
}

func (r *chunkReader) Read(buffer []byte) (int, error) {
	if len(buffer) > r.size {
		buffer = buffer[:r.size]
	}
	return r.reader.Read(buffer)
}
