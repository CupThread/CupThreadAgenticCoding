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
		"api", "apps", "auth", "billing", "changelog", "columns", "comments", "features",
		"imports", "inbox", "integrations", "me", "notifications", "search",
		"skills", "status", "users", "versions", "workspaces",
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
		"comments":      {"list", "create"},
		"columns":       {"list", "create", "update", "delete"},
		"versions":      {"list", "create", "update", "delete"},
		"changelog":     {"list", "create", "update", "delete", "publish", "unpublish"},
		"imports":       {"list", "history", "create", "get", "cancel", "rerun"},
		"integrations":  {"status", "github", "linear", "notion", "slack"},
		"notifications": {"list", "read", "read-all", "prefs"},
		"billing":       {"show", "checkout", "portal", "addons"},
		"users":         {"profile"},
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

// TestOutputFlagParsing exercises the global --output/--json resolution in
// PersistentPreRunE, including the shorthand and invalid values.
func TestOutputFlagParsing(t *testing.T) {
	cases := []struct {
		args    []string
		wantErr bool
	}{
		{args: []string{"skills", "list", "--json"}},
		{args: []string{"skills", "list", "-o", "json"}},
		{args: []string{"skills", "list", "-o", "yaml"}},
		{args: []string{"skills", "list"}},
		{args: []string{"skills", "list", "-o", "bogus"}, wantErr: true},
	}
	for _, tc := range cases {
		root := newRootCmd()
		root.SetOut(nil)
		root.SetArgs(tc.args)
		err := root.Execute()
		if tc.wantErr && err == nil {
			t.Errorf("args %v: expected error, got nil", tc.args)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("args %v: unexpected error %v", tc.args, err)
		}
		// The writer format must match the requested output.
		if A != nil && !tc.wantErr {
			switch {
			case contains(tc.args, "--json"), contains(tc.args, "json"):
				if A.out.Format != output.FormatJSON {
					t.Errorf("args %v: format = %s, want json", tc.args, A.out.Format)
				}
			case contains(tc.args, "yaml"):
				if A.out.Format != output.FormatYAML {
					t.Errorf("args %v: format = %s, want yaml", tc.args, A.out.Format)
				}
			}
		}
	}
}

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// TestOutputWiring keeps the package honest about imports used in tests above.
func TestOutputWiring(t *testing.T) {
	var buf bytes.Buffer
	w := output.New(&buf, output.FormatTable)
	w.Printf("ok")
	if buf.String() != "ok\n" {
		t.Fatalf("unexpected writer output %q", buf.String())
	}
}
