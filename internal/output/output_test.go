package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONOutput(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf, FormatJSON)
	if err := w.Structured(map[string]any{"a": 1}); err != nil {
		t.Fatalf("Structured: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "\"a\": 1") {
		t.Errorf("output = %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("output missing trailing newline")
	}
}

func TestYAMLOutput(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf, FormatYAML)
	if err := w.Structured(map[string]any{"a": 1, "b": "x"}); err != nil {
		t.Fatalf("Structured: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "a: 1") || !strings.Contains(got, `b: x`) {
		t.Errorf("output = %q", got)
	}
}

func TestYAMLNormalizesRawMessage(t *testing.T) {
	// Fields typed as json.RawMessage must render as structures in YAML,
	// not as base64-encoded bytes.
	payload := struct {
		Name string          `json:"name"`
		SDK  json.RawMessage `json:"sdk"`
	}{Name: "app", SDK: json.RawMessage(`{"theme":"dark","enabled":true}`)}

	var buf bytes.Buffer
	w := New(&buf, FormatYAML)
	if err := w.Structured(payload); err != nil {
		t.Fatalf("Structured: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "theme: dark") || !strings.Contains(got, "enabled: true") {
		t.Errorf("raw JSON not normalized to YAML structures: %q", got)
	}
	if strings.Contains(got, "eyK") || strings.Contains(got, "!!binary") {
		t.Errorf("YAML output contains base64/binary payload: %q", got)
	}
}

func TestYAMLKeepsIntegerForm(t *testing.T) {
	payload := struct {
		Bytes int `json:"bytes"`
	}{Bytes: 20000000}
	var buf bytes.Buffer
	w := New(&buf, FormatYAML)
	if err := w.Structured(payload); err != nil {
		t.Fatalf("Structured: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "bytes: 20000000") {
		t.Errorf("integer rendered in exponent form: %q", got)
	}
}

func TestParseFormat(t *testing.T) {
	for _, valid := range []string{"table", "json", "yaml"} {
		if _, err := ParseFormat(valid); err != nil {
			t.Errorf("ParseFormat(%q) = %v, want nil", valid, err)
		}
	}
	if _, err := ParseFormat("xml"); err == nil {
		t.Error("ParseFormat(xml) = nil, want error")
	}
}

func TestTableAlignsColumns(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf, FormatTable)
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
	w := New(&buf, FormatTable)
	w.Printf("✓ %s", "done")
	if buf.String() != "✓ done\n" {
		t.Errorf("output = %q", buf.String())
	}
}
