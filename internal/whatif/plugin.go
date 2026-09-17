package whatif

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ctford/tokenamun/internal/model"
)

// EvidenceVersion is the version of the document handed to an external
// intervention. An intervention should refuse a version it does not know
// rather than guess at a field that has moved.
const EvidenceVersion = 1

// Evidence is what an external intervention is given on stdin: exactly what a
// built-in one sees, with nothing withheld and nothing added.
//
// That equivalence is the point of the interface. The alternative -- a
// narrower document, summarised for plugins -- would make the built-ins a
// privileged class that can answer questions an extension cannot, and every
// new question would mean a change here before anyone could ask it.
//
// It follows that this struct is a published contract. Fields may be added.
// Removing or renaming one is a version bump.
type Evidence struct {
	SchemaVersion int `json:"schema_version"`
	// Session, Cache and Carry are the same values the Go interventions
	// reason over, which is why they carry their own provenance labels.
	Session any `json:"session"`
	Cache   any `json:"cache"`
	Carry   any `json:"carry"`
	// Weights are the model-relative token-class multipliers, so an
	// intervention prices a change the same way the rest of the tool does
	// instead of hardcoding the published numbers.
	Weights any `json:"weights"`
	// CompressionRatio is the assumed surviving fraction, from --ratio. An
	// intervention that assumes a ratio must use this one, so the reader sees
	// one assumption rather than several.
	CompressionRatio float64 `json:"compression_ratio"`
	// Replay is set when --replay-with measured real compression, in which
	// case an intervention should prefer it over CompressionRatio.
	Replay any `json:"replay,omitempty"`
}

// evidenceFor serialises a Context. The fields are `any` above so that this
// package's own types stay free to change shape without the wire format
// pretending to be something separate: the JSON tags on those types *are* the
// format, and they are covered by a golden test.
func evidenceFor(c Context) Evidence {
	e := Evidence{
		SchemaVersion:    EvidenceVersion,
		Session:          c.Session,
		Cache:            c.Cache,
		Carry:            c.Carry,
		Weights:          c.Weights,
		CompressionRatio: c.CompressionRatio,
	}
	if c.Replay != nil {
		e.Replay = c.Replay
	}
	return e
}

// Manifest is what an external intervention prints in response to `describe`.
type Manifest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// External is an intervention supplied as an executable.
//
// The protocol is two invocations of the same program:
//
//	prog describe              -> a Manifest on stdout
//	prog estimate < evidence   -> a Result on stdout
//
// Anything on stderr is passed through to the user's terminal, so a script can
// log freely without corrupting its output.
type External struct {
	// Path is the executable. It is also what an error message names, since
	// the point of a failure here is to say which of your scripts broke.
	Path     string
	manifest Manifest
	// Timeout bounds a script that hangs. A wedged intervention should not
	// wedge the report.
	Timeout time.Duration
}

// ExternalTimeout is how long an external intervention gets. Generous, because
// an intervention may legitimately shell out to a compressor over a few
// megabytes of evidence, but not unbounded.
const ExternalTimeout = 60 * time.Second

func (e *External) Name() string     { return e.manifest.Name }
func (e *External) Describe() string { return e.manifest.Description }

// Estimate runs the script and validates what it returns.
//
// A failure is reported as a Result rather than returned as an error, because
// one broken extension must not take down a report that has six working
// interventions in it. The failure appears in the table where that
// intervention's number would have been, which is where someone debugging it
// will look.
func (e *External) Estimate(c Context) Result {
	r, err := e.estimate(c)
	if err != nil {
		return Result{
			Intervention:  e.manifest.Name,
			Description:   e.manifest.Description,
			Applicable:    false,
			NotMeasurable: err.Error(),
			Caveat:        "This script failed; the row is not a finding.",
			Unknown: []string{
				"everything: the script that was to estimate this did not produce a usable result.",
			},
		}
	}
	return r
}

func (e *External) estimate(c Context) (Result, error) {
	evidence, err := json.Marshal(evidenceFor(c))
	if err != nil {
		return Result{}, fmt.Errorf("could not serialise the evidence: %w", err)
	}
	out, err := e.run("estimate", evidence)
	if err != nil {
		return Result{}, err
	}
	var r Result
	if err := json.Unmarshal(out, &r); err != nil {
		return Result{}, fmt.Errorf("%s printed something that is not a result: %w", e.Path, err)
	}
	if err := Validate(&r, e.manifest); err != nil {
		return Result{}, err
	}
	return r, nil
}

// run executes one phase of the protocol.
func (e *External) run(phase string, stdin []byte) ([]byte, error) {
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = ExternalTimeout
	}
	cmd := exec.Command(e.Path, phase)
	isolate(cmd)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Stderr = os.Stderr
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("could not run %s: %w", e.Path, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return nil, fmt.Errorf("%s %s failed: %w", e.Path, phase, err)
		}
	case <-time.After(timeout):
		terminate(cmd)
		return nil, fmt.Errorf("%s %s did not finish within %s", e.Path, phase, timeout)
	}
	return stdout.Bytes(), nil
}

// Validate enforces on an external result the same three rules the built-ins
// are held to by their own tests. It is the whole reason this is an interface
// and not just "run a script and print whatever it says".
func Validate(r *Result, m Manifest) error {
	if r.Intervention == "" {
		r.Intervention = m.Name
	}
	if r.Intervention != m.Name {
		return fmt.Errorf("intervention named itself %q in describe and %q in estimate",
			m.Name, r.Intervention)
	}
	if r.Description == "" {
		r.Description = m.Description
	}
	// Rule: no reduction without the outcome caveat. An intervention that
	// claims a saving and lists nothing it cannot know is making the exact
	// unfalsifiable claim this package exists to avoid.
	if len(r.Unknown) == 0 {
		return fmt.Errorf("%s returned no unknowns: every counterfactual must state "+
			"what it cannot know, starting with whether the task still succeeded",
			r.Intervention)
	}
	// Rule: a headline must be quotable, which means it must arrive with the
	// one thing a reader needs to know before quoting it.
	if r.Headline != nil && r.Applicable && strings.TrimSpace(r.Caveat) == "" {
		return fmt.Errorf("%s nominated a headline with no caveat: the caveat is the "+
			"column beside the number, and an unqualified number is how the published "+
			"claims got this wrong", r.Intervention)
	}
	// The caveat shares a table row with a number, in a table of eight. A
	// paragraph there is not read, and eight paragraphs are read even less,
	// so the argument goes in caveat_detail where there is room for it.
	if n := len([]rune(r.Caveat)); n > CaveatLimit {
		return fmt.Errorf("%s: caveat is %d characters and the limit is %d. It is a "+
			"column in a table; put the argument in caveat_detail", r.Intervention, n, CaveatLimit)
	}
	switch {
	case !r.Applicable:
	case r.Acts == AxisVolume, r.Acts == AxisRoundTrips, r.Acts == AxisPrice:
	default:
		return fmt.Errorf("%s must say which factor it moves: acts_on is one of %q, "+
			"%q or %q. A box's cost is volume x round trips x price, and only the "+
			"first reads as a discount on what the viewer draws",
			r.Intervention, AxisVolume, AxisRoundTrips, AxisPrice)
	}
	if !r.Applicable && strings.TrimSpace(r.NotMeasurable) == "" {
		return fmt.Errorf("%s says it is not applicable but does not say why", r.Intervention)
	}
	// Every quantity carries a provenance, and a counterfactual that labels
	// itself observed would launder a guess into a measurement.
	for _, f := range allFindings(r) {
		if f.Quantity == nil {
			continue
		}
		if err := f.Quantity.Validate(); err != nil {
			return fmt.Errorf("%s: finding %q: %w", r.Intervention, f.Label, err)
		}
	}
	for _, f := range r.Counterfact {
		if f.Quantity != nil && f.Quantity.Prov != model.Counterfactual {
			return fmt.Errorf("%s: %q is in the counterfactual section but labelled %q",
				r.Intervention, f.Label, f.Quantity.Prov)
		}
	}
	return nil
}

func allFindings(r *Result) []Finding {
	out := make([]Finding, 0, len(r.Observed)+len(r.Derived)+len(r.Counterfact))
	out = append(out, r.Observed...)
	out = append(out, r.Derived...)
	out = append(out, r.Counterfact...)
	if r.Headline != nil {
		out = append(out, *r.Headline)
	}
	return out
}

// LoadExternal reads an intervention's manifest, which is also how we find out
// whether it works at all before a report depends on it.
func LoadExternal(path string) (*External, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	e := &External{Path: abs}
	out, err := e.run("describe", nil)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(out, &e.manifest); err != nil {
		return nil, fmt.Errorf("%s did not describe itself: %w", abs, err)
	}
	if e.manifest.Name == "" {
		return nil, fmt.Errorf("%s described itself with no name", abs)
	}
	if e.manifest.Description == "" {
		return nil, fmt.Errorf("%s described itself with no description: the table has a "+
			"column for what an intervention targets", abs)
	}
	for _, builtin := range All() {
		if builtin.Name() == e.manifest.Name {
			return nil, fmt.Errorf("%s calls itself %q, which is a built-in intervention; "+
				"pick another name so the two are told apart in the report",
				abs, e.manifest.Name)
		}
	}
	return e, nil
}

// InterventionDirEnv names directories to search, colon-separated.
const InterventionDirEnv = "TOKENAMUN_INTERVENTIONS"

// SearchPath is where interventions are looked for, most specific first.
//
// Deliberately absent: the repository being analysed. Tokenamun is routinely
// pointed at a checkout you did not write -- that is most of what it is for --
// and a tool that executes scripts it finds in the subject of its analysis is
// a tool that runs a stranger's code because you asked a question about their
// tokens. Interventions come from places the person running the tool controls.
func SearchPath() []string {
	var dirs []string
	if env := os.Getenv(InterventionDirEnv); env != "" {
		for _, d := range filepath.SplitList(env) {
			if d != "" {
				dirs = append(dirs, d)
			}
		}
	}
	// ~/.config first, because that is where someone installing a script for a
	// command-line tool will put it, on a Mac as much as on Linux.
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".config", "tokenamun", "interventions"))
	}
	// And the platform's own location, for anyone who follows it.
	if cfg, err := os.UserConfigDir(); err == nil {
		p := filepath.Join(cfg, "tokenamun", "interventions")
		var already bool
		for _, d := range dirs {
			already = already || d == p
		}
		if !already {
			dirs = append(dirs, p)
		}
	}
	return dirs
}

// Discover loads every executable intervention on the search path, plus any
// named explicitly.
//
// Errors are returned alongside the interventions that did load, rather than
// instead of them: a typo in one script should report that typo and leave the
// rest of the report working.
func Discover(explicit []string) ([]Intervention, []error) {
	var found []Intervention
	var errs []error
	seen := map[string]bool{}

	load := func(path string) {
		abs, err := filepath.Abs(path)
		if err != nil {
			errs = append(errs, err)
			return
		}
		if seen[abs] {
			return
		}
		seen[abs] = true
		e, err := LoadExternal(abs)
		if err != nil {
			errs = append(errs, err)
			return
		}
		found = append(found, e)
	}

	for _, dir := range SearchPath() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			// A missing directory is the normal case, not a problem.
			continue
		}
		var names []string
		for _, entry := range entries {
			if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			info, err := entry.Info()
			if err != nil || info.Mode()&0o111 == 0 {
				continue
			}
			names = append(names, entry.Name())
		}
		// Sorted, so a report is the same twice running.
		sort.Strings(names)
		for _, name := range names {
			load(filepath.Join(dir, name))
		}
	}
	for _, path := range explicit {
		load(path)
	}
	return found, errs
}
