package content

import (
	"sort"
	"testing"
)

func TestTheTaxonomyIsInternallyConsistent(t *testing.T) {
	// Three invariants that the audit behind this test each caught a real
	// violation of. They are cheap to check and the failure mode is a tool
	// quietly sitting in the wrong place, which nobody notices.

	// 1. The binaries whose output is routed to file content agree with each
	// other about grouping. `bat` once had no group while cat, head and sed
	// did, so it appeared at the top level of cli output and they did not --
	// same job, two places. All or none; which of the two is a separate
	// judgement, and it is currently none.
	var grouped, ungrouped []string
	for binary := range fileReadingBinaries {
		if CommandGroup(binary) == "" {
			ungrouped = append(ungrouped, binary)
		} else {
			grouped = append(grouped, binary)
		}
	}
	if len(grouped) > 0 && len(ungrouped) > 0 {
		sort.Strings(grouped)
		sort.Strings(ungrouped)
		t.Errorf("file-printing binaries disagree about grouping: %v are grouped, "+
			"%v are not, so the same job appears in two places", grouped, ungrouped)
	}

	// 2. Every tool that opens up by its second word is a tool we recognise.
	// Fourteen were not -- brew, apt, systemctl and the rest -- so they were
	// splitting into subcommands while sitting ungrouped.
	for binary := range hasSubcommands {
		if CommandGroup(binary) == "" {
			t.Errorf("%q opens up by subcommand but has no tool group", binary)
		}
	}

	// 3. A target runner opens up by its target, the same as a subcommand.
	// `make test` against `make build` is the split a reader wants, and the
	// taxonomy's rule is against hardcoding a role rather than against
	// reading one off an observed command line.
	for _, runner := range []string{"make", "just", "task", "rake", "bazel"} {
		if !hasSubcommands[runner] {
			t.Errorf("%q should open up by its target", runner)
		}
		if CommandGroup(runner) != "task runners" {
			t.Errorf("%q should be a task runner", runner)
		}
	}
}

func TestEveryEcosystemHasSomethingInTheTaxonomy(t *testing.T) {
	// The taxonomy was built while profiling a TypeScript repo and a Go one,
	// which is exactly how a tool ends up only understanding two ecosystems.
	// One representative command per language family, so a gap shows up as a
	// failure rather than as an ungrouped row in somebody else's report.
	for _, tc := range []struct{ ecosystem, cmd string }{
		{"Go", "go"}, {"Rust", "cargo"}, {"Node", "pnpm"}, {"TypeScript", "tsc"},
		{"Python", "uv"}, {"Ruby", "bundle"}, {"PHP", "composer"},
		{"Java", "mvn"}, {"Kotlin", "gradle"}, {"Scala", "sbt"},
		{"Clojure", "lein"}, {"Elixir", "mix"}, {"Erlang", "rebar3"},
		{"Haskell", "cabal"}, {"OCaml", "dune"}, {"Swift", "swift"},
		{"Dart", "dart"}, {"C#", "dotnet"}, {"C++", "cmake"}, {"C", "cc"},
		{"Zig", "zig"}, {"Nim", "nimble"}, {"Crystal", "shards"},
		{"Elm", "elm"}, {"Perl", "cpanm"},
		{"R", "rscript"}, {"Julia", "julia"}, {"Lua", "lua"},
		{"PowerShell", "pwsh"}, {"Racket", "racket"},
	} {
		t.Run(tc.ecosystem, func(t *testing.T) {
			if CommandGroup(tc.cmd) == "" {
				t.Errorf("%s: %q is not in the taxonomy, so it would appear "+
					"ungrouped in a %s project's report", tc.ecosystem, tc.cmd, tc.ecosystem)
			}
		})
	}
}
