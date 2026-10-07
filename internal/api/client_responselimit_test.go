package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/CupThread/CupThreadAgenticCoding/internal/httpx"
)

// writeChunked answers with status and n bytes of 'a' written (and flushed)
// in chunk-size pieces, so the response has no Content-Length and Go's
// client sees ContentLength -1 — the chunked path the size cap must also
// bound.
func writeChunked(w http.ResponseWriter, status, n, chunk int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	flusher, canFlush := w.(http.Flusher)
	body := bytes.Repeat([]byte("a"), chunk)
	for written := 0; written < n; written += chunk {
		size := chunk
		if written+chunk > n {
			size = n - written
		}
		_, _ = w.Write(body[:size])
		if canFlush {
			flusher.Flush()
		}
	}
}

func TestDoDecodesExactLimitResponse(t *testing.T) {
	// A body exactly at the cap must decode byte-for-byte, not trip the
	// limit+1 streaming window.
	body := `{"ok":"` + strings.Repeat("a", 10) + `"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	client := New(server.URL)
	client.MaxResponseBytes = int64(len(body))
	var raw json.RawMessage
	if err := client.Do(context.Background(), "GET", "/x", nil, nil, &raw); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if string(raw) != body {
		t.Errorf("raw = %q, want the exact %d-byte body", raw, len(body))
	}
}

func TestDoRejectsOversizedChunkedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeChunked(w, http.StatusOK, 4096, 512)
	}))
	defer server.Close()

	client := New(server.URL)
	client.MaxResponseBytes = 512
	err := client.Do(context.Background(), "GET", "/api/v1/console/me", nil, nil, nil)
	var tooLarge *httpx.ResponseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected *ResponseTooLargeError, got %v", err)
	}
	if tooLarge.Limit != 512 || tooLarge.DeclaredLength != 0 {
		t.Errorf("error = %+v, want Limit 512 and a streamed (undeclared) rejection", tooLarge)
	}
	if !strings.Contains(err.Error(), "512-byte limit") {
		t.Errorf("error %q does not state the configured limit", err.Error())
	}
}

func TestDoRejectsDeclaredOversizeContentLengthUnread(t *testing.T) {
	const limit = 512
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(limit+1))
		_, _ = w.Write(bytes.Repeat([]byte("a"), limit+1))
	}))
	defer server.Close()

	client := New(server.URL)
	client.MaxResponseBytes = limit
	err := client.Do(context.Background(), "GET", "/x", nil, nil, nil)
	var tooLarge *httpx.ResponseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected *ResponseTooLargeError, got %v", err)
	}
	if tooLarge.DeclaredLength != limit+1 {
		t.Errorf("DeclaredLength = %d, want the %d the pre-check rejected unread", tooLarge.DeclaredLength, limit+1)
	}
	if !strings.Contains(err.Error(), "GET /x: read response:") {
		t.Errorf("error %q does not state the endpoint", err.Error())
	}
}

func TestDoDoesNotRetryOversizedTransientResponse(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		writeChunked(w, http.StatusServiceUnavailable, 4096, 512)
	}))
	defer server.Close()

	client := New(server.URL)
	client.MaxResponseBytes = 512
	var sleeps []time.Duration
	client.Sleeper = func(d time.Duration) { sleeps = append(sleeps, d) }

	err := client.Do(context.Background(), "GET", "/x", nil, nil, nil)
	var tooLarge *httpx.ResponseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected *ResponseTooLargeError, got %v", err)
	}
	if requests != 1 {
		t.Errorf("made %d requests; a size rejection must end the request immediately", requests)
	}
	if len(sleeps) != 0 {
		t.Errorf("slept %v for a response that must not be retried", sleeps)
	}
}

func TestDoRejectsOversizedBodyWhateverStatus(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadRequest, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeChunked(w, status, 4096, 512)
			}))
			defer server.Close()

			client := New(server.URL)
			client.MaxResponseBytes = 512
			err := client.Do(context.Background(), "GET", "/x", nil, nil, nil)
			var tooLarge *httpx.ResponseTooLargeError
			if !errors.As(err, &tooLarge) {
				t.Fatalf("status %d: expected *ResponseTooLargeError, got %v", status, err)
			}
			var apiErr *APIError
			if errors.As(err, &apiErr) {
				t.Fatalf("status %d: oversized body must not decode into an *APIError", status)
			}
		})
	}
}

func TestUploadAppIconRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeChunked(w, http.StatusOK, 4096, 512)
	}))
	defer server.Close()

	client := New(server.URL)
	client.MaxResponseBytes = 512
	_, err := client.UploadAppIcon(context.Background(), "ws_1", "app_1", "icon.png", []byte("not an image"))
	var tooLarge *httpx.ResponseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected *ResponseTooLargeError, got %v", err)
	}
	if !strings.Contains(err.Error(), "upload /api/v1/console/workspaces/ws_1/apps/app_1/icon") {
		t.Errorf("error %q does not state the upload endpoint", err.Error())
	}
}

func TestUploadAppIconParsesErrorWithinLimit(t *testing.T) {
	// The cap must not interfere with small error bodies: status, code, and
	// message keep parsing on the multipart path too.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"Image failed validation","code":"invalid_image"}`))
	}))
	defer server.Close()

	client := New(server.URL)
	client.MaxResponseBytes = 64
	_, err := client.UploadAppIcon(context.Background(), "ws_1", "app_1", "icon.png", []byte("not an image"))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if apiErr.Code != "invalid_image" || apiErr.Status != http.StatusBadRequest {
		t.Errorf("apiErr = %+v, want the parsed 400 code", apiErr)
	}
}
