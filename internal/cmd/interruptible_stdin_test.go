package cmd

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// Tests for the ctx-aware blocking reads (issue #315): once Execute installs
// its NotifyContext handlers, SIGINT/SIGTERM no longer terminate the process
// out of a raw blocking read — only the command's context observes the
// signal — so every blocking stdin path must return promptly on cancellation
// instead of parking until EOF or SIGKILL. The end-to-end signal regression
// against a real child process lives in stdin_signal_unix_test.go.

// failReader blows the test up if it is ever read, proving the
// already-cancelled fast path never touches the source.
type failReader struct{}

func (failReader) Read(p []byte) (int, error) {
	return 0, errors.New("a cancelled context must not reach the underlying reader")
}

// TestInterruptibleReaderAlreadyCanceled pins the fast path: a read on a
// cancelled context fails immediately with context.Canceled and never
// touches the source.
func TestInterruptibleReaderAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ir := interruptibleReader{ctx: ctx, r: failReader{}}
	n, err := ir.Read(make([]byte, 4))
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// TestInterruptibleReaderDeliversReads pins the pass-through path: with a
// live context, data from the source reaches the caller byte-identical, so
// the wrapper changes nothing about ordinary non-interrupted reads.
func TestInterruptibleReaderDeliversReads(t *testing.T) {
	ir := interruptibleReader{ctx: context.Background(), r: strings.NewReader("cpt_token\n")}
	buf := make([]byte, 16)
	n, err := ir.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := string(buf[:n]); got != "cpt_token\n" {
		t.Errorf("Read = %q, want %q", got, "cpt_token\n")
	}
}

// TestInterruptibleReaderReturnsOnCancelMidRead parks the wrapper on a
// blocking pipe, cancels the context, and requires the Read to come back
// with context.Canceled within the deadline — the exact mechanism whose
// absence made every stdin read immune to Ctrl-C (issue #315).
func TestInterruptibleReaderReturnsOnCancelMidRead(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	ir := interruptibleReader{ctx: ctx, r: pr}
	type readOutcome struct {
		n   int
		err error
	}
	done := make(chan readOutcome, 1)
	go func() {
		n, err := ir.Read(make([]byte, 8))
		done <- readOutcome{n, err}
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case res := <-done:
		if !errors.Is(res.err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", res.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read stayed parked past context cancellation — the issue #315 hang is back")
	}
}

// TestReadConfirmLineAbortsOnContextCancel drives the confirm prompt's
// byte-at-a-time loop the same way: cancellation must end the read instead
// of leaving it parked on the interactive stdin forever.
func TestReadConfirmLineAbortsOnContextCancel(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	type lineOutcome struct {
		line string
		err  error
	}
	done := make(chan lineOutcome, 1)
	go func() {
		line, err := readConfirmLine(ctx, pr)
		done <- lineOutcome{line, err}
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case res := <-done:
		if !errors.Is(res.err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", res.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readConfirmLine stayed parked past context cancellation — Ctrl-C at the prompt is dead again")
	}
}

// TestConfirmDestructiveAbortedOnInterrupt pins the error mapping: a
// cancelled context at the prompt becomes the aborted-without-mutation
// error — the clean non-zero-exit path the issue asks for, in place of a
// raw signal death.
func TestConfirmDestructiveAbortedOnInterrupt(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })
	withConfirmHooks(t, pr, true)

	c := &cobra.Command{Use: "changelog delete <entry-id>"}
	c.Flags().BoolP("yes", "y", false, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.SetContext(ctx)

	err := confirmDestructive(c, `permanently delete changelog entry "cl_1"`)
	if err == nil {
		t.Fatal("want an abort error on a cancelled context, got nil")
	}
	if !strings.Contains(err.Error(), "aborted: interrupted") {
		t.Errorf("err = %v, want the aborted-interrupted wording", err)
	}
	if !strings.Contains(err.Error(), "nothing was changed") {
		t.Errorf("err = %v, want the nothing-was-changed guarantee", err)
	}
}
