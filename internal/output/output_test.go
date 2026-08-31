package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestJSONModeEmitsIndentedJSON(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf, true)
	if err := w.JSON(map[string]any{"a": 1}); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "\"a\": 1") {
		t.Errorf("output = %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("output missing trailing newline")
	}
}

func TestTableAlignsColumns(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf, false)
	w.Table([]string{"ID", "Name"}, [][]string{
		{"1", "short"},
		{"22", "much-longer-name"},
	})
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "ID") || !strings.Contains(lines[0], "Name") {
		t.Errorf("header line = %q", lines[0])
	}
	// Column start of "Name" must line up across rows.
	nameCol := strings.Index(lines[0], "Name")
	for i, want := range []string{"short", "much-longer-name"} {
		if !strings.HasPrefix(lines[i+1][nameCol:], want) {
			t.Errorf("row %d column not aligned: %q", i+1, lines[i+1])
		}
	}
}

func TestPrintf(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf, false)
	w.Printf("✓ %s", "done")
	if buf.String() != "✓ done\n" {
		t.Errorf("output = %q", buf.String())
	}
}
