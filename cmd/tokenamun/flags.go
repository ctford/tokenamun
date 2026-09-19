package main

// Flag handling: the types Go's flag package does not have, and the two rules
// this CLI adds to it -- flags may follow a positional argument, and a flag a
// command cannot honour is an error rather than a no-op.
//
// Split from main.go when that file reached its length budget, along the seam
// the budget exposed: this changes when the argument grammar does, and the
// rest of main.go when the set of commands does.

import (
	"flag"
	"fmt"
	"strconv"
	"strings"
)

// pricesApplies rejects --prices on the commands that cannot honour it.
//
// Money is only offered where the total spans models, which is where EIT is
// unsound and where the conversion is available per call. The rest report one
// quantity in EIT throughout; `report` is absent because its payload feeds the
// HTML viewer, which would then show a figure the CLI does not.
func pricesApplies(cmd string, asked bool) error {
	if !asked {
		return nil
	}
	switch cmd {
	case "profile", "cache", "tree":
		return nil
	}
	return fmt.Errorf("--prices is not available on %s; it is taken by profile, cache and tree", cmd)
}

// given names the flags that were actually passed.
//
// For most flags the zero value is answer enough: an empty --at was not
// given. A bool is the exception, because false is also its default, and
// --prices has to be refused where it cannot be honoured rather than
// silently doing nothing. FlagSet.Visit accumulates across the repeated
// Parse calls that parseInterspersed makes, so it can be read once
// afterwards.
func given(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

// parseInterspersed parses flags that may appear before, after or between
// positional arguments, returning the positionals in order.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
	return positional, nil
}

// repeatable is a flag that may be given more than once, collecting each
// value. Go's flag package has no such type, and an intervention path is
// exactly the kind of thing you want several of.
type repeatable []string

func (r *repeatable) String() string { return strings.Join(*r, ",") }

func (r *repeatable) Set(v string) error {
	*r = append(*r, v)
	return nil
}

// last is the final value given, or "" when the flag was not given at all.
// For a flag that only means one thing, repeating it is a mistake and the
// last one is the conventional reading of it.
func (r repeatable) last() string {
	if len(r) == 0 {
		return ""
	}
	return r[len(r)-1]
}

// repeatableFloat is the same for a number. Go's flag package has neither,
// and --optimise needs one so that "not given" is an empty slice rather than
// a zero that is also a meaningful figure.
type repeatableFloat []float64

func (r *repeatableFloat) String() string {
	var parts []string
	for _, v := range *r {
		parts = append(parts, strconv.FormatFloat(v, 'g', -1, 64))
	}
	return strings.Join(parts, ",")
}

func (r *repeatableFloat) Set(v string) error {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fmt.Errorf("%q is not a number", v)
	}
	*r = append(*r, f)
	return nil
}
