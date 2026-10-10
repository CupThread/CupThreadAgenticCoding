package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// Client-side request-body caps, mirroring the server's SEC-36 intake limits
// (see skills/cupthread-api/SKILL.md). Oversized local input must fail here —
// deterministically and before any HTTP request — instead of buffering a huge
// file or pipe all the way to the server's 413 (issue #142).
const (
	// maxConsoleBodyBytes caps bodies sent to console JSON surfaces: apps
	// settings, imports options, changelog bodies, and `api request` on
	// non-public routes.
	maxConsoleBodyBytes int64 = 1 << 20 // 1 MB
	// maxPublicBodyBytes caps bodies sent to public JSON intake
	// (/api/v1/public/...), e.g. the signed payment-attribute user update.
	maxPublicBodyBytes int64 = 256 << 10 // 256 KB
	// maxSecretBytes caps piped credentials (`--secret -`, `--token -`):
	// tokens are short, and the bound keeps a runaway pipe from hanging the
	// command forever.
	maxSecretBytes int64 = 64 << 10 // 64 KB
)

// InputTooLargeError reports local input that crossed the command's
// request-body cap. Structured mode renders it as the command's single stdout
// document (code "input_too_large") while the error itself still fails the
// command, so the human detail lands on stderr and the exit code stays
// non-zero.
type InputTooLargeError struct {
	Limit int64 // configured cap, in bytes
}

func (e *InputTooLargeError) Error() string {
	return fmt.Sprintf("input exceeds the %s request-body limit", humanBytes(e.Limit))
}

// humanBytes renders a byte cap the way the SEC-36 limits are documented.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%d MB", n/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// readInputFile reads flag input where "@" or "-" means stdin, otherwise a
// filesystem path, buffering at most max bytes. It reads exactly one byte
// past the cap to detect overflow, so an unbounded pipe or sparse file is cut
// off instead of being read into memory to exhaustion; the error is returned
// before any request is built or sent.
func readInputFile(path string, max int64) ([]byte, error) {
	var r io.Reader
	if path == "@" || path == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	return readBounded(r, max)
}

// readBounded is readInputFile's core, split out so tests can drive it with
// an arbitrary reader and prove the source is abandoned at the cap.
func readBounded(r io.Reader, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, &InputTooLargeError{Limit: max}
	}
	return data, nil
}

// reportInputTooLarge renders an InputTooLargeError as the command's single
// structured stdout document in --json/-o yaml mode. It returns the rendering
// error only: callers keep returning the original error so the process still
// exits non-zero with the human detail on stderr, matching how api request
// surfaces APIError payloads (issue #21).
func (a *app) reportInputTooLarge(err error) error {
	var e *InputTooLargeError
	if !errors.As(err, &e) || !a.structured() {
		return nil
	}
	return a.out.Structured(map[string]any{
		"error": e.Error(),
		"code":  "input_too_large",
		"limit": e.Limit,
	})
}

// mutationResult is the minimal machine-readable record of a completed
// mutation, emitted on stdout in --json/-o yaml mode by every mutating
// command the server answers without a body (issue #194). id carries the
// RESOLVED resource ID — for prefix-taking commands that is the full ID the
// lookup picked, which would otherwise be disclosed only by the human echo —
// and is omitted for whole-context actions (notifications read-all,
// integration disconnects name their provider instead).
type mutationResult struct {
	Action  string `json:"action"`
	ID      string `json:"id,omitempty"`
	Success bool   `json:"success"`
}

// emitMutationResult prints the mutation record in structured mode; human
// mode callers keep their existing ✓ echo, so table output stays
// byte-identical.
func (a *app) emitMutationResult(action, id string) error {
	return a.out.Structured(mutationResult{Action: action, ID: id, Success: true})
}

// warnf reports a non-fatal warning. In structured mode it goes to stderr so
// stdout stays a single machine-parseable document.
func (a *app) warnf(format string, args ...any) {
	if a.structured() {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
		return
	}
	a.out.Printf(format, args...)
}

// decodeStrictRawJSON validates that data holds exactly one JSON value and
// returns it verbatim as json.RawMessage (only trailing whitespace is
// allowed). Decoding into any instead would rebuild every number through
// float64, silently rewriting integers a float64 cannot represent exactly
// (issue #75); the RawMessage keeps the caller's bytes untouched. The strict
// trailing-data rejection follows issue #87: a single Decode call alone
// silently discarded everything past the first value, so a malformed --input
// file was sent truncated.
func decodeStrictRawJSON(data []byte) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse input JSON: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("parse input JSON: unexpected trailing data — exactly one JSON value expected")
	}
	return raw, nil
}
