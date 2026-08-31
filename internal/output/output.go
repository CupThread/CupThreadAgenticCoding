// Package output renders command results for humans (aligned tables) or for
// machines (indented JSON via the global --json flag).
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
)

// Writer is shared by all commands.
type Writer struct {
	w        io.Writer
	JSONMode bool
}

// New creates a Writer writing to w.
func New(w io.Writer, jsonMode bool) *Writer {
	return &Writer{w: w, JSONMode: jsonMode}
}

// JSON pretty-prints v. When JSON mode is on, v is usually the raw API
// response so the output is a faithful, machine-readable copy.
func (w *Writer) JSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	_, err = fmt.Fprintf(w.w, "%s\n", data)
	return err
}

// Table prints a header row plus data rows with aligned columns.
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
