// Package ingest turns a transcript into a normalized model.Session.
//
// Its central job is the deduplication in METHODOLOGY.md section 2: a Claude
// Code assistant entry is a content block, not an API call, and entries
// sharing a requestId repeat the same usage object. Summing per entry
// overstates token usage by roughly 70% on real sessions.
package ingest

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/ctford/tokenamun/internal/claudecode"
	"github.com/ctford/tokenamun/internal/content"
	"github.com/ctford/tokenamun/internal/model"
	"github.com/ctford/tokenamun/internal/tokens"
)

// maxLine is generous enough for a transcript line carrying a large tool
// result. Lines above it are reported rather than silently truncated.
const maxLine = 64 << 20

// Options configures ingestion.
type Options struct {
	// Classifier decides what retrieved content is. The zero value uses the
	// built-in naming heuristics.
	Classifier content.Classifier
}

// Load parses the transcript a ref points at, using the built-in classifier.
func Load(ref model.SessionRef) (*model.Session, error) {
	return LoadWith(ref, Options{})
}

// LoadWith parses the transcript a ref points at.
func LoadWith(ref model.SessionRef, opts Options) (*model.Session, error) {
	f, err := os.Open(ref.Transcript)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseWith(f, ref, opts)
}

// Parse reads a transcript from r with the built-in classifier.
func Parse(r io.Reader, ref model.SessionRef) (*model.Session, error) {
	return ParseWith(r, ref, Options{})
}

// ParseWith reads a transcript from r.
//
// A transcript that is still being appended to can end in a partial line, so
// an unparseable final line is tolerated and reported as a warning. An
// unparseable line anywhere else is also survivable -- one bad line should not
// cost the whole profile -- but it is counted.
func ParseWith(r io.Reader, ref model.SessionRef, opts Options) (*model.Session, error) {
	s := &model.Session{Ref: ref}
	s.ClassifierSource = opts.Classifier.Source
	if s.ClassifierSource == "" {
		s.ClassifierSource = "built-in heuristics"
	}

	byRequest := map[string]int{} // request id -> index into s.Invocations
	toolIndex := map[string]int{} // tool_use id -> index into s.ToolCalls
	var skipped int
	var lastLineBad bool

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), maxLine)
	for sc.Scan() {
		line := sc.Bytes()
		s.TranscriptLines++
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var e claudecode.Entry
		if err := json.Unmarshal(line, &e); err != nil {
			skipped++
			lastLineBad = true
			continue
		}
		lastLineBad = false
		absorb(s, e, opts.Classifier, byRequest, toolIndex)
	}
	if err := sc.Err(); err != nil && !errors.Is(err, bufio.ErrTooLong) {
		return nil, err
	} else if err != nil {
		s.Warn("line_too_long", "a transcript line exceeded the read buffer and was skipped")
	}

	switch {
	case skipped == 1 && lastLineBad:
		s.Warn("partial_final_line",
			"the transcript's last line was incomplete, which is expected while a session is still running")
	case skipped > 0:
		s.Warn("unparseable_lines", fmt.Sprintf("%d transcript lines could not be parsed", skipped))
	}

	resolveTools(s, toolIndex)
	estimate(s)
	findRepeats(s)
	diagnose(s)
	return s, nil
}

// absorb folds one transcript entry into the session.
func absorb(s *model.Session, e claudecode.Entry, cl content.Classifier, byRequest, toolIndex map[string]int) {
	if e.CWD != "" && s.CWD == "" {
		s.CWD = e.CWD
	}
	if e.GitBranch != "" && s.Branch == "" {
		s.Branch = e.GitBranch
	}

	switch e.Type {
	case "assistant":
		s.AssistantEntries++
		absorbAssistant(s, e, byRequest, toolIndex)
	case "user":
		if e.Message == nil {
			return
		}
		if results := e.Message.ToolResults(); len(results) > 0 {
			meta := claudecode.ParseResultMeta(e.ToolUseResult)
			for _, b := range results {
				absorbResult(s, b, meta, cl, toolIndex)
			}
			return
		}
		// A user entry that is not carrying tool results is a prompt. isMeta
		// marks harness-injected content rather than something a human typed.
		if !e.IsMeta {
			s.Prompts++
			s.PromptEntries = append(s.PromptEntries, model.PromptEntry{
				Bytes:         e.Message.Content.Len(),
				InvocationSeq: len(s.Invocations) - 1,
			})
		}
	}
}

// absorbAssistant deduplicates an assistant entry into an invocation.
//
// The key is requestId, falling back to message.id: both were present and in
// exact agreement on the reference dataset, but only one of them is guaranteed
// to be there on an entry.
func absorbAssistant(s *model.Session, e claudecode.Entry, byRequest, toolIndex map[string]int) {
	key := e.RequestID
	if key == "" && e.Message != nil {
		key = e.Message.ID
	}

	idx, seen := byRequest[key]
	if key == "" {
		// With no key we cannot tell a repeat from a new call. Counting it
		// would risk double-counting usage, so it is dropped and reported.
		s.Warn("unkeyed_assistant_entry",
			"an assistant entry had neither requestId nor message.id and was not counted")
		return
	}
	if !seen {
		inv := model.ModelInvocation{
			Seq:       len(s.Invocations),
			RequestID: e.RequestID,
			Version:   e.Version,
			Effort:    e.Effort,
			Timestamp: e.Time(),
			Sidechain: e.IsSidechain,
		}
		if e.Message != nil {
			inv.MessageID = e.Message.ID
			inv.Model = e.Message.Model
			inv.Usage = usageOf(e.Message.Usage)
		}
		idx = len(s.Invocations)
		byRequest[key] = idx
		s.Invocations = append(s.Invocations, inv)
	}
	s.Invocations[idx].Entries++

	if e.Message == nil {
		return
	}
	for _, b := range e.Message.Content.Blocks {
		if b.Type == "text" {
			s.ProseBytes += len(b.Text)
		}
	}
	for _, b := range e.Message.ToolUses() {
		if _, dup := toolIndex[b.ID]; dup {
			continue
		}
		toolIndex[b.ID] = len(s.ToolCalls)
		s.ToolCalls = append(s.ToolCalls, model.ToolCall{
			Seq:           len(s.ToolCalls),
			ID:            b.ID,
			Name:          b.Name,
			InputBytes:    len(b.Input),
			InvocationSeq: idx,
			Command:       commandOf(b.Input),
		})
	}
}

// absorbResult attaches an observed tool result to its call and records what
// entered the context as retrieved content.
//
// Size comes from the tool_result block, which is what the model received.
// The transcript's toolUseResult field is richer but does not measure the
// same thing: on real sessions it disagrees by more than an order of
// magnitude, because large output is spilled to a file and the model is shown
// only an excerpt. It is used here for path, line range and truncation only.
func absorbResult(s *model.Session, b claudecode.Block, meta claudecode.ResultMeta, cl content.Classifier, toolIndex map[string]int) {
	bytesIn := b.Content.Len()
	images, imageBytes := b.Content.Images()

	idx, ok := toolIndex[b.ToolUseID]
	if !ok {
		// A result with no matching call: the call may predate a resumed
		// transcript. Recorded so the bytes are not lost.
		s.ToolCalls = append(s.ToolCalls, model.ToolCall{
			Seq:           len(s.ToolCalls),
			ID:            b.ToolUseID,
			Name:          "(unpaired)",
			ResultBytes:   bytesIn,
			IsError:       b.IsError,
			Resolved:      true,
			InvocationSeq: -1,
		})
		return
	}
	tc := &s.ToolCalls[idx]
	tc.ResultBytes = bytesIn
	tc.IsError = b.IsError
	tc.Resolved = true

	if bytesIn == 0 && images == 0 {
		return
	}
	cat, prov, path, declared, paths := categorise(cl, tc.Name, tc.Command, meta)
	s.Retrievals = append(s.Retrievals, model.RetrievedContent{
		Seq:            len(s.Retrievals),
		ToolID:         tc.ID,
		Tool:           tc.Name,
		Category:       cat,
		Channel:        content.ChannelFor(tc.Name, meta.Path != ""),
		CommandClass:   content.CommandClass(tc.Command),
		CommandDetail:  content.CommandDetail(tc.Command),
		CommandBinary:  content.CommandBinary(tc.Command),
		PipelineFilter: content.IsPipelineFilter(tc.Command),
		CategoryProv:   prov,
		Declared:       declared,
		Path:           path,
		Paths:          paths,
		Bytes:          bytesIn,
		Images:         images,
		ImageBytes:     imageBytes,
		Hash:           tokens.Hash(b.Content.String()),
		InvocationSeq:  tc.InvocationSeq,
		StartLine:      meta.StartLine,
		Lines:          meta.Lines,
		TotalLines:     meta.TotalLines,
		Partial:        meta.Partial(),
		Truncated:      meta.Truncated,
		WithheldBytes:  withheld(meta, bytesIn),
		IsError:        b.IsError,
	})
}

// categorise decides what a payload is, and how confident that is.
//
// A path reported by the tool is observed, so classifying from it is derived.
// A path parsed out of a shell command line is a guess, so it is inferred.
// Output that cannot be attributed to a path is tool output rather than a
// speculative category.
func categorise(cl content.Classifier, tool, command string, meta claudecode.ResultMeta) (
	cat model.Category, prov model.Provenance, path string, declared bool, paths []string) {
	if meta.Path != "" {
		c, ok, dec := cl.Match(meta.Path)
		if ok {
			return c, model.Derived, meta.Path, dec, []string{meta.Path}
		}
		return model.CatOther, model.Derived, meta.Path, false, []string{meta.Path}
	}

	if command != "" {
		found := content.PathsFromCommand(command)
		cats := map[model.Category]bool{}
		anyDeclared := false
		var classified []string
		for _, p := range found {
			c, ok, dec := cl.Match(p)
			if !ok {
				continue
			}
			cats[c] = true
			anyDeclared = anyDeclared || dec
			classified = append(classified, p)
		}
		switch len(cats) {
		case 0:
			// Nothing classifiable; fall through to tool output.
		case 1:
			for c := range cats {
				return c, model.Inferred, classified[0], anyDeclared, classified
			}
		default:
			// A compound command that read a decision record, a source file
			// and a directory listing in one result is genuinely a mixture.
			// Splitting the bytes between them would be invented precision,
			// and picking one would be arbitrary.
			return model.CatMixed, model.Inferred, "", anyDeclared, classified
		}
	}
	return content.ClassifyTool(tool), model.Observed, "", false, nil
}

// withheld reports how much content the harness kept out of context. It is
// observed, and it is a saving the session already enjoyed.
func withheld(meta claudecode.ResultMeta, shown int) int {
	if meta.PersistedBytes > shown {
		return meta.PersistedBytes - shown
	}
	return 0
}

// commandOf pulls the command line out of a Bash tool input.
func commandOf(input json.RawMessage) string {
	if len(input) == 0 || input[0] != '{' {
		return ""
	}
	var in struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return ""
	}
	return in.Command
}

// usageOf converts an API usage object, preferring the reported cache-creation
// total over the TTL split so that an inconsistency stays visible.
func usageOf(u *claudecode.Usage) model.TokenUsage {
	if u == nil {
		return model.TokenUsage{}
	}
	t := model.TokenUsage{
		Input:         u.InputTokens,
		CacheRead:     u.CacheReadInputTokens,
		CacheCreation: u.CacheCreationInputTokens,
		Output:        u.OutputTokens,
	}
	if u.CacheCreation != nil {
		t.CacheCreation5m = u.CacheCreation.Ephemeral5m
		t.CacheCreation1h = u.CacheCreation.Ephemeral1h
	}
	if u.OutputTokensDetails != nil {
		t.Thinking = u.OutputTokensDetails.ThinkingTokens
	}
	return t
}

// resolveTools reports calls that never got a result.
func resolveTools(s *model.Session, toolIndex map[string]int) {
	var unresolved int
	for _, tc := range s.ToolCalls {
		if !tc.Resolved {
			unresolved++
		}
	}
	if unresolved > 0 {
		s.Warn("unresolved_tool_calls", fmt.Sprintf(
			"%d tool calls have no result in this transcript (interrupted, denied, or still running)",
			unresolved))
	}
}

// diagnose records what the reader needs to know about the data itself.
func diagnose(s *model.Session) {
	if s.AssistantEntries > len(s.Invocations) {
		s.Warn("entries_collapsed", fmt.Sprintf(
			"%d assistant entries collapsed into %d API calls; summing per entry would overstate usage by %.0f%%",
			s.AssistantEntries, len(s.Invocations),
			100*(float64(s.AssistantEntries)/float64(max(len(s.Invocations), 1))-1)))
	}
	var zero, agentCalls int
	for _, inv := range s.Invocations {
		if inv.Usage.PromptTokens() == 0 {
			zero++
		}
		if !inv.Usage.TTLSplitConsistent() {
			s.Warn("ttl_split_mismatch",
				"a call's 5m/1h cache-creation split did not match its reported total; the cheaper write rate was applied")
		}
	}
	if zero > 0 {
		s.Warn("zero_prompt_calls", fmt.Sprintf(
			"%d calls reported no prompt tokens (API errors or synthetic entries) and are excluded from cost", zero))
	}
	for _, tc := range s.ToolCalls {
		if tc.Name == "Agent" {
			agentCalls++
		}
	}
	var images, imageBytes int
	for _, r := range s.Retrievals {
		images += r.Images
		imageBytes += r.ImageBytes
	}
	if images > 0 {
		s.Warn("image_tokens_not_estimated", fmt.Sprintf(
			"%d image results carried %d bytes of base64; images are priced by dimensions, "+
				"so their token cost is excluded from the byte-ratio estimate rather than guessed",
			images, imageBytes))
	}
	if agentCalls > 0 && !hasSidechain(s) {
		s.Warn("subagent_usage_missing", fmt.Sprintf(
			"%d Agent calls were made but no sidechain invocations are present; subagent token usage is not in this transcript",
			agentCalls))
	}
}

func hasSidechain(s *model.Session) bool {
	for _, inv := range s.Invocations {
		if inv.Sidechain {
			return true
		}
	}
	return false
}

// estimate calibrates a token estimator against the session's own observed
// prompt growth and applies it to every retrieval.
//
// Two passes are needed because the calibration depends on the whole session:
// per-call prompt sizes are observed, so the growth between calls is the
// evidence, and the fit is only as good as the session that produced it.
func estimate(s *model.Session) {
	byInvocation := map[int]int{}
	for _, r := range s.Retrievals {
		byInvocation[r.InvocationSeq] += r.Bytes
	}

	var samples []tokens.Sample
	for k := 1; k < len(s.Invocations); k++ {
		prev, cur := s.Invocations[k-1], s.Invocations[k]
		// Error entries are not requests, so the growth across them is not
		// content arriving; nor is the growth across a model switch, which
		// rebuilds the whole prefix.
		if !prev.IsRealCall() || !cur.IsRealCall() || prev.Model != cur.Model {
			continue
		}
		samples = append(samples, tokens.Sample{
			PromptGrowth: cur.Usage.PromptTokens() - prev.Usage.PromptTokens(),
			OutputTokens: prev.Usage.Output,
			ResultBytes:  byInvocation[prev.Seq],
		})
	}

	ratio := tokens.Calibrate(samples)
	s.Estimator = model.TokenEstimator{
		Method:          ratio.Describe(),
		BytesPerToken:   ratio.BytesPerToken,
		PerCallOverhead: ratio.PerCallOverhead,
		Residual:        ratio.Residual,
		Calibrated:      ratio.Calibrated,
		Samples:         ratio.Samples,
	}
	for i := range s.Retrievals {
		s.Retrievals[i].Tokens = ratio.Count(s.Retrievals[i].Bytes)
		s.Retrievals[i].TokensProv = ratio.Provenance()
	}
}

// findRepeats groups byte-identical retrievals. This is the strongest
// counterfactual available: content fetched twice could have been fetched
// once, which is arithmetic rather than modelling.
func findRepeats(s *model.Session) {
	type group struct {
		r     model.RetrievedContent
		count int
		seqs  []int
		items []int
	}
	groups := map[string]*group{}
	var order []string
	for _, r := range s.Retrievals {
		g, ok := groups[r.Hash]
		if !ok {
			g = &group{r: r}
			groups[r.Hash] = g
			order = append(order, r.Hash)
		}
		g.count++
		g.seqs = append(g.seqs, r.InvocationSeq)
		g.items = append(g.items, r.Seq)
	}
	for _, h := range order {
		g := groups[h]
		if g.count < 2 {
			continue
		}
		size := g.r.ObservedBytes()
		s.Repeats = append(s.Repeats, model.Repeat{
			Hash:          h,
			Category:      g.r.Category,
			Path:          g.r.Path,
			Tool:          g.r.Tool,
			Count:         g.count,
			Bytes:         size,
			ImageBytes:    g.r.ImageBytes,
			WasteByte:     size * (g.count - 1),
			Seqs:          g.seqs,
			RetrievalSeqs: g.items,
		})
	}
	sort.SliceStable(s.Repeats, func(i, j int) bool {
		return s.Repeats[i].WasteByte > s.Repeats[j].WasteByte
	})
}
