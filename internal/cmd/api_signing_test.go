package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The golden values below mirror the vectors in internal/api/usersigning_test.go,
// which were generated independently with Node from the SaaS reference
// implementation (apps/api/src/lib/sdk-attributes-signing.ts). If these two
// copies ever disagree, the contract moved and both need re-syncing.
const (
	signTestSecret = "cpt_sk_test_secret_0123456789abcdef"
	signTestAppKey = "app_demo12345"
	signTestToken  = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	signAltToken   = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
	signTestStamp  = 1758000000
	// Signature of the full payment-attribute body (signGoldenBody below)
	// keyed with signTestSecret at signTestStamp, per TestAPISignUserAttrsGolden.
	signGoldenSignature = "59bc1177751f4c36f9caeed8c763cde9ee18835196acd1b6d4ce3b1ea2dc0273"
	signGoldenBody      = `{"isPaying":true,"plan":"pro","mrr":299.5,"currency":"USD"}`
)

func writeSignBody(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write body: %v", err)
	}
	return path
}

type signPayload struct {
	Canonical string `json:"canonical"`
	Signature string `json:"signature"`
	Timestamp int64  `json:"timestamp"`
	UserToken string `json:"userToken"`
	Note      string `json:"note"`
}

func runSign(t *testing.T, body string, extra ...string) (signPayload, string, error) {
	t.Helper()
	args := append([]string{
		"api", "sign-user-attrs",
		"--app-key", signTestAppKey,
		"--secret", signTestSecret,
		"--timestamp", "1758000000",
		"--input", writeSignBody(t, body),
	}, extra...)
	out, err := runRoot(t, "http://127.0.0.1:1", args...)
	var payload signPayload
	if err == nil {
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
			t.Fatalf("decode structured output %q: %v", out, err)
		}
	}
	return payload, out, err
}

// TestAPISignUserAttrsGolden pins the CLI helper against the wire contract:
// the emitted canonical string and signature must match the Node-derived
// reference vectors for the full payment-attribute body.
func TestAPISignUserAttrsGolden(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	payload, _, err := runSign(t,
		`{"isPaying":true,"plan":"pro","mrr":299.5,"currency":"USD"}`,
		"--user-token", signTestToken, "--json")
	if err != nil {
		t.Fatalf("sign-user-attrs: %v", err)
	}
	wantCanonical := "cpt-user-attrs-v1\n" + signTestAppKey + "\n" + signTestToken +
		"\ntrue\npro\n299.5\nUSD\n1758000000"
	if payload.Canonical != wantCanonical {
		t.Errorf("canonical = %q, want %q", payload.Canonical, wantCanonical)
	}
	if payload.Signature != "59bc1177751f4c36f9caeed8c763cde9ee18835196acd1b6d4ce3b1ea2dc0273" {
		t.Errorf("signature = %q", payload.Signature)
	}
	if payload.Timestamp != signTestStamp || payload.UserToken != signTestToken {
		t.Errorf("payload = %+v", payload)
	}
	if payload.Note != "" {
		t.Errorf("note = %q, want empty for a payment-attribute body", payload.Note)
	}
}

// TestAPISignUserAttrsTokenResolution covers both halves of the server's
// resolution order: a body userToken wins over the X-User-Token header value,
// and the header value fills in when the body omits it.
func TestAPISignUserAttrsTokenResolution(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	payload, _, err := runSign(t,
		`{"isPaying":true,"plan":"Pro Annual","currency":"usd","userToken":"`+signAltToken+`"}`,
		"--user-token", signTestToken, "--json")
	if err != nil {
		t.Fatalf("sign-user-attrs: %v", err)
	}
	if payload.UserToken != signAltToken {
		t.Errorf("body userToken = %q, want it to win over --user-token", payload.UserToken)
	}
	if payload.Signature != "d76ace06e2eec01d58a9c36bc62f2f7270c29a3cd11b0c0f45506bfd053be048" {
		t.Errorf("signature = %q", payload.Signature)
	}

	payload, _, err = runSign(t, `{"isPaying":false}`, "--user-token", signTestToken, "--json")
	if err != nil {
		t.Fatalf("sign-user-attrs: %v", err)
	}
	if payload.UserToken != signTestToken {
		t.Errorf("userToken = %q, want the --user-token fallback", payload.UserToken)
	}
	wantCanonical := "cpt-user-attrs-v1\n" + signTestAppKey + "\n" + signTestToken +
		"\nfalse\nunset\nunset\nunset\n1758000000"
	if payload.Canonical != wantCanonical {
		t.Errorf("canonical = %q, want %q", payload.Canonical, wantCanonical)
	}
}

// TestAPISignUserAttrsUnsignedNote verifies the helper tells agents when the
// body does not need a signature at all (identity/currency-only writes stay
// unsigned per the DATA-03 contract).
func TestAPISignUserAttrsUnsignedNote(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	payload, _, err := runSign(t, `{"currency":"USD"}`, "--user-token", signTestToken, "--json")
	if err != nil {
		t.Fatalf("sign-user-attrs: %v", err)
	}
	if !strings.Contains(payload.Note, "without a signature") {
		t.Errorf("note = %q, want the unsigned-is-accepted hint", payload.Note)
	}

	// Explicit null still counts as asserting a payment attribute, mirroring
	// the server's `rawBody.plan !== undefined` check.
	payload, _, err = runSign(t, `{"plan":null}`, "--user-token", signTestToken, "--json")
	if err != nil {
		t.Fatalf("sign-user-attrs: %v", err)
	}
	if payload.Note != "" {
		t.Errorf("note = %q, want empty when plan is explicitly null", payload.Note)
	}
}

// TestAPISignUserAttrsHumanOutput checks the table-mode output quotes the
// signature and canonical string so an agent can eyeball what was signed.
func TestAPISignUserAttrsHumanOutput(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	out, err := runRoot(t, "http://127.0.0.1:1",
		"api", "sign-user-attrs",
		"--app-key", signTestAppKey,
		"--secret", signTestSecret,
		"--timestamp", "1758000000",
		"--user-token", signTestToken,
		"--input", writeSignBody(t, `{"isPaying":true,"plan":"pro","mrr":299.5,"currency":"USD"}`))
	if err != nil {
		t.Fatalf("sign-user-attrs: %v", err)
	}
	for _, want := range []string{
		"✓ signed payment attributes for " + signTestAppKey,
		"signature: 59bc1177751f4c36f9caeed8c763cde9ee18835196acd1b6d4ce3b1ea2dc0273",
		"timestamp: 1758000000",
		"cpt-user-attrs-v1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q missing %q", out, want)
		}
	}
}

// TestAPISignUserAttrsErrors covers the argument validation and the
// contract-level rejections surfaced from the canonicalizer.
func TestAPISignUserAttrsErrors(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing app key",
			args:    []string{"api", "sign-user-attrs", "--secret", signTestSecret, "--input", writeSignBody(t, `{}`)},
			wantErr: "--app-key is required",
		},
		{
			name:    "missing input",
			args:    []string{"api", "sign-user-attrs", "--app-key", signTestAppKey, "--secret", signTestSecret},
			wantErr: "--input is required",
		},
		{
			name: "missing user token in body and flag",
			args: []string{"api", "sign-user-attrs", "--app-key", signTestAppKey, "--secret", signTestSecret,
				"--input", writeSignBody(t, `{"isPaying":true}`)},
			wantErr: "userToken is required",
		},
		{
			name: "mrr must be a number",
			args: []string{"api", "sign-user-attrs", "--app-key", signTestAppKey, "--secret", signTestSecret,
				"--user-token", signTestToken, "--input", writeSignBody(t, `{"mrr":"expensive"}`)},
			wantErr: "mrr must be a number",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runRoot(t, "http://127.0.0.1:1", tc.args...)
			if err == nil {
				t.Fatalf("sign-user-attrs %v: want error, got output %q", tc.args, out)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// withSignStdin replaces os.Stdin with a file carrying content for the
// duration of the test, so the --secret -/@ stdin path is hermetic.
func withSignStdin(t *testing.T, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write stdin file: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open stdin file: %v", err)
	}
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() {
		os.Stdin = old
		f.Close()
	})
}

// runSignFlags runs sign-user-attrs against the golden body with a
// caller-controlled flag set (used by the secret-source tests).
func runSignFlags(t *testing.T, args ...string) (signPayload, string, error) {
	t.Helper()
	base := []string{
		"api", "sign-user-attrs",
		"--app-key", signTestAppKey,
		"--timestamp", "1758000000",
		"--user-token", signTestToken,
		"--input", writeSignBody(t, signGoldenBody),
	}
	out, err := runRoot(t, "http://127.0.0.1:1", append(base, args...)...)
	var payload signPayload
	if err == nil {
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
			t.Fatalf("decode structured output %q: %v", out, err)
		}
	}
	return payload, out, err
}

// TestAPISignUserAttrsSecretFromStdin pins the stdin forms: --secret - and
// --secret @ must produce the byte-identical signature to the inline flag.
func TestAPISignUserAttrsSecretFromStdin(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
	t.Setenv("CUPTHREAD_SDK_SIGNING_SECRET", "")

	for _, form := range []string{"-", "@"} {
		t.Run("form "+form, func(t *testing.T) {
			withSignStdin(t, signTestSecret+"\n")
			payload, _, err := runSignFlags(t, "--secret", form, "--json")
			if err != nil {
				t.Fatalf("sign-user-attrs --secret %s: %v", form, err)
			}
			if payload.Signature != signGoldenSignature {
				t.Errorf("signature = %q, want the inline-flag golden %q", payload.Signature, signGoldenSignature)
			}
		})
	}

	// Empty stdin is a clean error, not a signature over an empty key.
	t.Run("empty stdin", func(t *testing.T) {
		withSignStdin(t, "\n")
		_, _, err := runSignFlags(t, "--secret", "-", "--json")
		if err == nil {
			t.Fatal("want error for empty stdin secret")
		}
		if !strings.Contains(err.Error(), "stdin carried no SDK signing secret") {
			t.Errorf("error = %q, want the empty-stdin message", err.Error())
		}
	})
}

// TestAPISignUserAttrsSecretTrimsWhitespace verifies trailing newlines and
// spaces on the stdin secret never change the signature (piped
// `echo $SECRET`-style usage always carries a trailing newline).
func TestAPISignUserAttrsSecretTrimsWhitespace(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
	t.Setenv("CUPTHREAD_SDK_SIGNING_SECRET", "")

	withSignStdin(t, "  "+signTestSecret+" \n\n")
	payload, _, err := runSignFlags(t, "--secret", "-", "--json")
	if err != nil {
		t.Fatalf("sign-user-attrs: %v", err)
	}
	if payload.Signature != signGoldenSignature {
		t.Errorf("signature = %q, want the trimmed secret to match the golden %q", payload.Signature, signGoldenSignature)
	}
}

// TestAPISignUserAttrsSecretEnv covers the $CUPTHREAD_SDK_SIGNING_SECRET
// fallback (including trailing-newline trimming) and the flag > env
// precedence.
func TestAPISignUserAttrsSecretEnv(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")

	t.Setenv("CUPTHREAD_SDK_SIGNING_SECRET", signTestSecret+"\n")
	payload, _, err := runSignFlags(t, "--json")
	if err != nil {
		t.Fatalf("sign-user-attrs without --secret: %v", err)
	}
	if payload.Signature != signGoldenSignature {
		t.Errorf("env-secret signature = %q, want the golden %q", payload.Signature, signGoldenSignature)
	}

	// When both are set the flag wins: the golden secret sits in the env, but
	// an inline different secret must change the signature.
	t.Setenv("CUPTHREAD_SDK_SIGNING_SECRET", signTestSecret)
	payload, _, err = runSignFlags(t, "--secret", "cpt_sk_inline_flag_secret", "--json")
	if err != nil {
		t.Fatalf("sign-user-attrs with both sources: %v", err)
	}
	if payload.Signature == signGoldenSignature {
		t.Error("signature matches the env secret; the inline --secret flag should win")
	}
}

// TestAPISignUserAttrsSecretMissingSources checks the no-source error names
// every accepted source so agents can self-serve.
func TestAPISignUserAttrsSecretMissingSources(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
	t.Setenv("CUPTHREAD_SDK_SIGNING_SECRET", "")

	_, _, err := runSignFlags(t, "--json")
	if err == nil {
		t.Fatal("want error when no secret source is set")
	}
	for _, want := range []string{"--secret", "stdin", "CUPTHREAD_SDK_SIGNING_SECRET"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err.Error(), want)
		}
	}
}

// TestAPISignUserAttrsSecretAndInputBothStdin pins the stdin conflict:
// --secret and --input cannot both consume stdin in one invocation.
func TestAPISignUserAttrsSecretAndInputBothStdin(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test_token")
	t.Setenv("CUPTHREAD_SDK_SIGNING_SECRET", "")

	withSignStdin(t, signTestSecret+"\n")
	_, _, err := runSignFlags(t, "--secret", "-", "--input", "-", "--json")
	if err == nil {
		t.Fatal("want error when --secret and --input both read stdin")
	}
	if !strings.Contains(err.Error(), "cannot both read stdin") {
		t.Errorf("error = %q, want the stdin-conflict message", err.Error())
	}
}
