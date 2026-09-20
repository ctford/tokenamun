package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// The help text is the only documentation an agent driving this tool has, so
// the thing worth testing about it is that it cannot lie: every command it
// offers must dispatch, every flag it attributes to a command must exist, and
// neither can be added without the other. Prose is not asserted on -- these
// check the claims, not the wording.

// parsedMain reads the command-line source, which is where the switch and the
// flag registrations live. Reading the source rather than the running program
// because both are local to run(), and extracting them to reach from a test
// would restructure the command to suit the test.
func parsedMain(t *testing.T) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// dispatched lists the command names the switch in run() answers to.
func dispatched(t *testing.T) map[string]bool {
	names := map[string]bool{}
	ast.Inspect(parsedMain(t), func(n ast.Node) bool {
		sw, ok := n.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		if tag, ok := sw.Tag.(*ast.Ident); !ok || tag.Name != "cmd" {
			return true
		}
		for _, stmt := range sw.Body.List {
			clause, ok := stmt.(*ast.CaseClause)
			if !ok {
				continue
			}
			for _, expr := range clause.List {
				if lit, ok := expr.(*ast.BasicLit); ok {
					names[strings.Trim(lit.Value, `"`)] = true
				}
			}
		}
		return false
	})
	if len(names) == 0 {
		t.Fatal("found no command switch in main.go")
	}
	return names
}

// registered lists the flag names run() defines.
func registered(t *testing.T) map[string]bool {
	names := map[string]bool{}
	ast.Inspect(parsedMain(t), func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "fs" {
			return true
		}
		// Var takes the value first and the name second; the rest name first.
		arg := 0
		if sel.Sel.Name == "Var" {
			arg = 1
		}
		if arg >= len(call.Args) {
			return true
		}
		if lit, ok := call.Args[arg].(*ast.BasicLit); ok {
			names[strings.Trim(lit.Value, `"`)] = true
		}
		return true
	})
	if len(names) == 0 {
		t.Fatal("found no flag registrations in main.go")
	}
	return names
}

func TestEveryDocumentedCommandDispatches(t *testing.T) {
	// A command name is a claim. One that the help offers and the switch does
	// not answer sends an agent to `unknown command`, which reads as the tool
	// being broken rather than the help being stale.
	answers := dispatched(t)
	for _, c := range commands {
		for _, name := range append([]string{c.name}, c.also...) {
			if !answers[name] {
				t.Errorf("help offers %q, which run() does not dispatch", name)
			}
		}
	}
}

func TestEveryDispatchedCommandIsDocumented(t *testing.T) {
	// The other direction, which is the one that rots: a command added to the
	// switch and not to the table is invisible to anything that reads help.
	for name := range dispatched(t) {
		switch name {
		case "help", "-h", "--help":
			continue
		}
		if _, ok := lookup(name); !ok {
			t.Errorf("run() dispatches %q, which `tokenamun help` never mentions", name)
		}
	}
}

func TestEveryDocumentedFlagExists(t *testing.T) {
	// Offering a flag the binary does not define is worse than omitting it:
	// the caller gets "flag provided but not defined" from the tool that just
	// recommended it.
	defined := registered(t)
	for name := range flagHelp {
		if !defined[name] {
			t.Errorf("help documents --%s, which run() does not define", name)
		}
	}
	for _, c := range commands {
		for _, f := range append(append([]string{}, c.flags...), c.commonFlags()...) {
			if _, ok := flagHelp[f]; !ok {
				t.Errorf("%s lists --%s, which has no description", c.name, f)
			}
		}
	}
}

func TestEveryFlagIsDocumentedSomewhere(t *testing.T) {
	for name := range registered(t) {
		if _, ok := flagHelp[name]; !ok {
			t.Errorf("--%s is defined but appears in no help page", name)
		}
	}
}

func TestACommandPageOffersOnlyTheFlagsThatCommandTakes(t *testing.T) {
	// The reason these pages exist. The flag package answers -h with every
	// flag in the binary, so `tokenamun tree -h` recommended --max-complexity,
	// which tree ignores, and --prices, which three commands accept and the
	// rest refuse outright.
	page, ok := helpFor("tree")
	if !ok {
		t.Fatal("tree has no page")
	}
	for _, absent := range []string{"--max-complexity", "--cost", "--sort", "--title"} {
		if strings.Contains(page, absent) {
			t.Errorf("tree's page offers %s, which tree does not take", absent)
		}
	}
	for _, present := range []string{"--at", "--mode", "--prices", "--json"} {
		if !strings.Contains(page, present) {
			t.Errorf("tree's page omits %s", present)
		}
	}
}

func TestPricesIsOfferedExactlyWhereItIsHonoured(t *testing.T) {
	// pricesApplies refuses the flag on every other command, and a help page
	// that offered it anyway would be recommending an error.
	for _, c := range commands {
		offered := false
		for _, f := range c.flags {
			offered = offered || f == "prices"
		}
		honoured := pricesApplies(c.name, true) == nil
		if offered != honoured {
			t.Errorf("%s: help offers --prices=%v, the command honours it=%v",
				c.name, offered, honoured)
		}
	}
}

func TestTheAllSelectorIsOfferedExactlyWhereItComposes(t *testing.T) {
	// all_test.go asserts which commands accept the selector. This asserts
	// that the help and the refusal message both read it from the same table.
	takers := map[string]bool{}
	for _, name := range selectAllTakers() {
		takers[name] = true
	}
	for _, name := range []string{"tree", "profile", "cache", "carry", "report", "optimise"} {
		if !takers[name] {
			t.Errorf("%s takes `all` but the help does not say so", name)
		}
	}
	for _, name := range []string{"retrieval", "hotspots", "compare", "scan"} {
		if takers[name] {
			t.Errorf("%s refuses `all` but the help offers it", name)
		}
	}
}

func TestHelpWrapsToAReadableWidth(t *testing.T) {
	// Output is terse (AGENTS.md); a line that wraps in the terminal is a line
	// whose second half looks like a separate entry.
	pages := []string{usage()}
	for _, c := range commands {
		page, _ := helpFor(c.name)
		pages = append(pages, page)
	}
	for _, page := range pages {
		for _, line := range strings.Split(page, "\n") {
			if len(line) > helpWidth {
				t.Errorf("line of %d characters: %q", len(line), line)
			}
		}
	}
}

func TestHelpForACommandPrintsThatCommandAlone(t *testing.T) {
	out, err := capture(t, "help", "carry")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "tokenamun carry") {
		t.Errorf("a page should lead with its own command, got %q", out)
	}
	if strings.Contains(out, "tokenamun retrieval") {
		t.Errorf("a page should not list the other commands, got %q", out)
	}
}

func TestHelpForAnUnknownCommandNamesTheRealOnes(t *testing.T) {
	_, err := capture(t, "help", "wat")
	if err == nil {
		t.Fatal("an unknown command must be an error")
	}
	if !strings.Contains(err.Error(), "sessions") {
		t.Errorf("the error should list the commands: %v", err)
	}
}

func TestDashHAfterACommandAnswersWithThatCommandsPage(t *testing.T) {
	out, err := capture(t, "tree", "-h")
	if err != nil {
		t.Fatalf("-h should not be an error: %v", err)
	}
	if !strings.HasPrefix(out, "tokenamun tree") {
		t.Errorf("-h should answer with the command's page, got %q", out)
	}
}

func TestAMisspelledFlagPointsAtTheCommandsPage(t *testing.T) {
	_, err := capture(t, "tree", "--nope")
	if err == nil {
		t.Fatal("an undefined flag must be an error")
	}
	if !strings.Contains(err.Error(), "tokenamun help tree") {
		t.Errorf("the error should say where the real flags are: %v", err)
	}
}
