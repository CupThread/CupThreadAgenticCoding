package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// The golden signatures below were generated independently with Node from
// the SaaS reference implementation (apps/api/src/lib/sdk-attributes-signing.ts,
// native Number.prototype.toFixed(2) canonical numbers), so any drift between
// this Go port and the server contract fails here first.
const (
	testSecret   = "cpt_sk_test_secret_0123456789abcdef"
	testAppKey   = "app_demo12345"
	testToken    = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	testAltToken = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
	testStamp    = 1758000000
)

type goldenCase struct {
	name string
	// body is the raw request body as sent.
	body string
	// headerToken is the X-User-Token fallback; the resolved token signs.
	headerToken string
	canonical   string
	signature   string
}

func goldenSigningCases(t *testing.T) []goldenCase {
	t.Helper()
	return []goldenCase{
		{
			name:        "full payment attributes",
			body:        `{"isPaying":true,"plan":"pro","mrr":299.5,"currency":"USD"}`,
			headerToken: testToken,
			canonical:   "cpt-user-attrs-v1\n" + testAppKey + "\n" + testToken + "\ntrue\npro\n299.5\nUSD\n1758000000",
			signature:   "59bc1177751f4c36f9caeed8c763cde9ee18835196acd1b6d4ce3b1ea2dc0273",
		},
		{
			name:        "isPaying false only, absent fields unset",
			body:        `{"isPaying":false}`,
			headerToken: testToken,
			canonical:   "cpt-user-attrs-v1\n" + testAppKey + "\n" + testToken + "\nfalse\nunset\nunset\nunset\n1758000000",
			signature:   "a909c6a3571dda772392b17ffc84319f032d672deeb1ea422e48c503cf8ef5fe",
		},
		{
			name:        "explicit null plan/mrr sign as null",
			body:        `{"plan":null,"mrr":null,"currency":"EUR"}`,
			headerToken: testToken,
			canonical:   "cpt-user-attrs-v1\n" + testAppKey + "\n" + testToken + "\nunset\nnull\nnull\nEUR\n1758000000",
			signature:   "b6938265d28ec04e280456a79a65da50287846cb2197de9addcebf5cbe466e33",
		},
		{
			name:        "mrr 1200 renders without fraction",
			body:        `{"isPaying":true,"mrr":1200,"currency":"USD"}`,
			headerToken: testToken,
			canonical:   "cpt-user-attrs-v1\n" + testAppKey + "\n" + testToken + "\ntrue\nunset\n1200\nUSD\n1758000000",
			signature:   "82c1c04e71f0e3c3151f095ca322c567b1984136cffe750b356b705a75d724e9",
		},
		{
			name:        "mrr 99.995 rounds up off the exact binary value",
			body:        `{"mrr":99.995,"currency":"usd"}`,
			headerToken: testToken,
			canonical:   "cpt-user-attrs-v1\n" + testAppKey + "\n" + testToken + "\nunset\nunset\n100\nusd\n1758000000",
			signature:   "2a22c8981ad90ee59d14b115e12f48a1639237ae54489980e98f4a8bcb10d532",
		},
		{
			name:        "mrr 10.125 exact tie picks the larger n",
			body:        `{"mrr":10.125,"currency":"USD"}`,
			headerToken: testToken,
			canonical:   "cpt-user-attrs-v1\n" + testAppKey + "\n" + testToken + "\nunset\nunset\n10.13\nUSD\n1758000000",
			signature:   "7fd7e5330e5f734aeea01329245c2b29675f1d7a53d15cedece4ee8615d5c1bc",
		},
		{
			name:        "mrr zero renders as 0",
			body:        `{"isPaying":false,"mrr":0,"currency":"USD"}`,
			headerToken: testToken,
			canonical:   "cpt-user-attrs-v1\n" + testAppKey + "\n" + testToken + "\nfalse\nunset\n0\nUSD\n1758000000",
			signature:   "8746748aabeef036357a947f91f2c6c4bcee581792bc97ab228d8100a7ed0213",
		},
		{
			name:        "body userToken wins over X-User-Token header",
			body:        `{"isPaying":true,"plan":"Pro Annual","currency":"usd","userToken":"7c9e6679-7425-40de-944b-e07fc1f90ae7"}`,
			headerToken: testToken,
			canonical:   "cpt-user-attrs-v1\n" + testAppKey + "\n" + testAltToken + "\ntrue\nPro Annual\nunset\nusd\n1758000000",
			signature:   "d76ace06e2eec01d58a9c36bc62f2f7270c29a3cd11b0c0f45506bfd053be048",
		},
		{
			name:        "plan signs unicode verbatim",
			body:        `{"plan":"Pro – Ånnual ✓","currency":"SEK"}`,
			headerToken: testToken,
			canonical:   "cpt-user-attrs-v1\n" + testAppKey + "\n" + testToken + "\nunset\nPro – Ånnual ✓\nunset\nSEK\n1758000000",
			signature:   "7a88dc701f317f903385cb5be3756e1683a950017f9aaaefcc24cecbdb75e1c7",
		},
		{
			name:        "currency alone is a signed payment attribute (DATA-07)",
			body:        `{"currency":"jpy"}`,
			headerToken: testToken,
			canonical:   "cpt-user-attrs-v1\n" + testAppKey + "\n" + testToken + "\nunset\nunset\nunset\njpy\n1758000000",
			signature:   "b227f3014d7e00747cb2054a9d53fe60b5a015f36999d7ca2ad02d817effba0e",
		},
		{
			name:        "issue #196 golden: mrr 99 and lowercase currency sign as sent",
			body:        `{"isPaying":true,"mrr":99,"currency":"usd","userToken":"3fa85f64-5717-4562-b3fc-2c963f66afa6"}`,
			headerToken: testToken,
			canonical:   "cpt-user-attrs-v1\n" + testAppKey + "\n" + testToken + "\ntrue\nunset\n99\nusd\n1758000000",
			signature:   "06a006bb698c0c1845578ab27889156b2df02beb628205dae8d70140ef340208",
		},
	}
}

func TestSignSDKAttributePayloadGoldenVectors(t *testing.T) {
	for _, tc := range goldenSigningCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := DecodeSDKAttributeBody([]byte(tc.body))
			if err != nil {
				t.Fatalf("decode body: %v", err)
			}
			userToken, err := ResolveSDKUserToken(raw, tc.headerToken)
			if err != nil {
				t.Fatalf("resolve userToken: %v", err)
			}
			canonical, signature, err := SignSDKAttributePayload(testSecret, SDKAttributePayload{
				AppKey:    testAppKey,
				UserToken: userToken,
				Raw:       raw,
				Timestamp: testStamp,
			})
			if err != nil {
				t.Fatalf("sign: %v", err)
			}
			if canonical != tc.canonical {
				t.Errorf("canonical = %q, want %q", canonical, tc.canonical)
			}
			if signature != tc.signature {
				t.Errorf("signature = %q, want %q", signature, tc.signature)
			}
		})
	}
}

func TestCanonicalNumberMatchesToFixed2(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{50, "50"},
		{99.5, "99.5"},
		{1200, "1200"},
		{1200.005, "1200.01"}, // binary is 1200.0050000000001…, rounds up
		{29.99, "29.99"},      // binary is 29.98999…
		{10.125, "10.13"},     // exact tie: ECMAScript picks the larger n
		{0.125, "0.13"},       // exact tie below 1
		{3.625, "3.63"},       // exact tie, odd base
		{99.995, "100"},       // binary is 99.99500…004, carries
		{0.05, "0.05"},        // hundredths need zero padding
		{0.005, "0.01"},       // tiny tie
		{999999.99, "999999.99"},
		{1e21, "1e+21"}, // out of schema range but must not crash
	}
	for _, tc := range cases {
		got, err := canonicalNumber(tc.in)
		if err != nil {
			t.Fatalf("canonicalNumber(%v): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("canonicalNumber(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if _, err := canonicalNumber(-1); err == nil {
		t.Error("canonicalNumber(-1) = nil error, want nonnegative constraint error")
	}
}

func TestCanonicalizeSDKAttributePayloadValidation(t *testing.T) {
	empty := map[string]json.RawMessage{}
	if _, err := CanonicalizeSDKAttributePayload(SDKAttributePayload{UserToken: testToken, Raw: empty, Timestamp: 1}); err == nil {
		t.Error("missing appKey: want error")
	}
	if _, err := CanonicalizeSDKAttributePayload(SDKAttributePayload{AppKey: testAppKey, Raw: empty, Timestamp: 1}); err == nil {
		t.Error("missing userToken: want error")
	}
	if _, err := CanonicalizeSDKAttributePayload(SDKAttributePayload{AppKey: testAppKey, UserToken: testToken, Raw: empty, Timestamp: -1}); err == nil {
		t.Error("negative timestamp: want error")
	}
	bad := map[string]json.RawMessage{"mrr": json.RawMessage(`"expensive"`)}
	if _, err := CanonicalizeSDKAttributePayload(SDKAttributePayload{AppKey: testAppKey, UserToken: testToken, Raw: bad, Timestamp: 1}); err == nil {
		t.Error("string mrr: want type error")
	}
	if _, _, err := SignSDKAttributePayload("", SDKAttributePayload{AppKey: testAppKey, UserToken: testToken, Raw: empty, Timestamp: 1}); err == nil {
		t.Error("empty secret: want error")
	}
}

func TestSignSDKAttributePayloadSignatureShape(t *testing.T) {
	raw, err := DecodeSDKAttributeBody([]byte(`{"isPaying":true}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	_, signature, err := SignSDKAttributePayload(testSecret, SDKAttributePayload{
		AppKey:    testAppKey,
		UserToken: testToken,
		Raw:       raw,
		Timestamp: testStamp,
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if len(signature) != 64 {
		t.Errorf("signature length = %d, want 64 hex chars", len(signature))
	}
	if strings.ToLower(signature) != signature {
		t.Errorf("signature %q is not lowercase hex", signature)
	}
}

// TestCanonicalCurrencyRejectsExplicitNull pins issue #196: currency is not
// nullable server-side, so no canonicalization path may render an explicit
// null as an empty line (the old json.Unmarshal no-op) — it fails loudly
// instead.
func TestCanonicalCurrencyRejectsExplicitNull(t *testing.T) {
	if got, err := canonicalCurrency(json.RawMessage(`null`)); err == nil {
		t.Errorf("canonicalCurrency(null) = %q, want an error", got)
	} else if !strings.Contains(err.Error(), "omit the key instead") && !strings.Contains(err.Error(), "omit it instead") {
		t.Errorf("error = %v, want self-diagnosing guidance to omit the field", err)
	}
	raw := map[string]json.RawMessage{"currency": json.RawMessage(`null`)}
	if _, err := CanonicalizeSDKAttributePayload(SDKAttributePayload{
		AppKey: testAppKey, UserToken: testToken, Raw: raw, Timestamp: testStamp,
	}); err == nil {
		t.Error("canonicalize with currency null: want error, no empty canonical line")
	}
}

// TestValidateSDKAttributeBodySchemaMirror checks the local mirror of the
// server's EndUserAttributesInputSchema (SaaS
// packages/shared/src/schemas/end-users.ts): bodies the server always
// rejects at zod parse fail validation, bodies it accepts pass — including
// the nullable plan/mrr, the rune-based plan length, and the zod v4 UUID
// alternatives (reserved nil/max forms pass, wrong variant nibble fails).
func TestValidateSDKAttributeBodySchemaMirror(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr string // empty = must validate
	}{
		{name: "empty body", body: `{}`},
		{name: "valid full body", body: `{"isPaying":true,"mrr":99,"currency":"usd","userToken":"3fa85f64-5717-4562-b3fc-2c963f66afa6"}`},
		{name: "unknown keys are stripped server-side", body: `{"email":"a@b.c","nickname":"x"}`},
		{name: "nullable plan and mrr sign as null", body: `{"plan":null,"mrr":null}`},
		{name: "mrr exactly at the cap", body: `{"mrr":1000000}`},
		{name: "plan at 64 ascii chars", body: `{"plan":"` + strings.Repeat("a", 64) + `"}`},
		{name: "plan length counts code points not bytes", body: `{"plan":"` + strings.Repeat("✓", 64) + `"}`},
		{name: "reserved nil uuid body token", body: `{"userToken":"00000000-0000-0000-0000-000000000000"}`},
		{name: "reserved max uuid body token", body: `{"userToken":"ffffffff-ffff-ffff-ffff-ffffffffffff"}`},

		{name: "currency explicit null", body: `{"currency":null}`, wantErr: "explicit null is not accepted server-side"},
		{name: "currency not a code", body: `{"currency":"dollars"}`, wantErr: "must be a 3-letter alphabetic code"},
		{name: "currency short", body: `{"currency":"us"}`, wantErr: "must be a 3-letter alphabetic code"},
		{name: "currency with a digit", body: `{"currency":"u5d"}`, wantErr: "must be a 3-letter alphabetic code"},
		{name: "currency wrong type", body: `{"currency":3}`, wantErr: "currency must be a string"},
		{name: "mrr over the cap", body: `{"mrr":1000001}`, wantErr: "must be at most 1000000"},
		{name: "mrr fractionally over the cap", body: `{"mrr":1000000.5}`, wantErr: "must be at most 1000000"},
		{name: "mrr wrong type", body: `{"mrr":"expensive"}`, wantErr: "mrr must be a number"},
		{name: "plan empty", body: `{"plan":""}`, wantErr: "must be 1-64 characters, got 0"},
		{name: "plan 65 chars", body: `{"plan":"` + strings.Repeat("a", 65) + `"}`, wantErr: "must be 1-64 characters, got 65"},
		{name: "plan wrong type", body: `{"plan":7}`, wantErr: "plan must be a string or null"},
		{name: "isPaying explicit null", body: `{"isPaying":null}`, wantErr: "explicit null is not accepted server-side"},
		{name: "isPaying wrong type", body: `{"isPaying":"yes"}`, wantErr: "isPaying must be a boolean"},
		{name: "userToken not a uuid", body: `{"userToken":"not-a-uuid"}`, wantErr: "must be an RFC 4122 UUID"},
		{name: "userToken empty", body: `{"userToken":""}`, wantErr: "must be an RFC 4122 UUID"},
		{name: "userToken explicit null", body: `{"userToken":null}`, wantErr: "explicit null is not accepted server-side"},
		{name: "userToken wrong variant nibble", body: `{"userToken":"3fa85f64-5717-4562-c3fc-2c963f66afa6"}`, wantErr: "must be an RFC 4122 UUID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := DecodeSDKAttributeBody([]byte(tc.body))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			err = ValidateSDKAttributeBody(raw)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateSDKAttributeBody(%s): %v, want accepted", tc.body, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateSDKAttributeBody(%s): nil error, want %q", tc.body, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestValidateResolvedSDKToken mirrors the route's UUID_RE gate on the
// resolved token: case-insensitive 8-4-4-4-12 hex, no version constraint —
// the shape that also guards the X-User-Token header fallback.
func TestValidateResolvedSDKToken(t *testing.T) {
	for _, token := range []string{
		testToken,
		"3FA85F64-5717-4562-B3FC-2C963F66AFA6", // UUID_RE is case-insensitive
		"00000000-0000-0000-0000-000000000000",
	} {
		if err := ValidateResolvedSDKToken(token); err != nil {
			t.Errorf("ValidateResolvedSDKToken(%q): %v, want accepted", token, err)
		}
	}
	for _, token := range []string{"", "local-user", "not-a-uuid", "3fa85f64_5717_4562_b3fc_2c963f66afa6"} {
		if err := ValidateResolvedSDKToken(token); err == nil {
			t.Errorf("ValidateResolvedSDKToken(%q): nil error, want rejected", token)
		}
	}
}
