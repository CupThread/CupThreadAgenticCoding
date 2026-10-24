package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Tests for the shared flag-input regular-file discipline (issue #269): the
// file-backed forms of readInputFile (plain path and @path) verify the opened
// descriptor's kind before the first read, so a non-regular source fails with
// NotRegularFileError instead of a raw read error — or, for a FIFO, instead of
// parking the command in open(2) — and ordinary regular files behave exactly
// as before. The unix-only FIFO cases live in fifo_input_unix_test.go; a
// directory is the portable non-regular stand-in here, exercising the same
// fstat branch on every platform including Windows.

func TestReadInputFileRejectsNonRegularFile(t *testing.T) {
	dir := t.TempDir()
	for _, arg := range []string{dir, "@" + dir} {
		_, err := readInputFile(arg, 1024)
		var nrf *NotRegularFileError
		if !errors.As(err, &nrf) {
			t.Fatalf("readInputFile(%q) err = %v, want NotRegularFileError", arg, err)
		}
		if nrf.Path != dir {
			t.Errorf("readInputFile(%q): path = %q, want %q", arg, nrf.Path, dir)
		}
	}
}

func TestReadInputFileRegularFilesUnchanged(t *testing.T) {
	dir := t.TempDir()
	const body = `{"a":1}`
	bodyPath := filepath.Join(dir, "body.json")
	if err := os.WriteFile(bodyPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write body file: %v", err)
	}
	emptyPath := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(emptyPath, nil, 0o600); err != nil {
		t.Fatalf("write empty file: %v", err)
	}

	for _, tc := range []struct {
		name string
		arg  string
		want string
	}{
		{"plain path", bodyPath, body},
		{"@path", "@" + bodyPath, body},
		{"empty file", emptyPath, ""},
		{"@path to empty file", "@" + emptyPath, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := readInputFile(tc.arg, maxConsoleBodyBytes)
			if err != nil {
				t.Fatalf("readInputFile(%q): %v", tc.arg, err)
			}
			if string(out) != tc.want {
				t.Errorf("readInputFile(%q) = %q, want %q", tc.arg, out, tc.want)
			}
		})
	}
}
