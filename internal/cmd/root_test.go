package cmd

import (
	"bytes"
	"testing"

	"github.com/CupThread/CupThreadAgenticCoding/internal/output"
	"github.com/spf13/cobra"
)

// TestCommandTreeComplete guards against a command group being dropped from
// the root registration.
func TestCommandTreeComplete(t *testing.T) {
	root := newRootCmd()
	want := []string{
		"api", "apps", "auth", "billing", "changelog", "columns", "features",
		"imports", "inbox", "integrations", "me", "notifications", "search",
		"skills", "status", "versions", "workspaces",
	}
	got := map[string]bool{}
	for _, sub := range root.Commands() {
		got[sub.Name()] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("root is missing the %q command", name)
		}
	}
}

func TestSubcommandTreesComplete(t *testing.T) {
	cases := map[string][]string{
		"auth":          {"login", "logout", "status"},
		"workspaces":    {"list", "create", "use", "members", "invitations"},
		"apps":          {"list", "create", "get", "update", "use", "settings"},
		"inbox":         {"list", "priority", "retry", "deliveries"},
		"features":      {"list", "get", "create", "update", "approve", "delete", "forward"},
		"columns":       {"list", "create", "update", "delete"},
		"versions":      {"list", "create", "update", "delete"},
		"changelog":     {"list", "create", "update", "delete", "publish", "unpublish"},
		"imports":       {"list", "history", "create", "get", "cancel", "rerun"},
		"integrations":  {"status", "github", "linear", "notion", "slack"},
		"notifications": {"list", "read", "read-all", "prefs"},
		"billing":       {"show", "checkout", "portal", "addons"},
		"api":           {"request"},
		"skills":        {"list", "link"},
	}
	for parentName, wantSubs := range cases {
		var parent *cobra.Command
		root := newRootCmd()
		for _, sub := range root.Commands() {
			if sub.Name() == parentName {
				parent = sub
			}
		}
		if parent == nil {
			t.Fatalf("missing parent command %q", parentName)
		}
		got := map[string]bool{}
		for _, sub := range parent.Commands() {
			got[sub.Name()] = true
		}
		for _, name := range wantSubs {
			if !got[name] {
				t.Errorf("%s is missing the %q subcommand", parentName, name)
			}
		}
	}
}

// TestOutputWiring keeps the package honest about imports used in tests above.
func TestOutputWiring(t *testing.T) {
	var buf bytes.Buffer
	w := output.New(&buf, false)
	w.Printf("ok")
	if buf.String() != "ok\n" {
		t.Fatalf("unexpected writer output %q", buf.String())
	}
}
