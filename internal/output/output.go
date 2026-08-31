// Package output renders command results for humans (aligned tables) or for
// machines (JSON or YAML via the global --output flag).
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

// Format selects how structured results are rendered.
type Format string

const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatYAML  Format = "yaml"
)

// ParseFormat validates a --output flag value.
func ParseFormat(s string) (Format, error) {
	switch Format(s) {
	case FormatTable:
		return FormatTable, nil
	case FormatJSON:
		return FormatJSON, nil
	case FormatYAML:
		return FormatYAML, nil
	default:
		return "", fmt.Errorf("invalid output format %q: use table, json or yaml", s)
	}
}

// Writer is shared by all commands.
type Writer struct {
	w      io.Writer
	Format Format
}

// New creates a Writer writing to w in the given format.
func New(w io.Writer, format Format) *Writer {
	if format == "" {
		format = FormatTable
	}
	return &Writer{w: w, Format: format}
}

// Structured prints v as JSON or YAML; in table mode it is a no-op so
// commands can call it unconditionally when a structured format is active.
func (w *Writer) Structured(v any) error {
	switch w.Format {
	case FormatYAML:
		return w.encodeYAML(v)
	default:
		return w.encodeJSON(v)
	}
}

func (w *Writer) encodeJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	_, err = fmt.Fprintf(w.w, "%s\n", data)
	return err
}

// encodeYAML normalizes v through a JSON round-trip first so that
// json.RawMessage fields decode into real structures instead of being
// base64-encoded as raw bytes by the YAML encoder. Numbers keep their
// integer form (json.Number → int64) instead of rendering as 2e+07.
func (w *Writer) encodeYAML(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode YAML: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var norm any
	if err := dec.Decode(&norm); err != nil {
		return fmt.Errorf("encode YAML: %w", err)
	}
	out, err := yaml.Marshal(convertNumbers(norm))
	if err != nil {
		return fmt.Errorf("encode YAML: %w", err)
	}
	_, err = w.w.Write(out)
	return err
}

func convertNumbers(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			x[k] = convertNumbers(val)
		}
		return x
	case []any:
		for i, val := range x {
			x[i] = convertNumbers(val)
		}
		return x
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		f, _ := x.Float64()
		return f
	default:
		return v
	}
}

// Table prints a header row plus data rows with aligned columns.
// In JSON/YAML mode callers are expected to print structured output instead.
func (w *Writer) Table(headers []string, rows [][]string) {
	tw := tabwriter.NewWriter(w.w, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, joinTabs(headers))
	for _, row := range rows {
		fmt.Fprintln(tw, joinTabs(row))
	}
	tw.Flush()
}

// Printf writes a formatted line.
func (w *Writer) Printf(format string, args ...any) {
	fmt.Fprintf(w.w, format+"\n", args...)
}

func joinTabs(fields []string) string {
	out := ""
	for i, f := range fields {
		if i > 0 {
			out += "\t"
		}
		out += f
	}
	return out
}
