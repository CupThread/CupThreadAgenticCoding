package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/CupThread/CupThreadAgenticCoding/internal/output"
	"github.com/spf13/cobra"
)

// nonBlankArg rejects a blank positional argument (empty or whitespace-only,
// per strings.TrimSpace). cobra.ExactArgs alone passes the empty string
// through, and every single-ID command interpolates the value straight into
// the request path, so the canonical unset-shell-variable accident used to
// reach the wire as a collection-route DELETE (or a // path) — aimed even by
// confirm-guarded commands after --yes — and the ✓ success lines embedded the
// blank id (issue #293, the workspace-wide extension of the #289 features
// guard). Arity stays the command's own validator; compose with
// cobra.MatchAll, e.g. cobra.MatchAll(cobra.ExactArgs(2), nonBlankArg(0, …)).
// The error fires before RunE, so a destructive command refuses before its
// confirmDestructive prompt and no HTTP request is built at all.
func nonBlankArg(index int, what string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if index >= len(args) || strings.TrimSpace(args[index]) != "" {
			return nil
		}
		return fmt.Errorf("%s is empty — pass a non-blank %s (see '%s --help')", what, what, cmd.CommandPath())
	}
}

// exactNonBlankArg is the Args: drop-in for single-ID commands: exactly one
// positional argument, which must not be blank.
func exactNonBlankArg(what string) cobra.PositionalArgs {
	return cobra.MatchAll(cobra.ExactArgs(1), nonBlankArg(0, what))
}

// pathID percent-encodes a user-supplied resource reference for interpolation
// as a single request-path segment. The API client hands the concatenated
// path to http.NewRequest, which parses it as a full URL, so a raw id pasted
// from untrusted content silently reshaped the request: '#' dropped the
// whole /suffix route (an authenticated mutation fired against the truncated
// prefix), '?' became a query string, and '/'/'../' added or traversed path
// segments — all with the bearer token attached and no local error (issue
// #175). Escaped, the route simply fails to match and the server's 404 is
// the command's error — the honest outcome. The same encoding the app-key
// sites already use (apps.go url.PathEscape).
func pathID(ref string) string {
	return url.PathEscape(ref)
}

// Client-side request-body caps, mirroring the server's SEC-36 intake limits
// (see skills/cupthread-api/SKILL.md). Oversized local input must fail here —
// deterministically and before any HTTP request — instead of buffering a huge
// file or pipe all the way to the server's 413 (issue #142).
const (
	// maxConsoleBodyBytes caps bodies sent to console JSON surfaces: apps
	// settings, imports options, changelog bodies, and `api request` on
	// routes outside the public JSON intake class.
	maxConsoleBodyBytes int64 = 1 << 20 // 1 MB
	// maxPublicBodyBytes caps bodies sent to the server's public JSON intake
	// class (SEC-36 PUBLIC_JSON_BODY_LIMIT_BYTES): the /api/v1/public/...
	// family — e.g. the signed payment-attribute user update — plus the
	// propose/comment/vote/feedback, me/link, me/erase, and uploads/sessions
	// routes outside it; bodyLimitForPath holds the exact split.
	maxPublicBodyBytes int64 = 256 << 10 // 256 KB
	// maxSecretBytes caps piped credentials (`--secret -`, `--token -`):
	// tokens are short, and the bound keeps a runaway pipe from hanging the
	// command forever.
	maxSecretBytes int64 = 64 << 10 // 64 KB
)

// InputTooLargeError reports local input that crossed the command's
// request-body cap. Structured mode renders it as the command's single stdout
// document (code "input_too_large") while the error itself still fails the
// command, so the human detail lands on stderr and the exit code stays
// non-zero.
type InputTooLargeError struct {
	Limit int64 // configured cap, in bytes
}

func (e *InputTooLargeError) Error() string {
	return fmt.Sprintf("input exceeds the %s request-body limit", humanBytes(e.Limit))
}

// humanBytes renders a byte cap the way the SEC-36 limits are documented.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%d MB", n/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// readInputFile reads flag input where "@" or "-" means stdin, a leading "@"
// followed by more characters means the file named by the rest (the curl-style
// @path spelling the help texts, the apps settings set error and SKILL.md
// already teach — issue #188), and anything else a filesystem path, buffering
// at most max bytes. The file forms go through the same regular-file
// discipline as --icon (issues #192 and #269): the open is O_NONBLOCK so a
// writer-less FIFO cannot park open(2) past SIGTERM, and the descriptor's own
// kind is verified before the first read, so a FIFO, character device, or
// other non-regular source fails fast with NotRegularFileError instead of
// hanging. Stdin goes through interruptibleReader: once Execute installs the
// NotifyContext handlers the default SIGINT/SIGTERM disposition is gone for
// the whole process, so a plain blocking stdin read can never be interrupted
// — only the command's context observes the signal (issue #315). Every form
// reads exactly one byte past the cap to detect overflow, so an unbounded
// source is cut off instead of being read into memory to exhaustion; the
// error is returned before any request is built or sent. A file whose literal
// name starts with "@" is referenced via the plain-path branch, e.g. ./@name.
func readInputFile(ctx context.Context, path string, max int64) ([]byte, error) {
	if path == "@" || path == "-" {
		return readBounded(interruptibleReader{ctx: ctx, r: os.Stdin}, max)
	}
	return readRegularFile(strings.TrimPrefix(path, "@"), max)
}

// interruptibleReader makes a blocking reader — always os.Stdin in
// production, or the confirm prompt's stdin — observe the command's context:
// Execute runs every command under signal.NotifyContext, which replaces the
// default SIGINT/SIGTERM disposition process-wide, so the raw read itself is
// never interrupted and would stay parked until EOF or SIGKILL (issue #315).
// Each Read parks in a goroutine and selects on ctx.Done(), returning the
// context error on cancellation. The parked goroutine reads into its own
// buffer, never the caller's: after a cancellation wins the select, a
// late-completing read cannot race a caller that has already moved on, and
// the buffered result channel lets the goroutine exit instead of leaking.
// The goroutine itself only unblocks on data, EOF, or process exit — fine
// for a CLI, which is exiting by the time this matters.
type interruptibleReader struct {
	ctx context.Context
	r   io.Reader
}

func (ir interruptibleReader) Read(p []byte) (int, error) {
	// A nil ctx (a hand-built cobra command never run through Execute)
	// degrades to the pre-#315 behavior instead of panicking: there is no
	// context to observe, exactly like the plain read this wrapper replaced.
	ctx := ir.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	type readResult struct {
		n   int
		buf []byte
		err error
	}
	ch := make(chan readResult, 1)
	go func() {
		buf := make([]byte, len(p))
		n, err := ir.r.Read(buf)
		ch <- readResult{n: n, buf: buf[:n], err: err}
	}()
	select {
	case res := <-ch:
		copy(p, res.buf)
		return res.n, res.err
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// readBounded is readInputFile's core, split out so tests can drive it with
// an arbitrary reader and prove the source is abandoned at the cap.
func readBounded(r io.Reader, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, &InputTooLargeError{Limit: max}
	}
	return data, nil
}

// NotRegularFileError reports a request-body path that opened to something
// other than a regular file — a FIFO, character device, socket, or similar.
// Such a source is unbounded or not yet written, so reading it could hang the
// command forever (a writer-less FIFO) or buffer until memory is exhausted
// (/dev/zero); it is rejected before the first read instead (issues #192 and
// #269).
type NotRegularFileError struct {
	Path string
	Mode os.FileMode
}

func (e *NotRegularFileError) Error() string {
	return fmt.Sprintf("%q: not a regular file", e.Path)
}

// readRegularFile is the file-backed half of the bounded-input discipline:
// open path once, verify the descriptor itself is a regular file, then read
// at most max bytes through readBounded. Checking the opened descriptor
// (fstat, not Stat-before-open) closes the check-then-read gap: a file swapped
// for a FIFO or grown past the cap after an earlier advisory Stat still fails
// here, before any HTTP request (issue #192). The open goes through
// openInputFile so a FIFO cannot even park the open(2) call.
func readRegularFile(path string, max int64) ([]byte, error) {
	f, err := openInputFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, &NotRegularFileError{Path: path, Mode: fi.Mode()}
	}
	return readBounded(f, max)
}

// flagInputError names the flag behind a readInputFile failure the way the
// --icon reader does: NotRegularFileError alone says which path was rejected,
// and "invalid --input %q: not a regular file" says which flag to change
// (issue #269). Every other failure already identifies itself — a missing
// path names itself and InputTooLargeError renders the structured
// input_too_large document — so they pass through unchanged.
func flagInputError(flag string, err error) error {
	var nrf *NotRegularFileError
	if errors.As(err, &nrf) {
		return fmt.Errorf("invalid --%s %w", flag, nrf)
	}
	return err
}

// reportInputTooLarge renders an InputTooLargeError as the command's single
// structured stdout document in --json/-o yaml mode. It returns the rendering
// error only: callers keep returning the original error so the process still
// exits non-zero with the human detail on stderr, matching how api request
// surfaces APIError payloads (issue #21).
func (a *app) reportInputTooLarge(err error) error {
	var e *InputTooLargeError
	if !errors.As(err, &e) || !a.structured() {
		return nil
	}
	return a.out.Structured(map[string]any{
		"error": e.Error(),
		"code":  "input_too_large",
		"limit": e.Limit,
	})
}

// mutationResult is the minimal machine-readable record of a completed
// mutation, emitted on stdout in --json/-o yaml mode by every mutating
// command the server answers without a body (issue #194). id carries the
// RESOLVED resource ID — for prefix-taking commands that is the full ID the
// lookup picked, which would otherwise be disclosed only by the human echo —
// and is omitted for whole-context actions (notifications read-all,
// integration disconnects name their provider instead).
type mutationResult struct {
	Action  string `json:"action"`
	ID      string `json:"id,omitempty"`
	Success bool   `json:"success"`
}

// emitMutationResult prints the mutation record in structured mode; human
// mode callers keep their existing ✓ echo, so table output stays
// byte-identical.
func (a *app) emitMutationResult(action, id string) error {
	return a.out.Structured(mutationResult{Action: action, ID: id, Success: true})
}

// warnf reports a non-fatal warning. In structured mode it goes to stderr so
// stdout stays a single machine-parseable document. Both branches sanitize:
// warnings interpolate server-derived text (revoke failures quote the
// server's error, the issuer URL and the stored token prefix), so a hostile
// value can neither render escape sequences nor forge extra lines — the
// human line through PrintfSafe, the structured line through the same
// StripTerminalControls predicate (issue #286: the structured branch used to
// echo the raw value, so --json/-o yaml disagreed with human mode on
// identical data). Structured stdout documents stay byte-faithful; only
// stderr, which is human-facing in every mode, is touched.
func (a *app) warnf(format string, args ...any) {
	if a.structured() {
		fmt.Fprintln(os.Stderr, output.StripTerminalControls(fmt.Sprintf(format, args...)))
		return
	}
	a.out.PrintfSafe(format, args...)
}

// clampListLimit normalizes a console listing's --limit the way the server's
// parseListPagination silently does: below 1 becomes 1 (the server would
// degrade 0 to the route default, a different page size than requested) and
// above the route's maxLimit becomes the cap. Every console listing shares
// this helper so an offset-stepping walk advances by the page size the
// server actually serves instead of silently skipping rows (issues #201 and
// #337: features list was the last one clamping inline and silently). The
// rewrite is announced through warnf, so structured stdout stays a single
// document.
func (a *app) clampListLimit(requested, maxLimit int) int {
	effective := requested
	switch {
	case requested < 1:
		effective = 1
	case requested > maxLimit:
		effective = maxLimit
	}
	if effective != requested {
		a.warnf("⚠ --limit %d is outside the server's 1-%d page range; requesting %d instead", requested, maxLimit, effective)
	}
	return effective
}

// consoleListMaxOffset mirrors the server's shared CONSOLE_LIST_MAX_OFFSET
// (SaaS apps/api/src/lib/pagination.ts): every offset-paginated console and
// platform listing clamps ?offset= at 10,000 and still answers 200.
const consoleListMaxOffset = 10000

// clampListOffset normalizes a console listing's --offset the way the
// server's parseListPagination silently does: below 0 becomes 0 and above
// the shared 10,000 console cap becomes the cap (which still answers 200).
// Past the cap the server keeps serving the offset-10,000 page, so without
// the local rewrite a walk re-reads the same final page forever with no
// hint that anything changed (issue #178). The rewrite is announced through
// warnf, so structured stdout stays a single document.
func (a *app) clampListOffset(requested int) int {
	effective := requested
	switch {
	case requested < 0:
		effective = 0
	case requested > consoleListMaxOffset:
		effective = consoleListMaxOffset
	}
	if effective != requested {
		a.warnf("⚠ --offset %d is outside the server's 0-10000 page range; requesting %d instead", requested, effective)
	}
	return effective
}

// idRefNouns carries one command family's nouns for the shared ID-resolution
// errors, so every family reads the same way while naming its own resource.
type idRefNouns struct {
	what     string // singular resource name, e.g. "changelog entry"
	plural   string // plural resource name, e.g. "entries"
	listHint string // the listing command whose table printed the IDs, e.g. "cupthread changelog list"
}

// resolveIDRef resolves an ID reference copied out of a family's table — the
// exact ID or a unique prefix of one — against that family's listing, the
// fetchOneFeatureRequest contract generalized (issue #413): the console
// tables print 12-character IDs, so every sibling command that consumes one
// of those IDs must accept the truncated form instead of forwarding a
// guaranteed 404 (or a misleading 400) to the server.
//
// fetchPage is called with the 0-based page index and returns that page's
// items plus whether the listing ends there; the pagination mechanics stay
// with each family (offset steps, a cursor walk, or a single fixed window),
// which keeps the helper independent of how the server pages. The walk stops
// at maxPages so a server reporting endless pages cannot hang the CLI. An
// exact ID match returns immediately; prefix matches accumulate across every
// page before the ambiguity decision, so a prefix that looks unique on the
// first page still errors once a later page adds a second match. One unique
// match resolves; several produce the shared ambiguous-prefix error; none
// produce notFound, which each family tailors (scope wording, --app hints)
// from the reference, the scanned count, the exhaustion flag, and the
// prefix-match count (nonzero only in the refused under-cap case).
//
// acceptPrefixUnderCap marks whether a maxPages-stopped scan is still the
// whole reachable universe, so a prefix unique within it is genuinely
// unique. The offset-capped listings are: the server clamps offset at the
// same window resolution pages to, so rows past the cap cannot be served —
// and are passed true. A cursor-walked family with no server cap passes
// false: there maxPages is a pure hang-guard, a unique match within the
// scanned pages could gain a second match on an unscanned page, and the
// prefix is refused so a mutation cannot act on an unverifiable choice
// (PR #414 review).
//
// An empty (or whitespace-only) reference is rejected before any lookup:
// strings.HasPrefix(id, "") is true for every id, so with a single item in
// scope an empty ref — the canonical unset-shell-variable accident — would
// silently resolve to it and the mutation commands would act on a resource
// the caller never named (issue #289).
func resolveIDRef[T any](ref string, nouns idRefNouns, maxPages int, acceptPrefixUnderCap bool, fetchPage func(page int) (items []T, done bool, err error), notFound func(ref string, scanned int, exhausted bool, prefixMatches int) error, idOf func(*T) string) (*T, error) {
	if strings.TrimSpace(ref) == "" {
		return nil, fmt.Errorf("%s reference is empty — pass an id or non-empty id prefix (see '%s')", nouns.what, nouns.listHint)
	}
	var matches []T
	scanned := 0
	exhausted := false
	for page := 0; page < maxPages && !exhausted; page++ {
		items, done, err := fetchPage(page)
		if err != nil {
			return nil, err
		}
		for i := range items {
			id := idOf(&items[i])
			if id == ref {
				return &items[i], nil
			}
			if strings.HasPrefix(id, ref) {
				matches = append(matches, items[i])
			}
		}
		scanned += len(items)
		exhausted = done
	}
	if len(matches) == 1 && (exhausted || acceptPrefixUnderCap) {
		return &matches[0], nil
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("ambiguous %s prefix %q matches %d %s; use a longer prefix", nouns.what, ref, len(matches), nouns.plural)
	}
	return nil, notFound(ref, scanned, exhausted, len(matches))
}

// decodeStrictRawJSON validates that data holds exactly one JSON value and
// returns it verbatim as json.RawMessage (only trailing whitespace is
// allowed). Decoding into any instead would rebuild every number through
// float64, silently rewriting integers a float64 cannot represent exactly
// (issue #75); the RawMessage keeps the caller's bytes untouched. The strict
// trailing-data rejection follows issue #87: a single Decode call alone
// silently discarded everything past the first value, so a malformed --input
// file was sent truncated.
func decodeStrictRawJSON(data []byte) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse input JSON: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("parse input JSON: unexpected trailing data — exactly one JSON value expected")
	}
	return raw, nil
}
