// Package httpx holds the HTTP plumbing shared by the API client and the
// OAuth stack. Its first resident is bounded response reading (REL-1,
// issue #137): every response body the CLI buffers is capped, so a hostile
// or misbehaving endpoint cannot exhaust agent memory — including the
// chunked case, where no Content-Length announces the size in advance.
package httpx

import (
	"fmt"
	"io"
)

// DefaultMaxResponseBytes is the production cap on one response body: far
// above any JSON document the CupThread API serves (lists are paginated at
// 50–100 records), far below the allocation a CLI process should ever spend
// on a single response.
const DefaultMaxResponseBytes = 16 << 20 // 16 MiB

// ResponseTooLargeError reports a response rejected for exceeding the
// client's response-size limit. Callers wrap it with the endpoint context
// (e.g. "%s %s: read response: %w"), so errors.As still reaches this type
// while the rendered message states both the endpoint and the limit. No
// part of the oversized body is retained.
type ResponseTooLargeError struct {
	// Limit is the response-size cap the body exceeded.
	Limit int64
	// DeclaredLength is the Content-Length the server declared, set only
	// when the pre-check rejected the response without reading it; a body
	// rejected while streaming (no or lying Content-Length) leaves it 0.
	DeclaredLength int64
}

func (e *ResponseTooLargeError) Error() string {
	if e.DeclaredLength > 0 {
		return fmt.Sprintf("response body exceeds the %d-byte limit (declared Content-Length %d)", e.Limit, e.DeclaredLength)
	}
	return fmt.Sprintf("response body exceeds the %d-byte limit", e.Limit)
}

// ReadBounded reads r into memory under limit bytes. A declared
// contentLength above limit is rejected before a single byte is read;
// otherwise the body streams through a limit+1 window, so a chunked or
// lying response stops allocating one byte past the cap. A non-nil error is
// either a *ResponseTooLargeError or the reader's own failure. A limit <= 0
// falls back to DefaultMaxResponseBytes.
func ReadBounded(r io.Reader, contentLength, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = DefaultMaxResponseBytes
	}
	if contentLength > limit {
		return nil, &ResponseTooLargeError{Limit: limit, DeclaredLength: contentLength}
	}
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, &ResponseTooLargeError{Limit: limit}
	}
	return data, nil
}
