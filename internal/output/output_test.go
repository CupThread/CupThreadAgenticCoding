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

// assertNoControlBytes fails when s contains any C0 control other than tab
// and newline, or DEL — the byte classes that let content act as terminal
// commands (ESC sequences, CR line rewrites, BEL).
func assertNoControlBytes(t *testing.T, s string) {
	t.Helper()
	for i := 0; i < len(s); i++ {
		b := s[i]
		if (b < 0x20 && b != '\t' && b != '\n') || b == 0x7f {
			t.Errorf("output contains control byte 0x%02x: %q", b, s)
			return
		}
	}
}

func TestSanitize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "clean ascii byte-identical", in: "Ada <ada@example.com> (2019)", want: "Ada <ada@example.com> (2019)"},
		{name: "strips ESC and SGR payload boundary", in: "\x1b[31mred\x1b[0m", want: "[31mred[0m"},
		{name: "strips OSC 8 open and close", in: "\x1b]8;;https://evil.example\x1b\\link\x1b]8;;\x1b\\", want: "]8;;https://evil.example\\link]8;;\\"},
		{name: "strips carriage return", in: "line one\rline two", want: "line oneline two"},
		{name: "strips bel", in: "alert\x07", want: "alert"},
		{name: "strips DEL", in: "del\x7fete", want: "delete"},
		{name: "keeps tab", in: "a\tb", want: "a\tb"},
		{name: "keeps newline", in: "a\nb", want: "a\nb"},
		{name: "keeps CJK and emoji", in: "日本語テスト 🎉🚀", want: "日本語テスト 🎉🚀"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitize(tc.in); got != tc.want {
				t.Errorf("sanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitizeStripsEveryForbiddenByte(t *testing.T) {
	for b := 0; b <= 0x1f; b++ {
		if b == '\t' || b == '\n' {
			continue
		}
		in := "x" + string(rune(b)) + "y"
		if got := sanitize(in); got != "xy" {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, "xy")
		}
	}
	if got := sanitize("x\x7fy"); got != "xy" {
		t.Errorf("sanitize(DEL) = %q, want %q", got, "xy")
	}
}

// TestTableSanitizesCells pins the output-boundary defense: a row carrying
// the OSC 8 / SGR / CR injection payload renders with zero control bytes
// while the visible text survives for humans to read.
func TestTableSanitizesCells(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf, FormatTable)
	w.Table([]string{"ID", "Author"}, [][]string{
		{"cmt_1", "\x1b]8;;https://evil.example/verify\x1b\\CupThread Security\x1b]8;;\x1b\\"},
		{"cmt_2", "\x1b[31mACCOUNT COMPROMISED\x1b[0m\r✓ Backup exported to ~/backup.tar.gz\x07"},
	})
	out := buf.String()
	assertNoControlBytes(t, out)
	for _, want := range []string{"CupThread Security", "ACCOUNT COMPROMISED", "Backup exported to ~/backup.tar.gz"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing visible text %q:\n%s", want, out)
		}
	}
}

// TestTableCleanInputByteIdentical guards every existing golden output:
// cells without control characters must render exactly as before.
func TestTableCleanInputByteIdentical(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf, FormatTable)
	w.Table([]string{"ID", "Name"}, [][]string{
		{"cmt_1", "Ada Lovelace"},
		{"cmt_2", "日本語コメント 🎉"},
	})
	want := "ID     Name\ncmt_1  Ada Lovelace\ncmt_2  日本語コメント 🎉\n"
	if got := buf.String(); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// TestStructuredKeepsControlCharsEscaped keeps JSON/YAML byte-faithful:
// encoding/json escapes ESC as \u001b, so structured output must pass the
// payload through unmodified (as escaped text), not sanitized.
func TestStructuredKeepsControlCharsEscaped(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf, FormatJSON)
	if err := w.Structured(map[string]any{"authorName": "\x1b]8;;https://evil.example\x1b\\x"}); err != nil {
		t.Fatalf("Structured: %v", err)
	}
	out := buf.String()
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("JSON output contains a raw ESC byte: %q", out)
	}
	if !strings.Contains(out, `\u001b]8;;https://evil.example`) {
		t.Errorf("JSON output lost the escaped payload: %q", out)
	}
}
