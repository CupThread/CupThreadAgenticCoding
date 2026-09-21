package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// readInputFile reads flag input where "@" or "-" means stdin, otherwise a
// filesystem path.
func readInputFile(path string) ([]byte, error) {
	if path == "@" || path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
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
