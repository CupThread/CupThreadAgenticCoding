package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// Indirections over the confirmation I/O so tests can drive the interactive
// prompt hermetically; nil means the real stdin/stderr.
var (
	confirmIn    io.Reader
	confirmOut   io.Writer
	confirmIsTTY func() bool
)

// confirmDestructive guards an irreversible command. With --yes it is a
// no-op. On an interactive stdin it prompts once on stderr and aborts unless
// the answer is y/yes. On a non-interactive stdin without --yes it refuses
// before any resolution or HTTP work, so scripts and agents fail fast
// instead of hanging on a prompt or destroying data by accident.
//
// action is a full phrase describing the effect, e.g.
// `permanently delete feature request "fr_1"`; it is shown verbatim in the
// prompt, so pass the raw user-supplied id — it must read truthfully even
// though no resolution request has been sent yet.
func confirmDestructive(cmd *cobra.Command, action string) error {
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		return nil
	}
	path := strings.TrimPrefix(cmd.CommandPath(), "cupthread ")
	if !stdinIsTerminal() {
		return fmt.Errorf("%s is destructive and cannot be undone; stdin is not interactive, re-run with --yes to confirm", path)
	}
	w := confirmOut
	if w == nil {
		w = os.Stderr
	}
	r := confirmIn
	if r == nil {
		r = os.Stdin
	}
	fmt.Fprintf(w, "About to %s. This cannot be undone. Continue? [yN] ", action)
	answer, err := readConfirmLine(cmd.Context(), r)
	if err != nil {
		return fmt.Errorf("aborted: interrupted — %s was not confirmed, nothing was changed", path)
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer == "y" || answer == "yes" {
		return nil
	}
	return fmt.Errorf("aborted: %s was not confirmed, nothing was changed", path)
}

// stdinIsTerminal reports whether the process stdin is an interactive
// terminal.
func stdinIsTerminal() bool {
	if confirmIsTTY != nil {
		return confirmIsTTY()
	}
	return stdinIsInteractive(os.Stdin.Fd())
}

// readConfirmLine reads one answer line without buffering past the newline,
// so input the caller typed ahead is left unread for the rest of the process.
// The read goes through interruptibleReader: behind Execute's NotifyContext
// handlers a raw byte-at-a-time stdin read can never be interrupted, so
// Ctrl-C at the prompt would park forever (issue #315). A context
// cancellation returns the context error — surfaced as the aborted-without-
// mutation error — while EOF still yields whatever was typed, keeping the
// Ctrl-D escape working.
func readConfirmLine(ctx context.Context, r io.Reader) (string, error) {
	ir := interruptibleReader{ctx: ctx, r: r}
	var sb strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := ir.Read(buf)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", err
		}
		if n == 0 || err != nil || buf[0] == '\n' {
			return sb.String(), nil
		}
		sb.WriteByte(buf[0])
	}
}
