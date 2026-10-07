package httpx

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// countingReader fails the test if it is ever read; the Content-Length
// pre-check must reject a declared oversize body without touching it.
type countingReader struct {
	t       *testing.T
	reads   int
	content string
	pos     int
}

func (r *countingReader) Read(p []byte) (int, error) {
	r.reads++
	if r.pos >= len(r.content) {
		return 0, io.EOF
	}
	n := copy(p, r.content[r.pos:])
	r.pos += n
	return n, nil
}

func TestReadBoundedWithinLimit(t *testing.T) {
	data, err := ReadBounded(strings.NewReader("hello"), -1, 10)
	if err != nil {
		t.Fatalf("ReadBounded: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("data = %q, want %q", data, "hello")
	}
}

func TestReadBoundedExactLimitAccepted(t *testing.T) {
	body := strings.Repeat("a", 10)
	data, err := ReadBounded(strings.NewReader(body), int64(len(body)), 10)
	if err != nil {
		t.Fatalf("ReadBounded: %v", err)
	}
	if string(data) != body {
		t.Errorf("data length = %d, want %d", len(data), len(body))
	}
}

func TestReadBoundedDeclaredContentLengthRejectedUnread(t *testing.T) {
	r := &countingReader{t: t, content: strings.Repeat("a", 20)}
	_, err := ReadBounded(r, 20, 10)
	var tooLarge *ResponseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected *ResponseTooLargeError, got %v", err)
	}
	if tooLarge.Limit != 10 || tooLarge.DeclaredLength != 20 {
		t.Errorf("error = %+v, want Limit 10 and DeclaredLength 20", tooLarge)
	}
	if r.reads != 0 {
		t.Errorf("pre-check read the body %d time(s); it must reject unread", r.reads)
	}
	if got := tooLarge.Error(); got != "response body exceeds the 10-byte limit (declared Content-Length 20)" {
		t.Errorf("Error() = %q", got)
	}
}

func TestReadBoundedStreamedOverLimitRejected(t *testing.T) {
	body := strings.Repeat("a", 15)
	data, err := ReadBounded(strings.NewReader(body), -1, 10)
	if err == nil {
		t.Fatalf("expected an error for a %d-byte body over the 10-byte limit", len(body))
	}
	if data != nil {
		t.Errorf("data = %d bytes, want none of the oversized payload retained", len(data))
	}
	var tooLarge *ResponseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected *ResponseTooLargeError, got %v", err)
	}
	if tooLarge.Limit != 10 || tooLarge.DeclaredLength != 0 {
		t.Errorf("error = %+v, want Limit 10 and no DeclaredLength (streamed rejection)", tooLarge)
	}
	if got := tooLarge.Error(); got != "response body exceeds the 10-byte limit" {
		t.Errorf("Error() = %q", got)
	}
}

func TestReadBoundedNonLimitErrorPropagates(t *testing.T) {
	want := errors.New("connection reset")
	_, err := ReadBounded(errReader{err: want}, -1, 10)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want the reader's own failure", err)
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestReadBoundedNonPositiveLimitAppliesDefault(t *testing.T) {
	r := &countingReader{t: t}
	_, err := ReadBounded(r, DefaultMaxResponseBytes+1, 0)
	var tooLarge *ResponseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected *ResponseTooLargeError, got %v", err)
	}
	if tooLarge.Limit != DefaultMaxResponseBytes {
		t.Errorf("Limit = %d, want the %d-byte default", tooLarge.Limit, DefaultMaxResponseBytes)
	}
	if r.reads != 0 {
		t.Errorf("default-resolution read the body %d time(s)", r.reads)
	}
}

func TestReadBoundedWrapsCleanly(t *testing.T) {
	_, err := ReadBounded(strings.NewReader(strings.Repeat("a", 11)), -1, 10)
	// Mirrors the callers' endpoint wrapping, which must keep the typed
	// error reachable through errors.As.
	wrapped := fmt.Errorf("GET /x: read response: %w", err)
	var tooLarge *ResponseTooLargeError
	if !errors.As(wrapped, &tooLarge) {
		t.Fatalf("errors.As lost the typed error through wrapping: %v", wrapped)
	}
	if !strings.Contains(wrapped.Error(), "10-byte limit") {
		t.Errorf("wrapped message %q does not state the limit", wrapped.Error())
	}
}
