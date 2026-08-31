package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to the CupThread API. It is safe for concurrent use.
type Client struct {
	// BaseURL is the API origin, e.g. https://api.cupthread.com.
	BaseURL string
	HTTP    *http.Client
	// WorkspaceID, when set, is sent as X-Workspace-Id on every request.
	WorkspaceID string
	// Token returns the bearer credential. It is consulted per request so
	// OAuth tokens can be refreshed transparently.
	Token func(ctx context.Context) (string, error)
}

// New creates a client for baseURL.
func New(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

// APIError is a non-2xx API response.
type APIError struct {
	Status  int
	Message string
	Code    string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s (HTTP %d, code=%s)", e.Message, e.Status, e.Code)
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
}

// TierLimit returns true when the error is a subscription tier limit (402).
func (e *APIError) TierLimit() bool { return e.Status == http.StatusPaymentRequired }

// Do performs an API request. Path must start with "/" and is appended to
// BaseURL verbatim. When body is non-nil it is JSON-encoded; when out is
// non-nil the response body is decoded into it (*json.RawMessage receives
// the undecoded bytes).
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.WorkspaceID != "" {
		req.Header.Set("X-Workspace-Id", c.WorkspaceID)
	}
	if c.Token != nil {
		token, err := c.Token(ctx)
		if err != nil {
			return err
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	if query != nil {
		req.URL.RawQuery = query.Encode()
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s %s: read response: %w", method, path, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := &APIError{
			Status:  resp.StatusCode,
			Message: strings.TrimSpace(http.StatusText(resp.StatusCode)),
		}
		var parsed struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		if json.Unmarshal(data, &parsed) == nil && parsed.Error != "" {
			apiErr.Message = parsed.Error
			apiErr.Code = parsed.Code
		}
		if apiErr.TierLimit() {
			return fmt.Errorf("tier limit: %w", apiErr)
		}
		return apiErr
	}

	if out == nil {
		return nil
	}
	if raw, ok := out.(*json.RawMessage); ok {
		*raw = data
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s %s: decode response: %w", method, path, err)
	}
	return nil
}

// UploadAppIcon uploads an image (falling back to R2 storage) and returns its
// public URL, mirroring the Console's uploadAppIcon behavior.
func (c *Client) UploadAppIcon(ctx context.Context, appKey, filename string, data []byte) (string, error) {
	u, err := c.uploadFile(ctx, "/api/v1/uploads/images", appKey, filename, data)
	if err == nil {
		return u, nil
	}
	return c.uploadFile(ctx, "/api/v1/uploads/r2", appKey, filename, data)
}

func (c *Client) uploadFile(ctx context.Context, endpoint, appKey, filename string, data []byte) (string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("appKey", appKey); err != nil {
		return "", err
	}
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+endpoint, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if c.Token != nil {
		token, err := c.Token(ctx)
		if err != nil {
			return "", err
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &APIError{Status: resp.StatusCode, Message: strings.TrimSpace(string(body))}
	}
	var out UploadImageResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("upload %s: decode response: %w", endpoint, err)
	}
	return out.URL, nil
}
