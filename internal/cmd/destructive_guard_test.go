package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestPermanentDeleteCommandsDeclareYes is the class-wide tripwire issue
// #265 asked for: any command whose help promises a permanent effect
// (Short/Long says "permanently") must declare the --yes flag, so the next
// hard-delete command cannot ship without the #80 confirmation contract the
// way `comments moderation delete` did. The RunE-side wiring is pinned per
// command by the guard tests; walking the registered tree catches a new
// command even when only its help text exists.
func TestPermanentDeleteCommandsDeclareYes(t *testing.T) {
	root := newRootCmd()
	var offenders []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		text := strings.ToLower(c.Short + "\n" + c.Long)
		if strings.Contains(text, "permanently") && c.Flags().Lookup("yes") == nil {
			offenders = append(offenders, c.CommandPath())
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	if len(offenders) > 0 {
		t.Errorf("commands promising a permanent effect without the --yes guard (issue #265): %v", offenders)
	}
}

// TestYesFlagImpliesConfirmDestructive keeps the --yes flag and the guard
// call from drifting apart (issue #265): the guard reads the flag by name,
// so a confirmDestructive caller that never registers --yes silently ignores
// it (non-interactive callers can never confirm), and a --yes flag without a
// confirmDestructive call is a no-op that only pretends to guard. Both scans
// are function-level over the cmd package sources: every constructor that
// registers a boolean "yes" flag must call confirmDestructive, and every
// confirmDestructive caller must register the flag.
func TestYesFlagImpliesConfirmDestructive(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read command sources: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(".", e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, e.Name(), src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			var hasYesFlag, hasGuard bool
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.Ident); ok && sel.Name == "confirmDestructive" {
					hasGuard = true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					switch sel.Sel.Name {
					case "Bool", "BoolP":
						hasYesFlag = hasYesFlag || yesFlagName(call, 0)
					case "BoolVar", "BoolVarP":
						hasYesFlag = hasYesFlag || yesFlagName(call, 1)
					}
				}
				return true
			})
			if hasYesFlag && !hasGuard {
				t.Errorf("%s:%d declares a --yes flag but never calls confirmDestructive (issue #265)",
					e.Name(), fset.Position(fn.Pos()).Line)
			}
			if hasGuard && !hasYesFlag {
				t.Errorf("%s:%d calls confirmDestructive but never registers the --yes flag it reads (issue #265)",
					e.Name(), fset.Position(fn.Pos()).Line)
			}
		}
	}
}

// yesFlagName reports whether the bool-flag registration call carries the
// literal name "yes" at the given argument index (0 for Bool/BoolP, 1 for
// BoolVar/BoolVarP). Non-literal names can't be checked and don't match.
func yesFlagName(call *ast.CallExpr, nameArg int) bool {
	if nameArg >= len(call.Args) {
		return false
	}
	lit, ok := call.Args[nameArg].(*ast.BasicLit)
	return ok && lit.Kind == token.STRING && strings.Trim(lit.Value, "`\"") == "yes"
}
