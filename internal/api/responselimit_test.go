package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestDoBuffersResponseWithinLimit pins the normal path under the REL-1 cap:
// a response at or below the configured limit decodes exactly as before.
func TestDoBuffersResponseWithinLimit(t *testing.T) {
	const body = `{"ok":true,"pad":"xxxxxx"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	client := New(server.URL)
	client.MaxResponseBytes = 64
	var raw json.RawMessage
	if err := client.Do(context.Background(), "GET", "/x", nil, nil, &raw); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if string(raw) != body {
		t.Errorf("raw = %s, want %s", raw, body)
	}
}

// TestDoBuffersResponseAtExactLimit pins the boundary: a body of exactly
// limit bytes is a success, not a rejection.
func TestDoBuffersResponseAtExactLimit(t *testing.T) {
	const body = "12345678" // exactly 8 bytes
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	client := New(server.URL)
	client.MaxResponseBytes = int64(len(body))
	var raw json.RawMessage
	if err := client.Do(context.Background(), "GET", "/x", nil, nil, &raw); err != nil {
		t.Fatalf("Do at exact limit: %v", err)
	}
	if string(raw) != body {
		t.Errorf("raw = %s, want the full body %s", raw, body)
	}
}

// TestDoRejectsOversizedChunkedResponse covers the no-Content-Length path: a
// chunked body is read through a one-byte-over-limit window, so the client
// fails deterministically without buffering the whole payload.
func TestDoRejectsOversizedChunkedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bytes.Repeat([]byte("x"), 10))
		w.(http.Flusher).Flush()
		_, _ = w.Write(bytes.Repeat([]byte("x"), 10))
	}))
	defer server.Close()

	client := New(server.URL)
	client.MaxResponseBytes = 8
	err := client.Do(context.Background(), "GET", "/x", nil, nil, nil)
	var tooLarge *ResponseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected *ResponseTooLargeError, got %v", err)
	}
	if tooLarge.Method != "GET" || tooLarge.Path != "/x" || tooLarge.Limit != 8 {
		t.Errorf("tooLarge = %+v, want GET /x at limit 8", tooLarge)
	}
	if !strings.Contains(err.Error(), "GET /x") || !strings.Contains(err.Error(), "8 bytes") {
		t.Errorf("err = %q, want it to state the endpoint and the limit", err)
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		t.Error("size rejection must not surface as *APIError")
	}
}

// TestDoRejectsOversizedContentLengthResponse covers the known-size path: a
// Content-Length already over the limit is rejected before any body read.
func TestDoRejectsOversizedContentLengthResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(20))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bytes.Repeat([]byte("x"), 20))
	}))
	defer server.Close()

	client := New(server.URL)
	client.MaxResponseBytes = 8
	err := client.Do(context.Background(), "GET", "/x", nil, nil, nil)
	var tooLarge *ResponseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected *ResponseTooLargeError, got %v", err)
	}
	if tooLarge.Limit != 8 {
		t.Errorf("Limit = %d, want 8", tooLarge.Limit)
	}
}

// TestDoOversizedResponseIsSizeErrorAcrossStatuses pins that the bounded
// read runs before status mapping: an oversized body is a size error whether
// it arrives with a 2xx, 4xx, or 5xx status.
func TestDoOversizedResponseIsSizeErrorAcrossStatuses(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadRequest, http.StatusServiceUnavailable} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write(bytes.Repeat([]byte("x"), 64))
			}))
			defer server.Close()

			client := New(server.URL)
			client.NoRetry = true
			client.MaxResponseBytes = 8
			err := client.Do(context.Background(), "GET", "/x", nil, nil, nil)
			var tooLarge *ResponseTooLargeError
			if !errors.As(err, &tooLarge) {
				t.Fatalf("status %d: expected *ResponseTooLargeError, got %v", status, err)
			}
			var apiErr *APIError
			if errors.As(err, &apiErr) {
				t.Errorf("status %d: size rejection must not surface as *APIError", status)
			}
		})
	}
}

// TestDoOversizedTransientResponseIsNotRetried is the REL-1 regression: a
// 503 whose body blows the cap must fail in one attempt — replaying it would
// re-buffer an oversized payload on every retry.
func TestDoOversizedTransientResponseIsNotRetried(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write(bytes.Repeat([]byte("x"), 64))
	}))
	defer server.Close()

	var sleeps []time.Duration
	client := New(server.URL)
	client.MaxRetries = 3
	client.RetryBaseDelay = time.Millisecond
	client.RetryMaxDelay = time.Millisecond
	client.Sleeper = func(d time.Duration) { sleeps = append(sleeps, d) }
	client.MaxResponseBytes = 8

	err := client.Do(context.Background(), "GET", "/x", nil, nil, nil)
	var tooLarge *ResponseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected *ResponseTooLargeError, got %v", err)
	}
	if hits != 1 {
		t.Errorf("server hit %d times, want exactly 1 (size rejections are never retried)", hits)
	}
	if len(sleeps) != 0 {
		t.Errorf("slept %d times between retries, want none", len(sleeps))
	}
}

// TestUploadAppIconRejectsOversizedResponse pins the multipart path: the
// icon-upload response runs through the same cap and the error names the
// upload endpoint.
func TestUploadAppIconRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bytes.Repeat([]byte("x"), 64))
	}))
	defer server.Close()

	client := New(server.URL)
	client.MaxResponseBytes = 8
	_, err := client.UploadAppIcon(context.Background(), "ws_1", "app_1", "icon.png", []byte("png"))
	var tooLarge *ResponseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected *ResponseTooLargeError, got %v", err)
	}
	if !strings.Contains(err.Error(), "/api/v1/console/workspaces/ws_1/apps/app_1/icon") {
		t.Errorf("err = %q, want the upload endpoint named", err)
	}
}

// TestDoEnforcesDefaultResponseCap pins the production default: with no
// override, a body over DefaultMaxResponseBytes fails with the default named
// in the error.
func TestDoEnforcesDefaultResponseCap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, DefaultMaxResponseBytes+1))
	}))
	defer server.Close()

	client := New(server.URL)
	err := client.Do(context.Background(), "GET", "/x", nil, nil, nil)
	var tooLarge *ResponseTooLargeError
	if !errors.As(err, &tooLarge) || tooLarge.Limit != DefaultMaxResponseBytes {
		t.Fatalf("err = %v, want *ResponseTooLargeError at the default cap", err)
	}
	if !strings.Contains(err.Error(), "10 MiB") {
		t.Errorf("err = %q, want the 10 MiB default named", err)
	}
}

// TestReadResponseBodySkipsContentLengthPrecheckForHead unit-covers the HEAD
// carve-out: a HEAD response advertises the length of the body it does not
// carry, so the Content-Length precheck must not reject it — while the same
// advertised length on a GET is rejected.
func TestReadResponseBodySkipsContentLengthPrecheckForHead(t *testing.T) {
	client := &Client{MaxResponseBytes: 8}
	resp := &http.Response{
		StatusCode:    http.StatusOK,
		ContentLength: 1 << 20,
		Body:          io.NopCloser(strings.NewReader("")),
	}

	body, err := client.readResponseBody(http.MethodHead, "/x", resp)
	if err != nil {
		t.Fatalf("HEAD with advertised 1 MiB: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("HEAD body = %q, want empty", body)
	}

	resp.Body = io.NopCloser(strings.NewReader(""))
	if _, err := client.readResponseBody(http.MethodGet, "/x", resp); !errors.As(err, new(*ResponseTooLargeError)) {
		t.Fatalf("GET with advertised 1 MiB: err = %v, want *ResponseTooLargeError", err)
	}
}

// TestResponseTooLargeErrorMessage pins the rendered error: endpoint first,
// then the configured limit in human form.
func TestResponseTooLargeErrorMessage(t *testing.T) {
	e := &ResponseTooLargeError{Method: "GET", Path: "/api/v1/console/me", Limit: DefaultMaxResponseBytes}
	if want := "GET /api/v1/console/me: response body exceeds the 10 MiB response-size limit"; e.Error() != want {
		t.Errorf("Error() = %q, want %q", e.Error(), want)
	}
	e = &ResponseTooLargeError{Method: "POST", Path: "/x", Limit: 8}
	if want := "POST /x: response body exceeds the 8 bytes response-size limit"; e.Error() != want {
		t.Errorf("Error() = %q, want %q", e.Error(), want)
	}
}
