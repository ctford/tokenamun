package whatif

import (
	"fmt"
	"sort"

	"github.com/ctford/tokenamun/internal/content"
	"github.com/ctford/tokenamun/internal/model"
)

// FileCompression estimates making the files themselves smaller.
//
// Distinct from output-compression, and the distinction is the interesting
// part. A compression proxy sits between a tool and the context and shrinks
// what passes through; this shrinks the thing on disk, so it is smaller every
// time anything reads it, smaller in every later re-send, and smaller in every
// prefix rebuild. It is also the one intervention here that a person does by
// editing their own repository rather than by installing something, which is
// why the report can point at the files.
//
// It is the same question `tokenamun hotspots` asks from the other end: that
// command starts from a file's size and complexity and joins session cost onto
// it, and this starts from the cost and says what shrinking it was worth.
type FileCompression struct{}

func (FileCompression) Name() string { return "file-compression" }

func (FileCompression) Describe() string {
	return "make the files themselves smaller, so every read and every re-send is smaller"
}

func (FileCompression) Estimate(c Context) Result {
	r := Result{
		Intervention: "file-compression",
		Description:  FileCompression{}.Describe(),
		Unknown: []string{
			"sufficiency: a shorter file still has to answer the question the agent " +
				"was asking. A file trimmed past that point is read and then " +
				"something else is read as well, which costs more, not less.",
			"reachability: this prices the content that was read. How much of each " +
				"file could go without changing what it says is a judgement about " +
				"that file, and it is not in a transcript.",
			"human_readers: unlike a proxy, this changes the repository. The files " +
				"are also read by people, and their time is not in this budget.",
			behaviourUnknown,
			outcomeUnknown,
		},
	}

	// Carry cost is keyed by retrieval, so the join is on the sequence rather
	// than on a path that may be absent.
	carryBySeq := map[int]float64{}
	for _, it := range c.Carry.Items {
		carryBySeq[it.RetrievalSeq] = it.CarryEIT
	}

	type fileAgg struct {
		path   string
		bytes  int
		tokens float64
		carry  float64
		reads  int
	}
	byPath := map[string]*fileAgg{}
	var eligibleBytes int
	var eligibleTokens, eligibleCarry float64
	var reads, unattributedReads int

	for _, item := range c.Session.Retrievals {
		if !content.IsFileContent(item) {
			continue
		}
		reads++
		eligibleBytes += item.Bytes
		eligibleTokens += item.Tokens
		eligibleCarry += carryBySeq[item.Seq]
		if item.Path == "" {
			unattributedReads++
			continue
		}
		agg := byPath[item.Path]
		if agg == nil {
			agg = &fileAgg{path: item.Path}
			byPath[item.Path] = agg
		}
		agg.bytes += item.Bytes
		agg.tokens += item.Tokens
		agg.carry += carryBySeq[item.Seq]
		agg.reads++
	}

	total := c.Carry.PromptCostEIT
	r.Observed = []Finding{
		obs("file reads", float64(reads), model.Calls),
		obs("distinct files read", float64(len(byPath)), model.Calls),
		obs("file content", float64(eligibleBytes), model.Bytes),
		obs("prompt cost", total, model.EIT),
	}
	if unattributedReads > 0 {
		r.Observed = append(r.Observed, obs(
			"file reads with no recoverable path", float64(unattributedReads), model.Calls,
			"read through a compound shell command, so the content is counted but the "+
				"file is not named"))
	}

	if reads == 0 {
		r.Applicable = false
		r.NotMeasurable = "this session read no file content, so there is nothing here to shrink"
		return r
	}
	r.Applicable = true

	ratio, source := c.CompressionRatio, fmt.Sprintf(
		"assumed surviving fraction of %.0f%%, from --ratio", c.CompressionRatio*100)
	if c.FileReplay != nil && c.FileReplay.Items > 0 {
		ratio = c.FileReplay.Ratio()
		source = fmt.Sprintf(
			"measured: %s over %d of this session's own file reads, %.0f%% surviving",
			c.FileReplay.Command, c.FileReplay.Items, ratio*100)
	}

	r.Derived = []Finding{
		der("cost of carrying file content", eligibleCarry, model.EIT,
			"priced by residency: fetched once, re-sent on every call after it"),
	}
	if total > 0 {
		r.Derived = append(r.Derived,
			der("file content, share of prompt cost", eligibleCarry/total, model.Ratio))
	}

	// Ranked by what shrinking each one would be worth, which is not the same
	// as ranking by size: a big file read on the last call barely costs
	// anything, and a small one read early costs on every call since.
	files := make([]*fileAgg, 0, len(byPath))
	for _, f := range byPath {
		files = append(files, f)
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].carry > files[j].carry })
	for i, f := range files {
		if i >= 5 {
			break
		}
		r.Derived = append(r.Derived, der(f.path, f.carry*(1-ratio), model.EIT,
			fmt.Sprintf("%s read %s; worth this much shrunk to %.0f%%",
				bytesStr(f.bytes), plural(f.reads, "time"), ratio*100)))
	}

	saved := eligibleCarry * (1 - ratio)
	r.Counterfact = []Finding{
		fact("compression ratio", source),
		cf("net change", -saved, model.EIT,
			"negative is a saving. Unlike a proxy this shrinks the prefix itself, so it "+
				"also shrinks every cache rebuild, which is already priced in the carry "+
				"figure above"),
	}
	if total > 0 {
		r.Counterfact = append(r.Counterfact,
			cf("net change, share of prompt cost", -saved/total, model.Ratio))
	}
	r.Headline = &r.Counterfact[1]
	r.Caveat = fmt.Sprintf(
		"Scales linearly with a ratio you have to justify -- %s. Run it again with "+
			"--replay-with to measure the ratio on these files instead of assuming one, "+
			"and `tokenamun hotspots` to see which of them are big for reasons you could "+
			"remove.", source)
	return r
}

// bytesStr is a size for prose.
func bytesStr(v int) string {
	switch {
	case v >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(v)/(1<<20))
	case v >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(v)/(1<<10))
	default:
		return fmt.Sprintf("%d B", v)
	}
}

// plural is a count with its noun, so a report does not say "1 time(s)".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
