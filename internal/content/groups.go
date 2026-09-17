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
// A tool that is not listed stays at the top level of cli output rather than
// being swept into a catch-all, so an unrecognised tool is visible as itself.
//
// Wrappers such as env and xargs are deliberately absent: they are stripped
// before a command is identified, since `xargs grep foo` is a grep, so any
// group membership for them would be unreachable.
var commandGroups = map[string]string{}

// groupMembers is the source of truth; commandGroups is its inverse, built
// once at startup.
var groupMembers = map[string][]string{
	"version control": {
		"git", "hg", "svn", "jj", "bzr", "fossil", "cvs", "p4",
		"gh", "glab", "tea",
	},
	"standard unix tools": {
		// POSIX and near-POSIX text, file and search utilities, plus the
		// modern replacements that do the same job.
		"grep", "rg", "ag", "ack", "find", "fd", "ls", "tree", "du", "df",
		"wc", "sort", "uniq", "cut", "tr", "stat", "file", "diff", "comm",
		"join", "paste", "split", "rev", "column", "fold", "expand", "tee",
		"xxd", "od", "hexdump", "strings", "basename", "dirname", "readlink",
		"realpath", "which", "sd", "gron", "dasel", "delta", "choose",
		// These print file contents, so their output is routed to file
		// content when they actually read a file. Group membership is a
		// separate question: they are POSIX text tools either way, and when
		// used as pipeline filters they belong here.
		"cat", "bat", "head", "tail", "sed", "awk", "nl", "jq", "yq",
		"less", "more",
	},
	"language toolchains": {
		// Project-level build and dependency tools, by language. Compilers
		// belong here too: cc is the Go toolchain's opposite number, not a
		// different kind of thing.
		"go", "gofmt", "cargo", "rustc", "zig",
		"npm", "pnpm", "yarn", "tsc", "elm",
		"pip", "pip3", "pipenv", "poetry", "uv", "pdm", "hatch", "conda",
		"mamba", "tox",
		"bundle", "gem", "composer",
		"mvn", "gradle", "ant", "sbt", "mill", "lein", "javac", "kotlinc",
		"scalac", "kotlinc-jvm",
		"dotnet", "nuget", "msbuild",
		"cabal", "stack", "rebar3", "mix",
		"dart", "flutter", "pub",
		"swift", "swiftc", "xcodebuild",
		"cmake", "meson", "ninja", "gcc", "g++", "clang", "clang++", "cc",
		"dune", "opam", "nimble", "shards", "cpanm", "ghc",
	},
	"interpreters": {
		"python", "python2", "python3", "node", "ts-node", "tsx",
		"ruby", "perl", "php", "deno", "bun",
		"lua", "luajit", "julia", "elixir", "iex", "erl", "escript", "ghci",
		"scala", "kotlin", "groovy", "tclsh", "rscript", "r",
		"pwsh", "powershell", "osascript", "bb", "babashka", "clojure", "clj",
		"guile", "sbcl", "racket", "janet",
	},
	"containers and orchestration": {
		"docker", "docker-compose", "podman", "nerdctl", "buildah", "crictl",
		"kubectl", "helm", "helmfile", "kustomize", "oc", "k9s", "kubectx",
		"kubens", "argocd", "flux", "skaffold", "tilt",
		"minikube", "kind", "k3d", "colima", "lima",
	},
	"cloud and infrastructure": {
		"aws", "gcloud", "az", "doctl", "flyctl", "heroku", "eb",
		"vercel", "netlify", "railway", "wrangler", "supabase", "firebase",
		"terraform", "tofu", "opentofu", "pulumi", "packer", "vagrant",
		"ansible", "salt", "chef", "puppet",
		"consul", "vault", "nomad",
		"cdk", "sam", "serverless", "sls",
	},
	"package and version managers": {
		// System-level, as opposed to the project-level dependency tools in
		// language toolchains: brew installs a compiler, cargo installs a
		// crate. The distinction is whose dependencies they are.
		"brew", "apt", "apt-get", "dnf", "yum", "pacman", "apk", "zypper",
		"port", "choco", "scoop", "winget", "snap", "flatpak",
		"nix", "nix-env", "nix-shell", "nix-build", "pipx",
		"asdf", "nvm", "fnm", "volta", "rustup", "pyenv", "rbenv", "jenv",
		"corepack",
	},
	"task runners": {
		// Their second word is a target the repository defines, which is why
		// none of them opens up by it: see hasSubcommands.
		"make", "just", "mise", "task", "rake", "invoke", "doit", "mage",
		"nx", "turbo", "moon", "lage", "gulp", "grunt",
		"bazel", "buck", "buck2", "pants", "earthly",
	},
	"network": {
		"curl", "wget", "aria2c", "http", "httpie", "xh",
		"ssh", "scp", "sftp", "rsync", "ftp", "telnet", "nc",
		"dig", "host", "nslookup", "ping", "traceroute", "mtr",
	},
	"linters and formatters": {
		"golangci-lint", "staticcheck", "goimports", "govulncheck",
		"eslint", "oxlint", "biome", "prettier", "stylelint", "dprint",
		"dependency-cruiser",
		"ruff", "black", "isort", "flake8", "pylint", "mypy", "pyright",
		"clippy", "rustfmt",
		"rubocop", "standardrb", "erb_lint",
		"checkstyle", "spotbugs", "ktlint", "detekt",
		"phpcs", "php-cs-fixer", "phpstan", "psalm",
		"credo", "dialyzer", "swiftlint",
		"clang-format", "clang-tidy", "cppcheck",
		"shellcheck", "shfmt", "hadolint", "tflint", "yamllint",
		"markdownlint", "sqlfluff", "vale", "luacheck",
	},
	"security scanners": {
		"gitleaks", "trufflehog", "semgrep", "snyk", "trivy", "grype", "syft",
		"bandit", "gosec", "checkov", "tfsec", "osv-scanner",
		"openssl", "gpg", "age", "sops", "ssh-keygen",
	},
	"system and process": {
		"systemctl", "launchctl", "service", "journalctl", "dmesg",
		"ps", "top", "htop", "lsof", "kill", "pkill", "pgrep",
		"uname", "id", "whoami", "hostname", "uptime", "sw_vers",
	},
}

func init() {
	for group, members := range groupMembers {
		for _, m := range members {
			if existing, dup := commandGroups[m]; dup {
				// Two groups claiming one tool would make the answer depend
				// on map iteration order, so it is a programming error rather
				// than something to resolve at runtime.
				panic("tokenamun: " + m + " is in both " + existing + " and " + group)
			}
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
