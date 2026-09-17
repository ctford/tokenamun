package codescan

import (
	"path/filepath"
	"strings"
)

// languages maps an extension to a language name. An unlisted extension is not
// scanned, which is the right default: measuring complexity of a format we
// cannot parse or approximate would be worse than saying nothing.
var languages = map[string]string{
	".go": "go", ".ts": "typescript", ".tsx": "typescript",
	".js": "javascript", ".jsx": "javascript", ".py": "python",
	".rb": "ruby", ".rs": "rust", ".java": "java", ".kt": "kotlin",
	".scala": "scala", ".c": "c", ".h": "c", ".cc": "cpp", ".cpp": "cpp",
	".hpp": "cpp", ".cs": "csharp", ".php": "php", ".swift": "swift",
	".ex": "elixir", ".exs": "elixir", ".clj": "clojure",
	".sh": "shell", ".bash": "shell", ".zsh": "shell", ".sql": "sql",
}

// lineComments gives the line-comment marker per language, used both for
// counting code lines and for normalising before duplicate detection.
var lineComments = map[string]string{
	"go": "//", "typescript": "//", "javascript": "//", "rust": "//",
	"java": "//", "kotlin": "//", "scala": "//", "c": "//", "cpp": "//",
	"csharp": "//", "php": "//", "swift": "//", "sql": "--",
	"python": "#", "ruby": "#", "shell": "#", "elixir": "#", "clojure": ";",
}

func languageOf(path string) string {
	return languages[strings.ToLower(filepath.Ext(path))]
}

// isCode reports whether a line is code rather than blank or a comment. It is
// a heuristic: block comments are not tracked, so a long block comment counts
// as code. Stated rather than hidden, because the number is used in ratios.
func isCode(line, lang string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	if marker, ok := lineComments[lang]; ok && strings.HasPrefix(t, marker) {
		return false
	}
	return true
}
