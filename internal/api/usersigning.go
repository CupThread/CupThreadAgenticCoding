package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"unicode/utf8"
)

// SDKAttributeSignatureVersion is the domain tag on line 1 of the canonical
// string, ensuring signatures never cross between signing uses.
const SDKAttributeSignatureVersion = "cpt-user-attrs-v1"

// SDKAttributeSignatureFreshnessSeconds is the server's ± tolerance on the
// signature timestamp (epoch seconds).
const SDKAttributeSignatureFreshnessSeconds = 300

// Payment attributes (isPaying/mrr/plan/currency) on
// PUT /api/v1/public/apps/{appKey}/user are only accepted when the request
// carries signature + timestamp computed with the app's SDK signing secret.
// The canonical string is newline-joined with no trailing newline:
//
//	cpt-user-attrs-v1
//	<appKey>
//	<userToken>
//	<isPaying: true|false|unset>
//	<plan: value|null|unset>
//	<mrr: canonicalNumber|null|unset>
//	<currency: valueAsSent|unset>
//	<timestamp: epochSeconds>
//
// Absent fields sign as "unset"; explicit JSON null signs as "null". Values
// are the raw body values (currency as sent, before any server-side
// normalization), and userToken is the resolved token (body userToken, else
// the X-User-Token header).
type SDKAttributePayload struct {
	AppKey    string
	UserToken string
	// Raw is the request body exactly as sent; RawMessage keeps absent keys
	// ("unset") distinct from explicit null ("null").
	Raw map[string]json.RawMessage
	// Timestamp is the epoch-seconds value signed and sent with the request.
	Timestamp int64
}

// CanonicalizeSDKAttributePayload renders the exact byte string that must be
// HMAC-SHA256-signed for a payment-attribute report.
func CanonicalizeSDKAttributePayload(p SDKAttributePayload) (string, error) {
	if p.AppKey == "" {
		return "", errors.New("appKey is required")
	}
	if p.UserToken == "" {
		return "", errors.New("userToken is required (body userToken or X-User-Token header)")
	}
	if p.Timestamp < 0 {
		return "", errors.New("timestamp must be a nonnegative epoch-seconds integer")
	}
	isPaying, err := canonicalIsPaying(p.Raw["isPaying"])
	if err != nil {
		return "", err
	}
	plan, err := canonicalPlan(p.Raw["plan"])
	if err != nil {
		return "", err
	}
	mrr, err := canonicalMRR(p.Raw["mrr"])
	if err != nil {
		return "", err
	}
	currency, err := canonicalCurrency(p.Raw["currency"])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s\n%s\n%d",
		SDKAttributeSignatureVersion,
		p.AppKey,
		p.UserToken,
		isPaying,
		plan,
		mrr,
		currency,
		p.Timestamp,
	), nil
}

// SignSDKAttributePayload returns the lowercase-hex HMAC-SHA256 signature of
// the canonical payload string, keyed with the app's SDK signing secret.
func SignSDKAttributePayload(secret string, p SDKAttributePayload) (canonical string, signature string, err error) {
	canonical, err = CanonicalizeSDKAttributePayload(p)
	if err != nil {
		return "", "", err
	}
	if secret == "" {
		return "", "", errors.New("SDK signing secret is required (generate one in the console: App Access → App Credentials)")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(canonical))
	return canonical, hex.EncodeToString(mac.Sum(nil)), nil
}

// fieldState classifies a raw body field: missing ("unset"), explicit null
// ("null"), or a present value ("value").
func fieldState(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "unset"
	}
	if string(bytes.TrimSpace(raw)) == "null" {
		return "null"
	}
	return "value"
}

func canonicalIsPaying(raw json.RawMessage) (string, error) {
	switch fieldState(raw) {
	case "unset":
		return "unset", nil
	case "null":
		return "null", nil
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return "", fmt.Errorf("isPaying must be a boolean, got %s", jsonKind(raw))
	}
	if b {
		return "true", nil
	}
	return "false", nil
}

func canonicalPlan(raw json.RawMessage) (string, error) {
	switch fieldState(raw) {
	case "unset":
		return "unset", nil
	case "null":
		return "null", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("plan must be a string or null, got %s", jsonKind(raw))
	}
	return s, nil
}

func canonicalCurrency(raw json.RawMessage) (string, error) {
	switch fieldState(raw) {
	case "unset":
		return "unset", nil
	case "null":
		// EndUserAttributesInputSchema (SaaS packages/shared/src/schemas/
		// end-users.ts) keeps currency non-nullable, so a body carrying
		// `currency: null` always fails the server's zod parse and its
		// signature is never verified. The server's canonicalField
		// (SaaS apps/api/src/lib/sdk-attributes-signing.ts) would render
		// "null", but json.Unmarshal(null, *string) silently no-ops —
		// rendering that as an empty canonical line diverged from the
		// server on an unverifiable body. Error instead so no path
		// produces an empty currency line (issue #196).
		return "", errors.New("currency must be a string; explicit null is not accepted server-side (the field is not nullable), so omit it instead")
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("currency must be a string, got %s", jsonKind(raw))
	}
	return s, nil
}

func canonicalMRR(raw json.RawMessage) (string, error) {
	switch fieldState(raw) {
	case "unset":
		return "unset", nil
	case "null":
		return "null", nil
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return "", fmt.Errorf("mrr must be a number or null, got %s", jsonKind(raw))
	}
	return canonicalNumber(f)
}

// canonicalNumber renders a JSON number the way the server canonicalizes it:
// ECMAScript Number.prototype.toFixed(2) semantics (two fraction digits,
// rounding on the exact binary value with ties picking the larger n), then
// trailing "0"s and a trailing "." stripped ("1200.00" → "1200", "99.50" →
// "99.5"). strconv.FormatFloat is not used because it rounds binary ties to
// even where ECMAScript rounds them up (10.125 → "10.13", not "10.12").
func canonicalNumber(f float64) (string, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", errors.New("mrr must be a finite number")
	}
	if f < 0 {
		return "", errors.New("mrr must be nonnegative")
	}
	// ECMAScript Number.prototype.toFixed switches to Number::toString for
	// x >= 10^21, which prints shortest-round-trip exponential notation
	// ("1e+21"); big.Rat cannot reproduce that, so special-case it.
	if f >= 1e21 {
		return strconv.FormatFloat(f, 'e', -1, 64), nil
	}
	// Exact rational value of the float64, so rounding behaves like
	// ECMAScript's "as close as possible; ties pick the larger n".
	r := new(big.Rat).SetFloat64(f)
	r.Mul(r, big.NewRat(100, 1))
	// hundredths = floor(r + 1/2) = floor((2*num + den) / (2*den)); the
	// numerator is nonnegative, so truncated division equals floor.
	num := new(big.Int).Add(new(big.Int).Lsh(r.Num(), 1), r.Denom())
	den := new(big.Int).Lsh(r.Denom(), 1)
	hundredths, _ := new(big.Int).QuoRem(num, den, new(big.Int))
	s := hundredths.String()
	for len(s) < 3 {
		s = "0" + s
	}
	out := s[:len(s)-2] + "." + s[len(s)-2:]
	// Strip trailing zeros, then a trailing dot ("1200.00" → "1200").
	end := len(out)
	for end > 0 && out[end-1] == '0' {
		end--
	}
	if end > 0 && out[end-1] == '.' {
		end--
	}
	return out[:end], nil
}

// jsonKind names a raw JSON value's type for error messages.
func jsonKind(raw json.RawMessage) string {
	switch {
	case len(raw) == 0:
		return "absent"
	case bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`"`)):
		return "string"
	case bytes.ContainsAny(bytes.TrimSpace(raw), ".eE"):
		return "number"
	case bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`{`)):
		return "object"
	case bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`[`)):
		return "array"
	default:
		return "value"
	}
}

// DecodeSDKAttributeBody parses a raw JSON request body into the form the
// canonicalizer expects, preserving JSON.parse number semantics (decimals as
// float64) and the absent-vs-null distinction.
func DecodeSDKAttributeBody(data []byte) (map[string]json.RawMessage, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse request body JSON: %w", err)
	}
	if raw == nil {
		raw = map[string]json.RawMessage{}
	}
	return raw, nil
}

// ResolveSDKUserToken returns the token that identifies the user for this
// request: the body userToken when present, else the X-User-Token header
// fallback — the same resolution the server signs against.
func ResolveSDKUserToken(raw map[string]json.RawMessage, headerToken string) (string, error) {
	if body, ok := raw["userToken"]; ok && fieldState(body) == "value" {
		var s string
		if err := json.Unmarshal(body, &s); err != nil {
			return "", fmt.Errorf("userToken must be a string, got %s", jsonKind(body))
		}
		if s != "" {
			return s, nil
		}
	}
	if headerToken != "" {
		return headerToken, nil
	}
	return "", errors.New("a userToken is required (body userToken or the X-User-Token header)")
}

// The signed body targets EndUserAttributesInputSchema (SaaS
// packages/shared/src/schemas/end-users.ts), so these wire constraints are
// mirrored locally: a body the server can never accept must fail before a
// signature is produced and sent, with the offending field and the accepted
// form named, instead of burning a round trip on a guaranteed 400 (issue
// #196; the schema-mirror precedent is validateAppsUpdateFlags on apps
// update).
const MaxSDKAttributeMRR = 1_000_000

var (
	// currency: z.string().length(3).regex(/^[A-Za-z]{3}$/) — the anchored
	// regex implies exactly three ASCII letters, so it subsumes length(3).
	sdkCurrencyRE = regexp.MustCompile(`^[A-Za-z]{3}$`)
	// Body userToken: zod v4's z.string().uuid() (RFC 9562/4122 — version
	// nibble 1-8, variant [89abAB], plus the reserved all-zero and all-ones
	// UUIDs). The route additionally checks the resolved token against
	// UUID_RE (SaaS apps/api/src/lib/ids.ts); every zod-uuid match satisfies
	// it, and sdkResolvedTokenRE covers the X-User-Token header fallback that
	// skips the schema check server-side.
	sdkBodyTokenRE     = regexp.MustCompile(`^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}|00000000-0000-0000-0000-000000000000|ffffffff-ffff-ffff-ffff-ffffffffffff)$`)
	sdkResolvedTokenRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// ValidateSDKAttributeBody mirrors the server schema for the fields the
// canonical string signs. Unknown keys are stripped server-side and sign
// nowhere, so they stay accepted; plan and mrr may be explicitly null
// (nullable server-side) and sign as "null"; isPaying, currency, and
// userToken are not nullable and are rejected before a dead-on-arrival
// signature is produced. Type errors are re-checked here (with the same
// messages the canonicalizer produces) so rejection happens before any
// signing work.
func ValidateSDKAttributeBody(raw map[string]json.RawMessage) error {
	if v, ok := raw["isPaying"]; ok {
		switch fieldState(v) {
		case "null":
			return errors.New("invalid isPaying: explicit null is not accepted server-side (the field is not nullable); omit the key instead")
		case "value":
			var b bool
			if err := json.Unmarshal(v, &b); err != nil {
				return fmt.Errorf("isPaying must be a boolean, got %s", jsonKind(v))
			}
		}
	}
	if v, ok := raw["plan"]; ok && fieldState(v) == "value" {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return fmt.Errorf("plan must be a string or null, got %s", jsonKind(v))
		}
		// zod measures string length in Unicode code points (v4
		// codePointLength counts a surrogate pair once), so rune count —
		// not byte length — mirrors it.
		if n := utf8.RuneCountInString(s); n < 1 || n > 64 {
			return fmt.Errorf("invalid plan: must be 1-64 characters, got %d", n)
		}
	}
	if v, ok := raw["mrr"]; ok && fieldState(v) == "value" {
		var f float64
		if err := json.Unmarshal(v, &f); err != nil {
			return fmt.Errorf("mrr must be a number or null, got %s", jsonKind(v))
		}
		if f > MaxSDKAttributeMRR {
			return fmt.Errorf("invalid mrr %s: must be at most %d", bytesTrimRaw(v), int64(MaxSDKAttributeMRR))
		}
	}
	if v, ok := raw["currency"]; ok {
		switch fieldState(v) {
		case "null":
			return errors.New("invalid currency: explicit null is not accepted server-side (the field is not nullable); omit the key instead")
		case "value":
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				return fmt.Errorf("currency must be a string, got %s", jsonKind(v))
			}
			if !sdkCurrencyRE.MatchString(s) {
				return fmt.Errorf("invalid currency %q: must be a 3-letter alphabetic code (e.g. %q)", s, "usd")
			}
		}
	}
	if v, ok := raw["userToken"]; ok {
		switch fieldState(v) {
		case "null":
			return errors.New("invalid userToken: explicit null is not accepted server-side (the field is not nullable); omit the key instead")
		case "value":
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				return fmt.Errorf("userToken must be a string, got %s", jsonKind(v))
			}
			if !sdkBodyTokenRE.MatchString(s) {
				return fmt.Errorf("invalid userToken %q: must be an RFC 4122 UUID (8-4-4-4-12 hex, e.g. %q)", s, "3fa85f64-5717-4562-b3fc-2c963f66afa6")
			}
		}
	}
	return nil
}

// ValidateResolvedSDKToken mirrors the route's UUID_RE check on the resolved
// token (SaaS apps/api/src/domains/end-users/routes.ts). The body userToken
// is already schema-checked by ValidateSDKAttributeBody, so in practice only
// the X-User-Token header fallback reaches this.
func ValidateResolvedSDKToken(token string) error {
	if !sdkResolvedTokenRE.MatchString(token) {
		return fmt.Errorf("invalid userToken %q: the body userToken or --user-token (X-User-Token) value must be an RFC 4122 UUID (8-4-4-4-12 hex)", token)
	}
	return nil
}

// bytesTrimRaw renders a raw JSON scalar without surrounding whitespace for
// error messages.
func bytesTrimRaw(raw json.RawMessage) string {
	return string(bytes.TrimSpace(raw))
}
