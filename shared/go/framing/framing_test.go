package framing

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		size int
	}{
		{"zero", 0},
		{"one", 1},
		{"small", 17},
		{"max", MaxFrame},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := bytes.Repeat([]byte{0xAB}, tc.size)
			var buf bytes.Buffer
			if err := Write(&buf, payload); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if buf.Len() != 4+tc.size {
				t.Fatalf("encoded length %d, want %d", buf.Len(), 4+tc.size)
			}
			got, err := Read(&buf)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("payload mismatch (len %d vs %d)", len(got), len(payload))
			}
			if buf.Len() != 0 {
				t.Fatalf("%d bytes left unread", buf.Len())
			}
		})
	}
}

func TestHeaderEncoding(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 0, 0, 2, 'h', 'i'}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("got % x, want % x", buf.Bytes(), want)
	}
}

func TestWriteTooLarge(t *testing.T) {
	var buf bytes.Buffer
	err := Write(&buf, make([]byte, MaxFrame+1))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("wrote %d bytes for a rejected frame", buf.Len())
	}
}

func TestReadErrors(t *testing.T) {
	cases := []struct {
		name  string
		input []byte
		want  error
	}{
		{"empty stream", nil, io.EOF},
		{"short header", []byte{0, 0}, io.ErrUnexpectedEOF},
		{"short payload", []byte{0, 0, 0, 5, 'a', 'b'}, io.ErrUnexpectedEOF},
		{"over max", []byte{0, 0x10, 0, 1}, ErrTooLarge},
		{"huge", []byte{0xFF, 0xFF, 0xFF, 0xFF}, ErrTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Read(bytes.NewReader(tc.input))
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestMultipleFrames(t *testing.T) {
	var buf bytes.Buffer
	frames := [][]byte{[]byte("a"), {}, []byte("ccc")}
	for _, f := range frames {
		if err := Write(&buf, f); err != nil {
			t.Fatal(err)
		}
	}
	for i, want := range frames {
		got, err := Read(&buf)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("frame %d: got %q want %q", i, got, want)
		}
	}
	if _, err := Read(&buf); !errors.Is(err, io.EOF) {
		t.Fatalf("after last frame: got %v, want io.EOF", err)
	}
}
