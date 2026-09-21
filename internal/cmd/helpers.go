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

// decodeStrictJSON decodes exactly one JSON value and rejects any trailing
// content after it (only whitespace is allowed). A single Decode call alone
// silently discarded everything past the first value (issue #87), so a
// malformed --input file was sent truncated; this keeps the raw escape hatch
// as strict as the json.Unmarshal-based --input consumers.
func decodeStrictJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var body any
	if err := dec.Decode(&body); err != nil {
		return nil, fmt.Errorf("parse input JSON: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("parse input JSON: unexpected trailing data — exactly one JSON value expected")
	}
	return body, nil
}
