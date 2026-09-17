package content

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ctford/tokenamun/internal/model"
)

// ConfigFile is the per-repository configuration, found by walking up from the
// directory being profiled.
const ConfigFile = ".tokenamun.json"

// Config declares which subtrees hold which kind of content.
//
// Declaring beats guessing. Every codebase names these differently -- adr/,
// decisions/, docs/decisions/, rfcs/ -- and a tool that accumulates one
// repository's conventions in its source is both wrong for the next repository
// and dishonest about which of its answers were assumptions.
type Config struct {
	// Categories maps a category to the subtrees that hold it. Paths are
	// repository-relative; a retrieval's absolute path matches when the
	// subtree appears as a path-segment prefix of it.
	Categories map[string][]string `json:"categories"`
}

// Classifier assigns categories to paths.
//
// Declared subtrees are consulted first and are exact. The name heuristics are
// a fallback so the tool says something useful with no configuration, and the
// report distinguishes the two: a category built from guesses about directory
// names is worth less than one the team declared.
type Classifier struct {
	declared []declaration
	// Source is where the declarations came from, for reporting.
	Source string
}

type declaration struct {
	prefix   string
	category model.Category
}

// NewClassifier builds a classifier from a config.
func NewClassifier(cfg Config, source string) (Classifier, error) {
	c := Classifier{Source: source}
	for name, subtrees := range cfg.Categories {
		cat, ok := categoryNamed(name)
		if !ok {
			return Classifier{}, fmt.Errorf("unknown category %q in %s; known categories are %v",
				name, source, categoryNames())
		}
		for _, sub := range subtrees {
			clean := strings.Trim(filepath.ToSlash(strings.TrimSpace(sub)), "/")
			if clean == "" {
				continue
			}
			c.declared = append(c.declared, declaration{prefix: clean, category: cat})
		}
	}
	// Longest prefix wins, so docs/decisions/rationale can be declared
	// differently from docs/decisions.
	for i := range c.declared {
		for j := i + 1; j < len(c.declared); j++ {
			if len(c.declared[j].prefix) > len(c.declared[i].prefix) {
				c.declared[i], c.declared[j] = c.declared[j], c.declared[i]
			}
		}
	}
	return c, nil
}

// LoadConfig finds and reads a config by walking up from dir. A missing file is
// not an error: the built-in heuristics are the zero-configuration behaviour.
func LoadConfig(dir string) (Classifier, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	for {
		path := filepath.Join(abs, ConfigFile)
		if raw, err := os.ReadFile(path); err == nil {
			var cfg Config
			if err := json.Unmarshal(raw, &cfg); err != nil {
				return Classifier{}, fmt.Errorf("%s: %w", path, err)
			}
			return NewClassifier(cfg, path)
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return Classifier{Source: "built-in heuristics"}, nil
		}
		abs = parent
	}
}

// LoadConfigFile reads a config from an explicit path.
func LoadConfigFile(path string) (Classifier, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Classifier{}, err
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Classifier{}, fmt.Errorf("%s: %w", path, err)
	}
	return NewClassifier(cfg, path)
}

// Match categorises a path.
//
// Declared reports whether the answer came from configuration rather than from
// a guess about naming, which is the difference between knowing and assuming.
func (c Classifier) Match(path string) (cat model.Category, matched, declared bool) {
	if path == "" {
		return model.CatOther, false, false
	}
	norm := filepath.ToSlash(path)
	for _, d := range c.declared {
		if underSubtree(norm, d.prefix) {
			return d.category, true, true
		}
	}
	cat, matched = Classify(norm)
	return cat, matched, false
}

// underSubtree reports whether path is inside subtree, comparing whole path
// segments so that docs/decisions does not match docs/decisions-archive.
func underSubtree(path, subtree string) bool {
	if path == subtree {
		return true
	}
	if strings.HasPrefix(path, subtree+"/") {
		return true
	}
	// Retrieval paths are frequently absolute, so the subtree may appear in
	// the middle: /Users/x/repo/docs/decisions/0001.md.
	if i := strings.Index(path, "/"+subtree+"/"); i >= 0 {
		return true
	}
	return strings.HasSuffix(path, "/"+subtree)
}

// categoryNamed resolves a configured category name.
func categoryNamed(name string) (model.Category, bool) {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, cat := range model.Categories() {
		if strings.EqualFold(string(cat), want) {
			return cat, true
		}
	}
	// Accept a couple of obvious singular forms so a config does not fail on
	// punctuation.
	switch want {
	case "adr", "decision", "decisions":
		return model.CatADR, true
	case "spec", "specification":
		return model.CatSpecification, true
	case "test", "source":
		if want == "test" {
			return model.CatTest, true
		}
		return model.CatSourceCode, true
	case "plan":
		return model.CatPlan, true
	case "doc", "docs":
		return model.CatDocumentation, true
	}
	return "", false
}

func categoryNames() []string {
	var out []string
	for _, c := range model.Categories() {
		out = append(out, string(c))
	}
	return out
}
