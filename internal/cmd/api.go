package cmd

import (
	"bytes"
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
with --input @file (or "-" for stdin). This is the escape hatch for endpoints
the CLI does not wrap yet.

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

			var body any
			if inputPath != "" {
				data, err := readInputFile(inputPath)
				if err != nil {
					return err
				}
				dec := json.NewDecoder(bytes.NewReader(data))
				if err := dec.Decode(&body); err != nil {
					return fmt.Errorf("parse input JSON: %w", err)
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
				// Still surface structured API errors as JSON when in JSON mode.
				// errors.As is required because tier-limit (402) errors are
				// wrapped by the client.
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
					if hint := apiErr.Hint(); hint != "" {
						payload["hint"] = hint
					}
					return A.out.Structured(payload)
				}
				return err
			}

			if A.structured() {
				return A.out.Structured(raw)
			}
			if len(raw) == 0 {
				A.out.Printf("✓ %s %s succeeded (no response body, request-id %s)", method, path, requestID)
				return nil
			}
			A.out.Printf("✓ %s %s (request-id %s)", method, path, requestID)
			A.out.Printf("%s", raw)
			return nil
		},
	}
	req.Flags().StringVar(&inputPath, "input", "", "JSON request body file (\"-\" or \"@\" for stdin)")
	return req
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
func resolveSigningSecret(secretFlag, inputPath string) (string, error) {
	readsStdin := func(v string) bool { return v == "-" || v == "@" }
	if readsStdin(secretFlag) {
		if readsStdin(inputPath) {
			return "", errors.New("--secret and --input cannot both read stdin; pass at least one as a file path")
		}
		data, err := readInputFile(secretFlag)
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
payment attributes (isPaying, mrr, or plan).

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

Pass the exact JSON body you plan to send via --input (a file path, or "-" or
"@" for stdin). Absent fields sign as "unset" and explicit JSON null as
"null"; values are signed as sent (currency before any server-side
normalization). userToken comes from the body, falling back to --user-token
(the X-User-Token header value).

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
			secret, err := resolveSigningSecret(secretFlag, inputPath)
			if err != nil {
				return err
			}
			body, err := readInputFile(inputPath)
			if err != nil {
				return err
			}
			raw, err := api.DecodeSDKAttributeBody(body)
			if err != nil {
				return err
			}
			token, err := api.ResolveSDKUserToken(raw, userToken)
			if err != nil {
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
			// attributes is present (even explicit null counts).
			if _, ok := raw["isPaying"]; !ok {
				if _, ok := raw["mrr"]; !ok {
					if _, ok := raw["plan"]; !ok {
						out.Note = "body reports no payment attributes; the API accepts this request without a signature"
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
	sign.Flags().StringVar(&inputPath, "input", "", "exact JSON request body to sign (\"-\" or \"@\" for stdin)")
	sign.Flags().StringVar(&appKey, "app-key", "", "appKey path segment of the target app")
	sign.Flags().StringVar(&secretFlag, "secret", "", "SDK signing secret: value, or \"-\"/\"@\" for stdin; falls back to $"+signingEnvSecret)
	sign.Flags().StringVar(&userToken, "user-token", "", "X-User-Token header value when the body carries no userToken")
	sign.Flags().Int64Var(&timestamp, "timestamp", 0, "signature timestamp, epoch seconds (default: now)")
	return sign
}
