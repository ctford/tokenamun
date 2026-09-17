package whatif

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ctford/tokenamun/internal/model"
)

// RTK estimates wrapping shell commands in RTK's output filter.
//
// RTK is command-shaped rather than language-shaped: it has adapters for
// specific development commands and filters, groups, truncates and
// deduplicates their output. So eligibility is not "all shell output" -- it is
// the commands it has an adapter for, which is what this intervention
// measures.
//
// RTK is also unusually honest about what its own numbers mean. It states that
// it measures bash-output reduction rather than bill reduction, and that "the
// reduction dilutes at every step". Closing that gap is precisely what this
// tool is for: the published percentage applies to a slice of content, and
// what matters is what that slice cost to carry.
type RTK struct{}

func (RTK) Name() string { return "rtk" }

func (RTK) Describe() string {
	return "filter shell command output through RTK's per-command adapters"
}

// rtkFamily is a group of commands RTK handles, with the reduction it
// publishes for them where it publishes one.
type rtkFamily struct {
	name string
	re   *regexp.Regexp
	// surviving is the fraction RTK's own figures leave behind, or 0 when it
	// publishes no number for this family.
	surviving float64
	published string
}

// Families and figures as published by the project. Where no per-command
// number is given, the overall 60-90% band is applied and labelled as such
// rather than a specific figure being invented.
var rtkFamilies = []rtkFamily{
	{"tests", regexp.MustCompile(`\b(cargo test|pytest|jest|go test|rspec|vitest|ginkgo)\b`), 0.10, "-90%"},
	{"build", regexp.MustCompile(`\b(cargo build|go build|tsc|npm run build|make build)\b`), 0.20, "-80%"},
	{"lint", regexp.MustCompile(`\b(ruff check|eslint|golangci-lint|go vet|clippy)\b`), 0.20, "-80%"},
	{"sql", regexp.MustCompile(`\b(sqlfluff)\b`), 0.25, "-75%"},
	{"git", regexp.MustCompile(`\bgit (status|log|diff|add|commit|push|pull|show|branch)\b`), 0, ""},
	{"files", regexp.MustCompile(`\b(ls|tree|cat|grep|rg|find|diff|head|tail|sed|bat|nl)\b`), 0, ""},
	{"packages", regexp.MustCompile(`\b(pnpm|npm|yarn|pip|bundle|prisma|cargo add)\b`), 0, ""},
	{"containers", regexp.MustCompile(`\b(docker ps|docker logs|kubectl)\b`), 0, ""},
	{"cloud", regexp.MustCompile(`\b(aws|pulumi|terraform)\b`), 0, ""},
}

// RTK's overall claim, as a surviving fraction.
const (
	rtkBandBest  = 0.10 // the 90% end
	rtkBandWorst = 0.40 // the 60% end
)

func (RTK) Estimate(c Context) Result {
	r := Result{
		Intervention: "rtk",
		Description:  RTK{}.Describe(),
		Unknown: []string{
			"published_basis: RTK's figures measure bash-output reduction, not bill " +
				"reduction, and are estimated with a bytes/4 approximation rather than a " +
				"tokenizer. It says so itself. The carry numbers here are this session's, " +
				"but the ratios applied to them are RTK's.",
			"adapter_quality: a command matching a family name is not proof RTK has a " +
				"good adapter for that exact invocation, or that its filter keeps what " +
				"this agent needed.",
			"additional_tool_calls: filtered output the agent then re-fetched in full " +
				"would give back the saving.",
			behaviourUnknown,
			outcomeUnknown,
		},
	}

	// Commands live on the tool call, so index retrievals by tool id to
	// recover what produced each payload.
	commands := map[string]string{}
	for _, tc := range c.Session.ToolCalls {
		if tc.Command != "" {
			commands[tc.ID] = tc.Command
		}
	}
	carryByRetrieval := map[int]float64{}
	for _, it := range c.Carry.Items {
		carryByRetrieval[it.RetrievalSeq] = it.CarryEIT
	}

	type agg struct {
		bytes int
		carry float64
		items int
	}
	byFamily := map[string]*agg{}
	var shellBytes int
	var shellCarry, coveredCarry, publishedSaving float64
	var coveredBytes int

	for _, item := range c.Session.Retrievals {
		if !viaTool(item.Tool) {
			continue
		}
		shellBytes += item.Bytes
		shellCarry += carryByRetrieval[item.Seq]

		cmd := commands[item.ToolID]
		if cmd == "" {
			continue
		}
		fam, ok := rtkMatch(cmd)
		if !ok {
			continue
		}
		a := byFamily[fam.name]
		if a == nil {
			a = &agg{}
			byFamily[fam.name] = a
		}
		a.bytes += item.Bytes
		a.items++
		a.carry += carryByRetrieval[item.Seq]
		coveredBytes += item.Bytes
		coveredCarry += carryByRetrieval[item.Seq]

		surviving := fam.surviving
		if surviving == 0 {
			// No published figure for this family: use the conservative end of
			// the overall band rather than the flattering one.
			surviving = rtkBandWorst
		}
		publishedSaving += carryByRetrieval[item.Seq] * (1 - surviving)
	}

	r.Observed = []Finding{
		obs("shell and tool output", float64(shellBytes), model.Bytes),
		obs("covered by an RTK adapter", float64(coveredBytes), model.Bytes),
		obs("carry cost of covered output", coveredCarry, model.EIT),
		obs("carry cost of all shell output", shellCarry, model.EIT),
	}
	for _, fam := range rtkFamilies {
		a := byFamily[fam.name]
		if a == nil {
			continue
		}
		note := "no published per-command figure; the conservative end of RTK's 60-90% band applied"
		if fam.published != "" {
			note = "RTK publishes " + fam.published + " for this family"
		}
		r.Observed = append(r.Observed, obs(
			fmt.Sprintf("  %s (%d results)", fam.name, a.items), a.carry, model.EIT, note))
	}

	r.Applicable = coveredBytes > 0
	r.Acts = AxisVolume
	if !r.Applicable {
		r.NotMeasurable = "No shell output RTK has an adapter for."
		return r
	}

	coverage := 0.0
	if shellCarry > 0 {
		coverage = coveredCarry / shellCarry
	}
	r.Derived = []Finding{
		der("RTK coverage of shell carry cost", coverage, model.Ratio),
		der("uncovered shell carry cost", shellCarry-coveredCarry, model.EIT,
			"bespoke shell: heredocs, echo pipelines, inline scripts. RTK has no adapter for these."),
	}

	r.Addressable = addressable("shell output RTK has an adapter for", coveredCarry, c.Total)
	if coveredCarry > 0 {
		r.Reduction = -publishedSaving / coveredCarry
	}

	r.Counterfact = []Finding{
		cf("saving, RTK's published figures per family", -publishedSaving, model.EIT),
		cf("saving, whole band at 90% off covered output", -coveredCarry*(1-rtkBandBest), model.EIT),
		cf("saving, whole band at 60% off covered output", -coveredCarry*(1-rtkBandWorst), model.EIT),
		cf("share of prompt cost, published figures",
			-publishedSaving/nonZero(c.Carry.PromptCostEIT), model.Ratio,
			"the cache-invalidation cost of rewriting context is in unknown, not netted out here"),
	}
	r.Headline = &r.Counterfact[0]
	r.Caveat = "Published figures cover tests, build and lint only."
	r.CaveatDetail = "git and file commands are most of the covered cost here and use " +
		"the conservative end of its 60-90% band."
	return r
}

// rtkMatch finds the family a command belongs to, most specific first.
func rtkMatch(cmd string) (rtkFamily, bool) {
	lower := strings.ToLower(cmd)
	for _, fam := range rtkFamilies {
		if fam.re.MatchString(lower) {
			return fam, true
		}
	}
	return rtkFamily{}, false
}

func nonZero(v float64) float64 {
	if v == 0 {
		return 1
	}
	return v
}
