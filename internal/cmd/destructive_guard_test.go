package cmd

import (
	"fmt"
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
// confirmDestructive call is a no-op that only pretends to guard. The scan
// pairs them per cobra.Command literal — a constructor that builds several
// commands must not have one command's flag registration vouch for another's
// guard (CodeRabbit review of #284) — by tracking which variable each
// literal is assigned to and matching that variable's .Flags() calls.
func TestYesFlagImpliesConfirmDestructive(t *testing.T) {
	violations, err := scanYesGuardPairing(".")
	if err != nil {
		t.Fatalf("scan command sources: %v", err)
	}
	for _, v := range violations {
		t.Error(v)
	}
}

// TestYesGuardScannerCatchesPlantedViolations keeps the pairing scanner
// itself from rotting: a planted constructor whose command declares --yes
// without a guard, and one whose RunE calls confirmDestructive without a
// --yes flag, are both flagged, the fully paired neighbor stays silent, and
// one constructor building two commands (the CodeRabbit review of #284
// scenario) cannot make either command vouch for the other.
func TestYesGuardScannerCatchesPlantedViolations(t *testing.T) {
	dir := t.TempDir()
	src := `package cmd

import "github.com/spf13/cobra"

func plantedA() *cobra.Command {
	cmd := &cobra.Command{
		Use: "a",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
	cmd.Flags().BoolP("yes", "y", false, "")
	return cmd
}

func plantedB() *cobra.Command {
	cmd := &cobra.Command{
		Use: "b",
		RunE: func(cmd *cobra.Command, args []string) error {
			return confirmDestructive(cmd, "boom")
		},
	}
	return cmd
}

func plantedC() *cobra.Command {
	cmd := &cobra.Command{
		Use: "c",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := confirmDestructive(cmd, "ok"); err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().BoolP("yes", "y", false, "")
	return cmd
}

func plantedD() (*cobra.Command, *cobra.Command) {
	flagged := &cobra.Command{
		Use: "d1",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
	flagged.Flags().BoolP("yes", "y", false, "")
	guarded := &cobra.Command{
		Use: "d2",
		RunE: func(cmd *cobra.Command, args []string) error {
			return confirmDestructive(cmd, "split")
		},
	}
	return flagged, guarded
}
`
	if err := os.WriteFile(filepath.Join(dir, "planted.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write planted source: %v", err)
	}
	violations, err := scanYesGuardPairing(dir)
	if err != nil {
		t.Fatalf("scan planted dir: %v", err)
	}
	if len(violations) != 4 {
		t.Fatalf("violations = %v, want exactly the four unpaired commands", violations)
	}
	for _, want := range []string{
		"planted.go:6 declares a --yes flag but never calls confirmDestructive",
		"planted.go:17 calls confirmDestructive but never registers the --yes flag it reads",
		"planted.go:41 declares a --yes flag but never calls confirmDestructive",
		"planted.go:48 calls confirmDestructive but never registers the --yes flag it reads",
	} {
		found := false
		for _, v := range violations {
			if strings.Contains(v, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("violations missing %q:\n%v", want, violations)
		}
	}
}

// scanYesGuardPairing scans dir's non-test Go sources and returns one
// violation per cobra.Command literal whose --yes flag registration and
// confirmDestructive guard call do not pair up. A command's guard must be an
// inline RunE closure for the scan to see it (true for every guarded
// command today); RunE delegated to a named function is invisible here and
// is caught by the per-command behavioral guard tests instead.
func scanYesGuardPairing(dir string) ([]string, error) {
	var violations []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		fname := e.Name()
		src, err := os.ReadFile(filepath.Join(dir, fname))
		if err != nil {
			return nil, err
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, fname, src, 0)
		if err != nil {
			return nil, err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			// Map each command variable to the &cobra.Command literal it is
			// assigned, so cmd.Flags() calls attach to the command they
			// configure rather than to the whole constructor. The literal is
			// the operand of the & address-of UnaryExpr, not the assignment's
			// direct RHS.
			vars := map[*ast.CompositeLit][]string{}
			ast.Inspect(fn, func(n ast.Node) bool {
				assign, ok := n.(*ast.AssignStmt)
				if !ok || len(assign.Rhs) != 1 {
					return true
				}
				rhs := assign.Rhs[0]
				if un, ok := rhs.(*ast.UnaryExpr); ok && un.Op == token.AND {
					rhs = un.X
				}
				lit, ok := rhs.(*ast.CompositeLit)
				if !ok || !isCobraCommandLit(lit) {
					return true
				}
				for _, lhs := range assign.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						vars[lit] = append(vars[lit], id.Name)
					}
				}
				return true
			})
			ast.Inspect(fn, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok || !isCobraCommandLit(lit) {
					return true
				}
				guard := runECallsConfirmDestructive(lit)
				flag := false
				for _, name := range vars[lit] {
					if registersYesFlag(fn, name) {
						flag = true
					}
				}
				pos := fset.Position(lit.Pos())
				switch {
				case flag && !guard:
					violations = append(violations, fmt.Sprintf(
						"%s:%d declares a --yes flag but never calls confirmDestructive (issue #265)",
						fname, pos.Line))
				case guard && !flag:
					violations = append(violations, fmt.Sprintf(
						"%s:%d calls confirmDestructive but never registers the --yes flag it reads (issue #265)",
						fname, pos.Line))
				}
				return true
			})
		}
	}
	return violations, nil
}

// isCobraCommandLit reports whether lit is a &cobra.Command{…} composite
// literal.
func isCobraCommandLit(lit *ast.CompositeLit) bool {
	sel, ok := lit.Type.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Command"
}

// runECallsConfirmDestructive reports whether lit's RunE field is an inline
// closure that calls confirmDestructive somewhere in its body.
func runECallsConfirmDestructive(lit *ast.CompositeLit) bool {
	found := false
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "RunE" {
			continue
		}
		fn, ok := kv.Value.(*ast.FuncLit)
		if !ok {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "confirmDestructive" {
					found = true
				}
			}
			return true
		})
	}
	return found
}

// registersYesFlag reports whether fn contains a
// <var>.Flags().Bool*/BoolVar*/BoolVarP(…) call naming the flag "yes". The
// chain is two calls deep — `cmd.Flags()` is itself a CallExpr whose Fun is
// the Flags selector, and the outer Fun selects the Bool* method on its
// result — so the receiver ident is three selectors down (the name literal
// is argument 0 of Bool/BoolP, argument 1 of BoolVar/BoolVarP). Non-literal
// names cannot be checked and do not match.
func registersYesFlag(fn *ast.FuncDecl, varName string) bool {
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		var nameArg int
		switch sel.Sel.Name {
		case "Bool", "BoolP":
			nameArg = 0
		case "BoolVar", "BoolVarP":
			nameArg = 1
		default:
			return true
		}
		flagsCall, ok := sel.X.(*ast.CallExpr)
		if !ok {
			return true
		}
		flagsSel, ok := flagsCall.Fun.(*ast.SelectorExpr)
		if !ok || flagsSel.Sel.Name != "Flags" {
			return true
		}
		recv, ok := flagsSel.X.(*ast.Ident)
		if !ok || recv.Name != varName {
			return true
		}
		if nameArg >= len(call.Args) {
			return true
		}
		if lit, ok := call.Args[nameArg].(*ast.BasicLit); ok &&
			lit.Kind == token.STRING && strings.Trim(lit.Value, "`\"") == "yes" {
			found = true
		}
		return true
	})
	return found
}
