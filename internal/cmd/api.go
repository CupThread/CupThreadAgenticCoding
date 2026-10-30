package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/spf13/cobra"
)

func newAPICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Low-level API access (for agents and unreleased endpoints)",
	}
	cmd.AddCommand(newAPIRequestCmd())
	cmd.AddCommand(newAPISignUserAttrsCmd())
	return cmd
}

func newAPIRequestCmd() *cobra.Command {
	var inputPath string
	req := &cobra.Command{
		Use:   "request <METHOD> <path>",
		Short: "Perform a raw authenticated API request",
		Long: `Perform a raw authenticated request against the API.

Path must start with "/" and is appended to the base URL, e.g.
  cupthread api request GET /api/v1/console/me

Authentication, the X-Workspace-Id header (when a workspace is resolved) and
JSON output are handled the same as the high-level commands. Pass a JSON body
with --input (a file path, curl-style "@path", or "-" / "@" for stdin); the
body is sent byte-for-byte as given, so JSON numbers keep full precision
(issue #75). This is the escape hatch for endpoints the CLI does not wrap yet.

The body is capped locally at the server's request-body limits (SEC-36):
1 MB on console routes, 256 KB on public JSON intake — the /api/v1/public/
family plus the propose/comment/vote/feedback, me/link, me/erase, and
uploads/sessions routes. Larger input fails in-process with input_too_large
before anything is sent — no partial read of a huge file or pipe ever
reaches the network.

Every invocation sends an X-Request-Id correlation header (cli-<uuid>); the
API echoes it on the response and CLI errors quote it as request-id=… —
include that value in bug reports and support requests.`,
		Args:                  cobra.ExactArgs(2),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			method := upper(args[0])
			path := args[1]
			if len(path) == 0 || path[0] != '/' {
				return errors.New("path must start with '/'")
			}

			var body json.RawMessage
			if inputPath != "" {
				data, err := readInputFile(cmd.Context(), inputPath, bodyLimitForPath(path))
				if err != nil {
					if perr := A.reportInputTooLarge(err); perr != nil {
						return perr
					}
					return flagInputError("input", err)
				}
				body, err = decodeStrictRawJSON(data)
				if err != nil {
					return err
				}
			}

			// One correlation ID per invocation (OPS-01): sending our own
			// valid value means the API echoes it back verbatim, so the ID
			// shown below is always the one the server logged.
			requestID := api.NewRequestID()
			var raw json.RawMessage
			err := A.client.DoWithHeaders(cmd.Context(), method, path, nil,
				map[string]string{"X-Request-Id": requestID}, body, &raw)
			if err != nil {
				// Surface structured API errors as a machine-readable payload
				// on stdout, but still return the error so the process exits
				// non-zero: scripts and agents branch on the exit code, and
				// exiting 0 on a failed request would hide the failure behind
				// a parseable payload. errors.As is required because
				// tier-limit (402) errors are wrapped by the client.
				var apiErr *api.APIError
				if errors.As(err, &apiErr) && A.structured() {
					payload := map[string]any{
						"error":  apiErr.Message,
						"code":   apiErr.Code,
						"status": apiErr.Status,
					}
					if apiErr.RequestID != "" {
						payload["requestId"] = apiErr.RequestID
					}
					// Present only when the transient-failure retry loop ran
					// and still failed, so agents can tell exhausted retries
					// (429/502/503/504 on idempotent requests) from a plain
					// single-shot failure.
					if apiErr.Attempts > 1 {
						payload["attempts"] = apiErr.Attempts
					}
					if hint := apiErr.Hint(); hint != "" {
						payload["hint"] = hint
					}
					// Validation failures carry the server's field-level
					// details (zod flatten); pass them through verbatim so
					// programmatic consumers see exactly what the server sent.
					if len(apiErr.Details) > 0 {
						payload["details"] = json.RawMessage(apiErr.Details)
					}
					if perr := A.out.Structured(payload); perr != nil {
						return perr
					}
				}
				return err
			}

			if len(raw) == 0 {
				// A 204 (or any empty 2xx body) is a success. The structured
				// modes must answer with the single parseable document `null`
				// and exit 0: an empty json.RawMessage cannot be JSON/YAML
				// encoded, and reporting a successful (possibly destructive)
				// mutation as an encoder failure would invite a retry.
				if A.structured() {
					return A.out.Structured(json.RawMessage("null"))
				}
				A.out.PrintfSafe("✓ %s %s succeeded (no response body, request-id %s)", method, path, requestID)
				return nil
			}
			if A.structured() {
				return A.out.Structured(raw)
			}
			A.out.PrintfSafe("✓ %s %s (request-id %s)", method, path, requestID)
			// The body is server-derived: display it through the sanitized
			// human-line path so raw control bytes cannot reach the terminal
			// (--json/-o yaml above stay byte-faithful).
			A.out.PrintfSafe("%s", raw)
			return nil
		},
	}
	req.Flags().StringVar(&inputPath, "input", "", "JSON request body: file path, \"@path\", or \"-\"/\"@\" for stdin; max 1 MB, 256 KB on public-intake routes")
	return req
}

// bodyLimitForPath picks the client-side request-body cap for a raw
// `api request` path, mirroring the server's SEC-36 classification: 256 KB
// on the public JSON intake class, 1 MB everywhere else (console JSON).
func bodyLimitForPath(path string) int64 {
	if publicIntakeBodyLimit(path) {
		return maxPublicBodyBytes
	}
	return maxConsoleBodyBytes
}

// publicIntakeBodyLimit reports whether the path belongs to the server's
// 256 KB public JSON intake class (SEC-36 PUBLIC_JSON_BODY_LIMIT_BYTES —
// "public intake: FRs, comments, votes, user, feedback"). Most of that class
// lives under /api/v1/public/, but four route families sit outside it and
// are capped just the same (issue #382): the feature-request propose create,
// the id-scoped comment and vote mutations (the DELETE reads its body
// through the same 256 KB bound), POST /api/v1/feedback, and upload-session
// creation; the end-user POST /api/v1/me/link and /api/v1/me/erase routes
// are enforced at the same 256 KB server-side. The binary upload surfaces
// deliberately stay console-class: /api/v1/uploads/images accepts the 11 MB
// multipart envelope class and /api/v1/uploads/{uploadId} is bounded by the
// session's declared file size, not this JSON cap.
func publicIntakeBodyLimit(path string) bool {
	rest, ok := strings.CutPrefix(path, "/api/v1/")
	if !ok {
		return false
	}
	rest, _, _ = strings.Cut(rest, "?")
	segs := strings.Split(rest, "/")
	switch {
	case segs[0] == "public":
		return true
	case segs[0] == "feature-requests":
		// The propose create (exact) plus /:id/comments and /:id/vote; the
		// console equivalents all start /api/v1/console/ and cannot match.
		return len(segs) == 1 || (len(segs) == 3 && (segs[2] == "comments" || segs[2] == "vote"))
	case segs[0] == "feedback":
		return len(segs) == 1
	case segs[0] == "me":
		return len(segs) == 2 && (segs[1] == "link" || segs[1] == "erase")
	case segs[0] == "uploads":
		return len(segs) == 2 && segs[1] == "sessions"
	}
	return false
}

func upper(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'a' && c <= 'z' {
			out[i] = c - 32
		}
	}
	return string(out)
}

// signUserAttrsOutput is the --json payload of `api sign-user-attrs`.
type signUserAttrsOutput struct {
	AppKey    string `json:"appKey" yaml:"appKey"`
	UserToken string `json:"userToken" yaml:"userToken"`
	Timestamp int64  `json:"timestamp" yaml:"timestamp"`
	Canonical string `json:"canonical" yaml:"canonical"`
	Signature string `json:"signature" yaml:"signature"`
	Note      string `json:"note,omitempty" yaml:"note,omitempty"`
}

// signingEnvSecret names the environment fallback for the SDK signing secret,
// so the value never has to appear on a command line.
const signingEnvSecret = "CUPTHREAD_SDK_SIGNING_SECRET"

// resolveSigningSecret applies the sign-user-attrs secret convention: a "-"
// or "@" flag reads stdin (trimmed), an inline flag wins over the
// $CUPTHREAD_SDK_SIGNING_SECRET environment fallback (trimmed), and an error
// names every accepted source when none is set. inputPath is checked first so
// --secret and --input cannot both consume stdin in one invocation.
func resolveSigningSecret(ctx context.Context, secretFlag, inputPath string) (string, error) {
	readsStdin := func(v string) bool { return v == "-" || v == "@" }
	if readsStdin(secretFlag) {
		if readsStdin(inputPath) {
			return "", errors.New("--secret and --input cannot both read stdin; pass at least one as a file path")
		}
		data, err := readInputFile(ctx, secretFlag, maxSecretBytes)
		if err != nil {
			return "", err
		}
		secret := strings.TrimSpace(string(data))
		if secret == "" {
			return "", errors.New("stdin carried no SDK signing secret")
		}
		return secret, nil
	}
	if secretFlag != "" {
		return secretFlag, nil
	}
	if env := strings.TrimSpace(os.Getenv(signingEnvSecret)); env != "" {
		return env, nil
	}
	return "", fmt.Errorf("SDK signing secret is required: pass --secret <value>, pipe it via --secret - (stdin), or set $%s (generate one in the console: App Access → App Credentials)", signingEnvSecret)
}

func newAPISignUserAttrsCmd() *cobra.Command {
	var (
		inputPath  string
		appKey     string
		secretFlag string
		userToken  string
		timestamp  int64
	)
	sign := &cobra.Command{
		Use:   "sign-user-attrs",
		Short: "Compute the HMAC signature for a payment-attribute user update",
		Long: `Compute the signature + timestamp that
PUT /api/v1/public/apps/{appKey}/user requires whenever the body reports
payment attributes (isPaying, mrr, plan, or currency).

The signature is lowercase-hex HMAC-SHA256 over a newline-joined canonical
string (no trailing newline), keyed with the app's SDK signing secret
(generate one in the console: App Access → App Credentials):

  cpt-user-attrs-v1
  <appKey>
  <userToken>
  <isPaying: true|false|unset>
  <plan: value|null|unset>
  <mrr: canonicalNumber|null|unset>
  <currency: valueAsSent|unset>
  <timestamp: epochSeconds>

Pass the exact JSON body you plan to send via --input (a file path,
"@path", or "-" / "@" for stdin; capped at the public route's 256 KB body
limit). Absent fields sign as "unset" and explicit JSON null as "null";
values are signed as sent (currency before any server-side normalization).
userToken comes from the body, falling back to --user-token (the
X-User-Token header value). The server verifies the signature over the
userToken exactly as presented, then folds it to lowercase only for
identity storage — sign and send the same spelling (mixed-case UUIDs
included); signing one spelling and sending another fails 401
invalid_signature (issue #260).

The body is mirrored against the server's schema before anything is signed
(issue #196): currency must be a 3-letter alphabetic code and may not be
explicit null, mrr at most 1000000, plan 1-64 characters, isPaying a
non-null boolean, and userToken an RFC 4122 UUID (in the body, or in
--user-token when the body omits it). A body the API would always reject
fails here with the offending field named, so no dead-on-arrival signature
is ever produced; plan and mrr stay nullable and sign as "null".

The plan strings "unset" and "null" are reserved signature sentinels: an
absent plan signs as "unset" and an explicit null as "null", so a plan
equal to either exact string is rejected here (issue #235) and the API
answers 400 fieldErrors.plan ["plan_reserved_sentinel"]. Send JSON null to
clear the stored plan or omit the key to keep it; the reservation is
byte-exact, so "UNSET", "Unset", "unsettled", "nullify" remain valid.

The signing secret is a credential: pass it as --secret - (or @) to read it
from stdin, or export $CUPTHREAD_SDK_SIGNING_SECRET and omit --secret
entirely. Trailing whitespace is trimmed from the stdin and env forms. An
inline --secret value still works but lands in shell history and is visible
via ps — prefer stdin or the env var. --secret and --input cannot both read
stdin in the same invocation.

The server rejects timestamps more than ±300 seconds from its clock, so send
the signed body promptly: add the returned "signature" and "timestamp" fields
to the body without changing the signed values.`,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if appKey == "" {
				return errors.New("--app-key is required")
			}
			if inputPath == "" {
				return errors.New("--input is required (the exact JSON body you will send)")
			}
			secret, err := resolveSigningSecret(cmd.Context(), secretFlag, inputPath)
			if err != nil {
				if perr := A.reportInputTooLarge(err); perr != nil {
					return perr
				}
				return err
			}
			// The signed body targets PUT /api/v1/public/apps/{appKey}/user,
			// so it is capped at the public route's 256 KB intake limit.
			body, err := readInputFile(cmd.Context(), inputPath, maxPublicBodyBytes)
			if err != nil {
				if perr := A.reportInputTooLarge(err); perr != nil {
					return perr
				}
				return flagInputError("input", err)
			}
			raw, err := api.DecodeSDKAttributeBody(body)
			if err != nil {
				return err
			}
			// Issue #196: mirror the server's EndUserAttributesInputSchema
			// before signing, so a body the API can never accept fails here
			// with the offending field named instead of producing a
			// signature that is guaranteed to be answered by a 400.
			if err := api.ValidateSDKAttributeBody(raw); err != nil {
				return err
			}
			token, err := api.ResolveSDKUserToken(raw, userToken)
			if err != nil {
				return err
			}
			if err := api.ValidateResolvedSDKToken(token); err != nil {
				return err
			}
			stamp := timestamp
			if stamp == 0 {
				stamp = time.Now().Unix()
			}
			canonical, signature, err := api.SignSDKAttributePayload(secret, api.SDKAttributePayload{
				AppKey:    appKey,
				UserToken: token,
				Raw:       raw,
				Timestamp: stamp,
			})
			if err != nil {
				return err
			}
			out := signUserAttrsOutput{
				AppKey:    appKey,
				UserToken: token,
				Timestamp: stamp,
				Canonical: canonical,
				Signature: signature,
			}
			// The server only demands a signature when one of the payment
			// attributes is present (even explicit null counts). Since
			// DATA-07 currency is a payment attribute too.
			if _, ok := raw["isPaying"]; !ok {
				if _, ok := raw["mrr"]; !ok {
					if _, ok := raw["plan"]; !ok {
						if _, ok := raw["currency"]; !ok {
							out.Note = "body reports no payment attributes; the API accepts this request without a signature"
						}
					}
				}
			}
			if A.structured() {
				return A.out.Structured(out)
			}
			A.out.Printf("✓ signed payment attributes for %s (userToken %s)", appKey, token)
			A.out.Printf("canonical:")
			A.out.Printf("%s", canonical)
			A.out.Printf("signature: %s", signature)
			A.out.Printf("timestamp: %d", stamp)
			if out.Note != "" {
				A.out.Printf("note: %s", out.Note)
			}
			return nil
		},
	}
	sign.Flags().StringVar(&inputPath, "input", "", "exact JSON request body to sign: file path, \"@path\", or \"-\"/\"@\" for stdin; max 256 KB")
	sign.Flags().StringVar(&appKey, "app-key", "", "appKey path segment of the target app")
	sign.Flags().StringVar(&secretFlag, "secret", "", "SDK signing secret: value, or \"-\"/\"@\" for stdin; falls back to $"+signingEnvSecret)
	sign.Flags().StringVar(&userToken, "user-token", "", "X-User-Token header value when the body carries no userToken")
	sign.Flags().Int64Var(&timestamp, "timestamp", 0, "signature timestamp, epoch seconds (default: now)")
	return sign
}
