package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// intakeLedgerFixture is the response shape the SaaS OpenAPI spec pins for
// GET /api/v1/console/workspaces/{id}/intake-ledger (issue #305): five
// required fields, no envelope.
const intakeLedgerFixture = `{"utcMonth":"2026-10-01T00:00:00.000Z","acceptedCount":1234,"cap":5000,"resetsUsed":1,"resetsRemaining":1}`

// intakeLedgerServer answers the ledger endpoint and records the exact wire
// form of the request so the tests can pin the route.
func intakeLedgerServer(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	var wire string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wire = r.Method + " " + r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(intakeLedgerFixture))
	}))
	t.Cleanup(server.Close)
	return server, &wire
}

func seedConfig(t *testing.T) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(defaultAppConfig), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return cfgPath
}

// TestBillingIntakeLedgerJSON covers the issue #305 acceptance: the command
// GETs the workspace's /intake-ledger route and --json prints exactly one
// document carrying the five ledger fields — the durable accepted count, the
// cap it is enforced against, and the per-month reset budget.
func TestBillingIntakeLedgerJSON(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	server, wire := intakeLedgerServer(t)

	out, _, err := runRootCapture(t, seedConfig(t), server.URL, "billing", "intake-ledger", "--json")
	if err != nil {
		t.Fatalf("billing intake-ledger --json: %v\n%s", err, out)
	}
	if *wire != "GET /api/v1/console/workspaces/ws_1/intake-ledger" {
		t.Errorf("wire request = %q, want GET /api/v1/console/workspaces/ws_1/intake-ledger", *wire)
	}
	assertNoTableOutput(t, out)
	doc := unmarshalOneJSON(t, out)
	want := map[string]any{
		"utcMonth":        "2026-10-01T00:00:00.000Z",
		"acceptedCount":   float64(1234),
		"cap":             float64(5000),
		"resetsUsed":      float64(1),
		"resetsRemaining": float64(1),
	}
	for key, val := range want {
		if doc[key] != val {
			t.Errorf("%s = %v, want %v:\n%s", key, doc[key], val, out)
		}
	}
	if len(doc) != len(want) {
		t.Errorf("document has %d keys, want exactly the %d ledger fields:\n%s", len(doc), len(want), out)
	}
}

// TestBillingIntakeLedgerYAML pins the same contract for -o yaml.
func TestBillingIntakeLedgerYAML(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	server, _ := intakeLedgerServer(t)

	out, _, err := runRootCapture(t, seedConfig(t), server.URL, "billing", "intake-ledger", "-o", "yaml")
	if err != nil {
		t.Fatalf("billing intake-ledger -o yaml: %v\n%s", err, out)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not a single YAML document: %v\n%s", err, out)
	}
	if doc["utcMonth"] != "2026-10-01T00:00:00.000Z" || doc["acceptedCount"] != 1234 ||
		doc["cap"] != 5000 || doc["resetsUsed"] != 1 || doc["resetsRemaining"] != 1 {
		t.Errorf("yaml document = %v, want the five ledger fields", doc)
	}
}

// TestBillingIntakeLedgerTable pins the human-mode render: the durable count
// is shown against its cap and the reset budget is visible, the two values
// `billing show` cannot disclose (issue #305).
func TestBillingIntakeLedgerTable(t *testing.T) {
	t.Setenv("CUPTHREAD_TOKEN", "cpt_test")

	server, _ := intakeLedgerServer(t)

	out, _, err := runRootCapture(t, seedConfig(t), server.URL, "billing", "intake-ledger")
	if err != nil {
		t.Fatalf("billing intake-ledger: %v\n%s", err, out)
	}
	for _, want := range []string{
		"UTC month",
		"2026-10-01T00:00:00.000Z",
		"Accepted intake",
		"1234 / 5000",
		"Resets used this month",
		"Resets remaining this month",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}
