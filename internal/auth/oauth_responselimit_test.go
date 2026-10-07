package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/CupThread/CupThreadAgenticCoding/internal/httpx"
)

// writeChunkedForm answers with status and n bytes written (and flushed) in
// chunk-size pieces, so the response carries no Content-Length and the
// client sees ContentLength -1.
func writeChunkedForm(w http.ResponseWriter, status, n, chunk int) {
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

func TestPostFormAcceptsExactLimitBody(t *testing.T) {
	body := `{"access_token":"` + strings.Repeat("a", 16) + `"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	got, err := postFormLimit(context.Background(), server.URL, url.Values{}, int64(len(body)))
	if err != nil {
		t.Fatalf("postFormLimit: %v", err)
	}
	if string(got) != body {
		t.Errorf("body = %q, want the exact %d-byte response", got, len(body))
	}
}

func TestPostFormRejectsOversizedChunkedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeChunkedForm(w, http.StatusOK, 4096, 512)
	}))
	defer server.Close()

	_, err := postFormLimit(context.Background(), server.URL, url.Values{}, 512)
	var tooLarge *httpx.ResponseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected *ResponseTooLargeError, got %v", err)
	}
	if tooLarge.Limit != 512 || tooLarge.DeclaredLength != 0 {
		t.Errorf("error = %+v, want Limit 512 and a streamed (undeclared) rejection", tooLarge)
	}
	if !strings.Contains(err.Error(), "512-byte limit") || !strings.Contains(err.Error(), "POST "+server.URL) {
		t.Errorf("error %q does not state the endpoint and the limit", err.Error())
	}
}

func TestPostFormRejectsDeclaredOversizeContentLengthUnread(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4096")
		_, _ = w.Write(bytes.Repeat([]byte("a"), 4096))
	}))
	defer server.Close()

	_, err := postFormLimit(context.Background(), server.URL, url.Values{}, 512)
	var tooLarge *httpx.ResponseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected *ResponseTooLargeError, got %v", err)
	}
	if tooLarge.DeclaredLength != 4096 {
		t.Errorf("DeclaredLength = %d, want the 4096 the pre-check rejected unread", tooLarge.DeclaredLength)
	}
}

func TestPostFormRejectsOversizedOAuthErrorBody(t *testing.T) {
	// An oversized error response must fail on size, never decode into the
	// RFC 6749 error shape.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeChunkedForm(w, http.StatusBadRequest, 4096, 512)
	}))
	defer server.Close()

	_, err := postFormLimit(context.Background(), server.URL, url.Values{}, 512)
	var tooLarge *httpx.ResponseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected *ResponseTooLargeError, got %v", err)
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		t.Fatalf("oversized 400 body must not surface as an OAuth *APIError")
	}
}

func TestPostFormParsesOAuthErrorWithinLimit(t *testing.T) {
	// Small error bodies keep their RFC 6749 parsing under the cap.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"code expired"}`))
	}))
	defer server.Close()

	_, err := postFormLimit(context.Background(), server.URL, url.Values{}, 512)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_grant" {
		t.Fatalf("expected APIError invalid_grant, got %v", err)
	}
}
