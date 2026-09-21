package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// TestVersionFlagReflectsLinkedVersion exercises the injection path the
// release workflow relies on: the linker's -X flag assigns the package-level
// Version var before main runs, and `cupthread --version` must print
// whatever it holds. A const (the pre-#60 state) could not carry a
// tag-injected value, so this failing is the canary for accidentally
// freezing the version again.
func TestVersionFlagReflectsLinkedVersion(t *testing.T) {
	orig := Version
	defer func() { Version = orig }()
	Version = "9.9.9-link-test"

	var buf bytes.Buffer
	root := newRootCmd()
	root.SetOut(&buf)
	root.SetArgs([]string{"--version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--version returned an error: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "cupthread version 9.9.9-link-test") {
		t.Fatalf("--version output %q does not report the injected version", got)
	}
}

// TestVersionDefaultIsHonest keeps the source-build default from claiming a
// concrete release version: two builds of different commits once both
// reported "0.2.0" because the version was a hand-pinned const. Only
// link-time injection (see the release workflow) may name a real version.
func TestVersionDefaultIsHonest(t *testing.T) {
	if Version != "dev" {
		t.Fatalf("default Version = %q, want the honest placeholder %q", Version, "dev")
	}
	root := newRootCmd()
	if root.Version != Version {
		t.Fatalf("root command Version = %q, want the package var %q", root.Version, Version)
	}
}
