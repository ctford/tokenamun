package codescan

import (
	"sort"
	"strings"

	"github.com/ctford/tokenamun/internal/tokens"
)

// corpus accumulates normalised lines so duplicate runs can be found across
// the whole scan rather than within one file.
//
// The method is window hashing: normalise each line, hash every window of
// MinDuplicateLines consecutive lines, and group windows that hash alike. A
// matching window is then extended forward while all its occurrences continue
// to agree, so the reported run is maximal rather than the window size. This
// is a small clone detector, not a research one: it finds copy-paste, and it
// will miss duplication that differs by more than whitespace, comments and
// case.
type corpus struct {
	window int
	files  []normalised
}

type normalised struct {
	path  string
	lines []string
	// origin maps a normalised line back to its 1-based source line.
	origin []int
}

func newCorpus(window int) *corpus {
	return &corpus{window: window}
}

// add normalises a file into the corpus. Blank lines and comments are dropped
// rather than normalised, so that reformatting does not hide a duplicate.
func (c *corpus) add(path, lang, src string) {
	n := normalised{path: path}
	for i, line := range strings.Split(src, "\n") {
		if !isCode(line, lang) {
			continue
		}
		n.lines = append(n.lines, normaliseLine(line))
		n.origin = append(n.origin, i+1)
	}
	if len(n.lines) >= c.window {
		c.files = append(c.files, n)
	}
}

// normaliseLine collapses whitespace and case so that formatting differences
// do not hide a duplicate. Identifiers are deliberately left alone: renaming
// a variable makes it a different piece of code for our purposes.
func normaliseLine(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

type windowRef struct {
	file  int
	index int
}

// duplicates finds maximal duplicated runs.
//
// Windows are visited in file-then-line order and the first uncovered member
// of a group claims the run, so the output does not depend on map iteration
// order and a long run is reported once rather than as every sub-window of
// itself.
func (c *corpus) duplicates() []Duplicate {
	if c.window <= 0 || len(c.files) == 0 {
		return nil
	}

	groups := map[string][]windowRef{}
	var positions []windowRef
	for fi, f := range c.files {
		for i := 0; i+c.window <= len(f.lines); i++ {
			h := tokens.Hash(strings.Join(f.lines[i:i+c.window], "\n"))
			ref := windowRef{file: fi, index: i}
			groups[h] = append(groups[h], ref)
			positions = append(positions, ref)
		}
	}

	hashOf := func(r windowRef) string {
		f := c.files[r.file]
		return tokens.Hash(strings.Join(f.lines[r.index:r.index+c.window], "\n"))
	}

	covered := map[windowRef]bool{}
	var out []Duplicate
	for _, p := range positions {
		if covered[p] {
			continue
		}
		refs := groups[hashOf(p)]
		if len(refs) < 2 {
			continue
		}
		length := c.extend(refs)
		for _, r := range refs {
			for off := 0; off <= length-c.window; off++ {
				covered[windowRef{file: r.file, index: r.index + off}] = true
			}
		}

		d := Duplicate{Lines: length}
		for _, r := range refs {
			f := c.files[r.file]
			d.Occurrences = append(d.Occurrences, Location{
				Path:      f.path,
				StartLine: f.origin[r.index],
			})
		}
		sort.SliceStable(d.Occurrences, func(i, j int) bool {
			if d.Occurrences[i].Path != d.Occurrences[j].Path {
				return d.Occurrences[i].Path < d.Occurrences[j].Path
			}
			return d.Occurrences[i].StartLine < d.Occurrences[j].StartLine
		})
		out = append(out, d)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Lines != out[j].Lines {
			return out[i].Lines > out[j].Lines
		}
		return out[i].Occurrences[0].Path < out[j].Occurrences[0].Path
	})
	return out
}

// extend grows a matching window while every occurrence still agrees, using
// the first occurrence as the reference.
func (c *corpus) extend(refs []windowRef) int {
	length := c.window
	base := c.files[refs[0].file]
	for {
		bi := refs[0].index + length
		if bi >= len(base.lines) {
			return length
		}
		want := base.lines[bi]
		for _, r := range refs[1:] {
			f := c.files[r.file]
			i := r.index + length
			if i >= len(f.lines) || f.lines[i] != want {
				return length
			}
		}
		length++
	}
}
