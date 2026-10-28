//go:build unix

package cmd

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// End-to-end regression for issue #315 (unix only): a child cupthread process
// parked on a blocking stdin read must exit on SIGINT/SIGTERM. An in-process
// assertion cannot catch a wiring regression here — the hang only exists once
// the real signal.NotifyContext handlers are installed in a real process —
// so these tests build the binary and drive it as a child.

// buildCupthreadBinary compiles the CLI once per test; the child-process
// signal tests need a real executable, not the in-process command tree.
func buildCupthreadBinary(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not in PATH: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "cupthread")
	build := exec.Command("go", "build", "-o", bin, "./cmd/cupthread")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// heldOpenStdin returns a file the child can adopt as stdin whose FIFO stays
// open but silent, so any read on it parks indefinitely. Opening O_RDWR keeps
// both FIFO ends attached from this one descriptor — no separate writer
// goroutine to keep alive — while the child's read still blocks on data that
// never comes.
func heldOpenStdin(t *testing.T) *os.File {
	t.Helper()
	fifo := filepath.Join(t.TempDir(), "stdin.fifo")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	f, err := os.OpenFile(fifo, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open FIFO O_RDWR: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// childEnv strips the local CUPTHREAD_* overrides so the child resolves
// config, base URL and credentials exactly like a fresh invocation.
func childEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "CUPTHREAD_TOKEN=") ||
			strings.HasPrefix(kv, "CUPTHREAD_BASE_URL=") ||
			strings.HasPrefix(kv, "CUPTHREAD_CONFIG=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// signalChild runs bin with the held-open stdin, delivers sig once the child
// has had time to park inside its read, and asserts the process exits
// promptly and non-zero with the context-cancellation error — never the
// pre-fix behavior of surviving every signal until SIGKILL.
func signalChild(t *testing.T, bin string, sig syscall.Signal, args ...string) {
	t.Helper()
	child := exec.Command(bin, args...)
	child.Env = childEnv()
	child.Stdin = heldOpenStdin(t)
	child.Dir = "../.."
	var stderr bytes.Buffer
	child.Stderr = &stderr
	if err := child.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	// Give the child time to install its NotifyContext handlers and park in
	// the read: a signal landing before the handlers exist would kill via the
	// default disposition and make this pass for the wrong reason. The
	// context-canceled stderr check below catches that race if it ever fires.
	time.Sleep(500 * time.Millisecond)
	if err := syscall.Kill(child.Process.Pid, sig); err != nil {
		t.Fatalf("send %v: %v", sig, err)
	}
	waited := make(chan error, 1)
	go func() { waited <- child.Wait() }()
	select {
	case err := <-waited:
		if err == nil {
			t.Fatal("child exited 0; the interrupted read must fail the command")
		}
		if !strings.Contains(stderr.String(), "context canceled") {
			t.Errorf("stderr = %q, want the context-cancellation error — anything else means the signal landed before the handlers were installed", stderr.String())
		}
	case <-time.After(10 * time.Second):
		_ = child.Process.Kill()
		<-waited
		t.Fatalf("child survived %v while parked on stdin — issue #315 is back (stderr so far: %q)", sig, stderr.String())
	}
}

// TestAuthLoginStdinTokenDiesOnSignals is the documented flow the issue
// opens with: `auth login --token -` typed interactively parks in the stdin
// read before any HTTP, and Ctrl-C/SIGTERM must still end it.
func TestAuthLoginStdinTokenDiesOnSignals(t *testing.T) {
	bin := buildCupthreadBinary(t)
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			signalChild(t, bin, sig, "auth", "login", "--token", "-",
				"--config", filepath.Join(t.TempDir(), "config.json"))
		})
	}
}

// TestAPIRequestBodyStdinDiesOnSignal covers a second wiring site: the raw
// `api request --input -` body read parks before any request is built and
// must abort the same way.
func TestAPIRequestBodyStdinDiesOnSignal(t *testing.T) {
	bin := buildCupthreadBinary(t)
	signalChild(t, bin, syscall.SIGINT, "api", "request", "GET", "/api/v1/console/me",
		"--input", "-", "--config", filepath.Join(t.TempDir(), "config.json"))
}

// TestSignalChildExitCode pins that the interrupted command exits non-zero
// through the ordinary error path (main's os.Exit(1)), which is what makes
// scripts and timeout(1) wrappers see the abort (issue #315, scenario 3).
func TestSignalChildExitCode(t *testing.T) {
	bin := buildCupthreadBinary(t)
	child := exec.Command(bin, "auth", "login", "--token", "-",
		"--config", filepath.Join(t.TempDir(), "config.json"))
	child.Env = childEnv()
	child.Stdin = heldOpenStdin(t)
	child.Dir = "../.."
	if err := child.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := syscall.Kill(child.Process.Pid, syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- child.Wait() }()
	select {
	case err := <-waited:
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("wait err = %v, want an ExitError with a non-zero code", err)
		}
		if code := exitErr.ExitCode(); code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
	case <-time.After(10 * time.Second):
		_ = child.Process.Kill()
		<-waited
		t.Fatal("child survived SIGTERM while parked on stdin — issue #315 is back")
	}
}
