package auth

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// TestPostFormRejectsOversizedChunkedResponse covers the no-Content-Length
// path (REL-1): a chunked token/device response is read through a one-byte-
// over-limit window and rejected without buffering the whole payload.
func TestPostFormRejectsOversizedChunkedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bytes.Repeat([]byte("x"), int(maxOAuthResponseBytes)))
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("x"))
	}))
	defer server.Close()

	_, err := postForm(context.Background(), server.URL, nil)
	var tooLarge *responseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected *responseTooLargeError, got %v", err)
	}
	if !strings.Contains(err.Error(), server.URL) || !strings.Contains(err.Error(), "1 MiB") {
		t.Errorf("err = %q, want it to state the endpoint and the limit", err)
	}
}

// TestPostFormRejectsOversizedContentLengthResponse covers the known-size
// path: a Content-Length already over the cap is rejected before any read.
func TestPostFormRejectsOversizedContentLengthResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(maxOAuthResponseBytes+1, 10))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bytes.Repeat([]byte("x"), int(maxOAuthResponseBytes)+1))
	}))
	defer server.Close()

	_, err := postForm(context.Background(), server.URL, nil)
	if !errors.As(err, new(*responseTooLargeError)) {
		t.Fatalf("expected *responseTooLargeError, got %v", err)
	}
}

// TestPostFormBuffersResponseAtExactLimit pins the boundary: a body of
// exactly maxOAuthResponseBytes bytes is delivered, not rejected.
func TestPostFormBuffersResponseAtExactLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("a"), int(maxOAuthResponseBytes)))
	}))
	defer server.Close()

	body, err := postForm(context.Background(), server.URL, nil)
	if err != nil {
		t.Fatalf("postForm at exact limit: %v", err)
	}
	if int64(len(body)) != maxOAuthResponseBytes {
		t.Errorf("body length = %d, want %d", len(body), maxOAuthResponseBytes)
	}
}

// TestPostFormOversizedErrorBodyIsSizeError pins that the bounded read runs
// before the RFC 6749 error mapping: an oversized 4xx body is a size error,
// not an oauth *APIError.
func TestPostFormOversizedErrorBodyIsSizeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(bytes.Repeat([]byte("x"), int(maxOAuthResponseBytes)+16))
	}))
	defer server.Close()

	_, err := postToken(context.Background(), server.URL, nil)
	var tooLarge *responseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected *responseTooLargeError, got %v", err)
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		t.Error("size rejection must not surface as the RFC 6749 *APIError")
	}
}

// TestRefreshRejectsOversizedTokenResponse pins the user-facing path: a
// token endpoint that answers a normal refresh with an oversized body fails
// with the endpoint named in the error.
func TestRefreshRejectsOversizedTokenResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(maxOAuthResponseBytes+1, 10))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bytes.Repeat([]byte("x"), int(maxOAuthResponseBytes)+1))
	}))
	defer server.Close()

	_, err := Refresh(context.Background(), server.URL, "cupthread-cli", "cpr_old")
	var tooLarge *responseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected *responseTooLargeError, got %v", err)
	}
	if !strings.Contains(err.Error(), server.URL) {
		t.Errorf("err = %q, want the token endpoint named", err)
	}
}
