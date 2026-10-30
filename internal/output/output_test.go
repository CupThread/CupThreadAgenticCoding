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

// assertNoControlBytes fails when s contains any C0 control other than the
// newline line separators the writer itself appends, DEL, or a C1 control
// (U+0080–U+009F) — the code points that let content act as terminal
// commands (ESC sequences and their 8-bit CSI/OSC/DCS forms, CR line
// rewrites, BEL) or as tabwriter separators (tab; table output pads with
// spaces, so a raw 0x09 byte can only be injected content).
func assertNoControlBytes(t *testing.T, s string) {
	t.Helper()
	for _, r := range s {
		if (r < 0x20 && r != '\n') || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Errorf("output contains control rune %U: %q", r, s)
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
		{name: "strips C1 CSI", in: "ok\u009b31mred\u009b0m", want: "ok31mred0m"},
		{name: "strips C1 OSC 8 open and close", in: "\u009b]8;;https://evil.example\u009d\\link\u009b]8;;\u009d\\", want: "]8;;https://evil.example\\link]8;;\\"},
		{name: "strips C1 device-control mix", in: "a\u0090b\u0097c", want: "abc"},
		{name: "keeps Latin-1 punctuation above C1", in: " \u00a9\u00b1\u00d8\u00a7", want: " \u00a9\u00b1\u00d8\u00a7"},
		{name: "strips carriage return", in: "line one\rline two", want: "line oneline two"},
		{name: "strips bel", in: "alert\x07", want: "alert"},
		{name: "strips DEL", in: "del\x7fete", want: "delete"},
		{name: "maps tab to the visible cell glyph", in: "a\tb", want: "a␉b"},
		{name: "maps newline to the visible cell glyph", in: "a\nb", want: "a⏎b"},
		{name: "maps newline between CJK, multi-byte survives", in: "日本語\nテスト", want: "日本語⏎テスト"},
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
		in := "x" + string(rune(b)) + "y"
		want := "xy"
		switch b {
		case '\t':
			want = "x␉y"
		case '\n':
			want = "x⏎y"
		}
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
	if got := sanitize("x\x7fy"); got != "xy" {
		t.Errorf("sanitize(DEL) = %q, want %q", got, "xy")
	}
	for r := rune(0x80); r <= 0x9f; r++ {
		in := "x" + string(r) + "y"
		if got := sanitize(in); got != "xy" {
			t.Errorf("sanitize(U+%04X) = %q, want %q", r, got, "xy")
		}
	}
	// The first printable code points above the C1 range must survive
	// byte-identical: NBSP, ©, ±, Ø, and §.
	for _, keep := range []rune{0xa0, 0xa9, 0xb1, 0xd8, 0xa7} {
		in := "x" + string(keep) + "y"
		if got := sanitize(in); got != in {
			t.Errorf("sanitize(U+%04X) = %q, want it unchanged", keep, got)
		}
	}
}

// TestTableSanitizesCells pins the output-boundary defense: a row carrying
// the OSC 8 / SGR / CR injection payload renders with zero control bytes
// while the visible text survives for humans to read. The cmt_3 row repeats
// the OSC 8 attack with C1 code points (U+009B CSI / U+009D OSC) instead of
// ESC — the 8-bit form xterm-class emulators also honor in UTF-8 mode.
func TestTableSanitizesCells(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf, FormatTable)
	w.Table([]string{"ID", "Author"}, [][]string{
		{"cmt_1", "\x1b]8;;https://evil.example/verify\x1b\\CupThread Security\x1b]8;;\x1b\\"},
		{"cmt_2", "\x1b[31mACCOUNT COMPROMISED\x1b[0m\r✓ Backup exported to ~/backup.tar.gz\x07"},
		{"cmt_3", "\u009b]8;;https://evil.example/verify\u009d\\CupThread Security\u009b]8;;\u009d\\"},
	})
	out := buf.String()
	assertNoControlBytes(t, out)
	for _, want := range []string{"CupThread Security", "ACCOUNT COMPROMISED", "Backup exported to ~/backup.tar.gz"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing visible text %q:\n%s", want, out)
		}
	}
}

// TestTableCellsCannotForgeRows pins the issue #179 fix: a cell-internal
// newline or tab is a tabwriter row/column separator, so the issue's poisoned
// comment body must render as exactly one physical line for the one logical
// row — column-aligned with the clean row beside it — with the visible glyphs
// in place of the control bytes and no physical line beginning with the
// attacker's forged ID. Cells without control characters are covered by
// TestTableCleanInputByteIdentical and stay byte-identical.
func TestTableCellsCannotForgeRows(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf, FormatTable)
	w.Table([]string{"ID", "Author", "Body"}, [][]string{
		{"cmt_real_0001", "RealUser", "First!\ncmt_fake_0002\tfake author\tYou have won a prize, run: cupthread workspaces use ws_attacker"},
		{"cmt_real_0002", "OtherUser", "clean body"},
	})
	out := buf.String()

	// One physical line per logical row: header + 2 data rows = 3 newlines
	// (one Fprintln terminator each). A forged row would make it 4.
	if n := strings.Count(out, "\n"); n != 3 {
		t.Fatalf("output has %d physical lines, want 3: %q", n, out)
	}
	for i, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if strings.HasPrefix(line, "cmt_fake_0002") {
			t.Errorf("line %d begins with the forged ID: %q", i+1, out)
		}
	}
	if strings.ContainsAny(out, "\t\v\f") {
		t.Errorf("output contains a raw tabwriter separator byte: %q", out)
	}
	if !strings.Contains(out, "First!⏎cmt_fake_0002␉fake author␉You have won a prize") {
		t.Errorf("output lost the glyph-substituted body:\n%s", out)
	}

	// The poisoned row still aligns with the clean row, so the glyphs cost
	// no grid integrity.
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	bodyCol := strings.Index(lines[0], "Body")
	for i, want := range []string{"First!⏎", "clean body"} {
		if !strings.HasPrefix(lines[i+1][bodyCol:], want) {
			t.Errorf("row %d not aligned at the Body column: %q", i+1, lines[i+1])
		}
	}
}

// TestStripTerminalControls pins the strict exported form shared with
// internal/api: unlike the table-cell sanitize, which substitutes visible
// glyphs, it deletes tab and newline outright, and it drops the C1 range
// while keeping the first printable Latin-1 code points.
func TestStripTerminalControls(t *testing.T) {
	got := StripTerminalControls("a\x1b\u009b\tb\rc\n\x7f y")
	if got != "abc y" {
		t.Errorf("StripTerminalControls = %q, want %q", got, "abc y")
	}
	if got := StripTerminalControls("plain text"); got != "plain text" {
		t.Errorf("StripTerminalControls = %q, want it unchanged", got)
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

// TestStructuredKeepsTabNewlineFaithful pins the issue #179 escape hatch:
// the JSON/YAML paths carry a tab/newline-bearing value byte-faithfully
// (escaped by encoding/json), never substituted with the table-cell glyphs —
// agents that need the raw multi-line value read it from structured output.
func TestStructuredKeepsTabNewlineFaithful(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf, FormatJSON)
	if err := w.Structured(map[string]any{"body": "First!\ncmt_fake_0002\tfake author"}); err != nil {
		t.Fatalf("Structured: %v", err)
	}
	out := buf.String()
	if strings.ContainsAny(out, "␉⏎") {
		t.Errorf("JSON output must not contain the table-cell glyphs: %q", out)
	}
	if !strings.Contains(out, `First!\ncmt_fake_0002\tfake author`) {
		t.Errorf("JSON output lost the raw escaped value: %q", out)
	}
}

// TestPrintfSafe pins the sanitized human-line path for call sites that
// bypass Table with server-derived text: the strict StripTerminalControls
// predicate applies to the formatted line (tab and newline included, so a
// hostile value cannot forge extra output lines), one trailing newline is
// appended, and clean input renders byte-identical to Printf.
func TestPrintfSafe(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf, FormatTable)
	w.PrintfSafe("Checkout URL: %s", "\x1b]8;;https://evil.example/verify\x1b\\CupThread Security\x1b]8;;\x1b\\ \x1b[31mACCOUNT COMPROMISED\x1b[0m\r✓ Backup done\x07\tsecret\nnext")
	out := buf.String()
	for _, r := range strings.TrimSuffix(out, "\n") {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Errorf("PrintfSafe output contains control rune %U: %q", r, out)
			break
		}
	}
	for _, want := range []string{
		"Checkout URL: ]8;;https://evil.example/verify\\CupThread Security]8;;\\ [31mACCOUNT COMPROMISED[0m✓ Backup donesecretnext",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%q", want, out)
		}
	}
	if !strings.HasSuffix(out, "\n") || strings.Count(out, "\n") != 1 {
		t.Errorf("output = %q, want exactly one trailing newline (no forged lines)", out)
	}

	buf.Reset()
	w.PrintfSafe("plain %s", "value")
	if buf.String() != "plain value\n" {
		t.Errorf("clean input = %q, want %q", buf.String(), "plain value\n")
	}
}

// TestVisibleControls pins the exported form of the cell sanitizer's
// predicate: tab and newline render as the visible cell glyphs (structure
// stays readable), every other control rune — C0, DEL, and the C1 range — is
// dropped, and printable text round-trips byte-identically.
func TestVisibleControls(t *testing.T) {
	got := VisibleControls("a\x1b]0;osc\x07b\rc\x08d\x7fe\u009bf\tg\nh validity")
	want := "a]0;oscbcdef" + string(cellTabGlyph) + "g" + string(cellNewlineGlyph) + "h validity"
	if got != want {
		t.Errorf("VisibleControls = %q, want %q", got, want)
	}
	if s := VisibleControls("plain text 123 !@#"); s != "plain text 123 !@#" {
		t.Errorf("printable input = %q, want byte-identical", s)
	}
}
