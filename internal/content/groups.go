package content

import "strings"

// commandGroups group command-line tools by what they are.
//
// These are hardcoded deliberately, and the line is tool *identity* rather
// than tool *role*. git is version control in every codebase; kubectl is
// orchestration in every codebase; sed is a POSIX text tool and has been for
// forty years. Those facts do not vary by repository, so encoding them here
// costs nothing and saves every user declaring them.
//
// What is NOT encoded here is role: whether python3 is this project's build
// system or incidental glue, whether mise or make is "the" task runner,
// whether a script under ./scripts matters. Those vary per repository, and
// guessing at them in library code is how a taxonomy starts lying about
// somebody else's project.
//
// A tool that is not listed stays at the top level of CLI output rather than
// being swept into a catch-all, so an unrecognised tool is visible as itself.
var commandGroups = map[string]string{}

// groupMembers is the source of truth; commandGroups is its inverse, built
// once at startup.
var groupMembers = map[string][]string{
	"version control": {
		"git", "hg", "svn", "jj", "bzr", "gh", "glab",
	},
	"standard tools": {
		// POSIX and near-POSIX text, file and search utilities.
		"grep", "rg", "ag", "ack", "find", "fd", "ls", "tree", "du", "df",
		"wc", "sort", "uniq", "cut", "tr", "xargs", "stat", "file", "diff",
		"basename", "dirname", "readlink", "realpath", "which", "env",
	},
	"language toolchains": {
		"go", "cargo", "npm", "pnpm", "yarn", "pip", "pip3", "poetry", "uv",
		"bundle", "mvn", "gradle", "dotnet", "tsc", "swift", "mix", "composer",
	},
	"interpreters": {
		"python", "python3", "node", "ruby", "perl", "php", "deno", "bun",
		"osascript", "awk",
	},
	"containers and orchestration": {
		"docker", "podman", "kubectl", "helm", "nerdctl", "skaffold", "minikube",
	},
	"cloud and infrastructure": {
		"aws", "gcloud", "az", "terraform", "pulumi", "ansible", "doctl", "flyctl",
	},
	"task runners": {
		"make", "just", "mise", "task", "rake", "invoke", "nx", "turbo",
	},
	"network": {
		"curl", "wget", "ssh", "scp", "rsync", "nc", "dig", "host",
	},
	"linters and formatters": {
		"golangci-lint", "eslint", "prettier", "ruff", "black", "clippy",
		"shellcheck", "hadolint", "sqlfluff", "vale",
	},
}

func init() {
	for group, members := range groupMembers {
		for _, m := range members {
			commandGroups[m] = group
		}
	}
}

// CommandGroup names the kind of tool a binary is, or "" when it is not a
// tool this package recognises.
func CommandGroup(binary string) string {
	return commandGroups[strings.ToLower(binary)]
}

// LooksLikeCommand reports whether a token is plausibly a command name rather
// than a filename or fragment that survived an unparsed heredoc.
//
// Without this, output from a multi-line command whose continuation happened
// to start with a path was filed under a "binary" called
// s1-tail-unserviceable.json, which is real output under a nonsense name.
func LooksLikeCommand(binary string) bool {
	if binary == "" {
		return false
	}
	if strings.ContainsAny(binary, "/.=$\"'") {
		return false
	}
	if binary[0] >= '0' && binary[0] <= '9' {
		return false
	}
	return true
}
