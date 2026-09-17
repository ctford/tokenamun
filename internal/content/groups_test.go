package content

import "testing"

func TestTheTaxonomyIsInternallyConsistent(t *testing.T) {
	// Three invariants that the audit behind this test each caught a real
	// violation of. They are cheap to check and the failure mode is a tool
	// quietly sitting in the wrong place, which nobody notices.

	// 1. Every binary whose output is routed to file content is also a tool
	// with a group. `bat` was not, so it appeared at the top level of CLI
	// output while cat, head and sed were under standard tools.
	for binary := range fileReadingBinaries {
		if CommandGroup(binary) == "" {
			t.Errorf("%q prints file contents but has no tool group", binary)
		}
	}

	// 2. Every tool that opens up by its second word is a tool we recognise.
	// Fourteen were not -- brew, apt, systemctl and the rest -- so they were
	// splitting into subcommands while sitting ungrouped.
	for binary := range hasSubcommands {
		if CommandGroup(binary) == "" {
			t.Errorf("%q opens up by subcommand but has no tool group", binary)
		}
	}

	// 3. A tool whose second word is a target the repository defines must not
	// open up by it: that would file output under somebody's own vocabulary,
	// which is the line the taxonomy exists to hold. Listed by tool rather
	// than by group, because the group does not settle it -- `mise run` and
	// `bazel build` are the tool's own verbs, while `make build` and `just
	// build` are the repository's.
	for _, runner := range []string{
		"make", "just", "task", "rake", "invoke", "doit", "mage",
		"gulp", "grunt", "moon", "lage", "buck", "buck2", "pants",
	} {
		if hasSubcommands[runner] {
			t.Errorf("%q takes a target the repository defines, so its second "+
				"word must not become a level", runner)
		}
		if CommandGroup(runner) == "" {
			t.Errorf("%q should still be a recognised tool", runner)
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
