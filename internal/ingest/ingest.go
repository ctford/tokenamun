// Package ingest turns a transcript into a normalized model.Session.
//
// Its central job is the deduplication in docs/METHODOLOGY.md section 2: a Claude
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
	"path/filepath"
	"sort"
	"strings"

	"github.com/ctford/tokenamun/internal/claudecode"
	"github.com/ctford/tokenamun/internal/content"
	"github.com/ctford/tokenamun/internal/entire"
	"github.com/ctford/tokenamun/internal/model"
	"github.com/ctford/tokenamun/internal/tokens"
)

// maxLine is generous enough for a transcript line carrying a large tool
// result. Lines above it are reported rather than silently truncated.
const maxLine = 64 << 20

// Options configures ingestion. Retained as an extension point; there is
// nothing to configure at present.
type Options struct{}

// Load parses the transcript a ref points at, using the built-in classifier.
func Load(ref model.SessionRef) (*model.Session, error) {
	return LoadWith(ref, Options{})
}

// LoadWith parses the transcript a ref points at.
func LoadWith(ref model.SessionRef, opts Options) (*model.Session, error) {
	open := func() (io.ReadCloser, error) { return os.Open(ref.Transcript) }
	if ref.InGit {
		// A transcript inside a checkpoint commit. Streamed out of git
		// rather than extracted to a file first: these reach 9 MB and
		// nothing here loads a whole one.
		open = func() (io.ReadCloser, error) {
			return entire.OpenBlob(ref.Repo, ref.Transcript)
		}
	}
	f, err := open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only: nothing to flush, nothing to lose
	s, err := ParseWith(f, ref, opts)
	if err != nil {
		return nil, err
	}
	loadSubagents(s, ref, opts)
	return s, nil
}

// Sources lists every file on disk that LoadWith reads for a ref.
//
// It exists so that a cache can key on all of them rather than only on the
// transcript it was handed: a session's parse also takes in the subagent
// transcripts beside it, and one of those can be written after its parent's
// last line. Nothing for an Entire recording, which is a single committed
// blob named by an immutable ref, with no siblings to find.
//
// Next to loadSubagents deliberately. The two have to agree about which files
// a parse depends on, and the way to keep them agreeing is to keep them
// where the next person changes both.
func Sources(ref model.SessionRef) []string {
	if ref.InGit || ref.Transcript == "" {
		return nil
	}
	return append([]string{ref.Transcript}, claudecode.SubagentTranscripts(ref.Transcript)...)
}

// loadSubagents parses the transcripts of the subagents a session launched
// and attaches their spend to it.
//
// Only for local transcripts. An Entire recording is a single committed blob
// with no sibling files, so there is nothing to look for -- and looking would
// mean a git lookup per session on the "all" path.
//
// A subagent transcript that will not parse is dropped with a warning rather
// than failing the session. The parent's numbers are still correct and still
// worth having; what is lost is an addendum to them, and refusing to report
// anything would be a worse trade than reporting most of it and saying so.
func loadSubagents(s *model.Session, ref model.SessionRef, opts Options) {
	if ref.InGit || ref.Transcript == "" {
		return
	}
	for _, path := range claudecode.SubagentTranscripts(ref.Transcript) {
		f, err := os.Open(path)
		if err != nil {
			s.Warn("subagent_unreadable", fmt.Sprintf(
				"a subagent transcript could not be opened, so its spend is missing from this profile: %v", err))
			continue
		}
		// Parsed as its own session, because that is what it is: its own
		// context, its own requestId space, its own dedup.
		sub, err := ParseWith(f, model.SessionRef{
			ID:         claudecode.SubagentID(path),
			Transcript: path,
			Origin:     ref.Origin,
			Repo:       ref.Repo,
		}, opts)
		_ = f.Close()
		if err != nil {
			s.Warn("subagent_unreadable", fmt.Sprintf(
				"subagent transcript %s could not be parsed, so its spend is missing from this profile",
				claudecode.SubagentID(path)))
			continue
		}
		s.Subagents = append(s.Subagents, model.SubagentRun{
			ID:          sub.Ref.ID,
			Transcript:  path,
			Invocations: sub.Invocations,
			ToolCalls:   len(sub.ToolCalls),
		})
	}
	if len(s.Subagents) > 0 {
		retract(s, "subagent_usage_missing")
	}
}

// retract removes a warning that later work proved wrong.
//
// Diagnosis runs inside ParseWith, over one transcript, and from there an
// Agent call with no sidechain really does look like spend that cannot be
// seen -- which is the right conclusion for a caller parsing a bare stream,
// where there are no sibling files to find. Only LoadWith knows there is a
// directory to look in, and it looks after parsing. So the warning is raised
// on what was known and withdrawn once it is not true.
//
// Withdrawn rather than never raised, because the stream case still needs
// it: Parse and ParseWith are the entry points for a transcript that has no
// path, and there the spend genuinely is invisible.
func retract(s *model.Session, code string) {
	kept := s.Warnings[:0]
	for _, w := range s.Warnings {
		if w.Code != code {
			kept = append(kept, w)
		}
	}
	s.Warnings = kept
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
		absorb(s, e, byRequest, toolIndex)
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
	relativise(s)
	estimate(s)
	findRepeats(s)
	diagnose(s)
	return s, nil
}

// absorb folds one transcript entry into the session.
func absorb(s *model.Session, e claudecode.Entry, byRequest, toolIndex map[string]int) {
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
				absorbResult(s, b, meta, toolIndex)
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
func absorbResult(s *model.Session, b claudecode.Block, meta claudecode.ResultMeta, toolIndex map[string]int) {
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
	filePath, prov := attributePath(tc.Command, meta)
	s.Retrievals = append(s.Retrievals, model.RetrievedContent{
		Seq:            len(s.Retrievals),
		ToolID:         tc.ID,
		Tool:           tc.Name,
		Channel:        content.ChannelFor(tc.Name, meta.Path != ""),
		CommandClass:   content.CommandClass(tc.Command),
		CommandDetail:  content.CommandDetail(tc.Command),
		CommandBinary:  content.CommandBinary(tc.Command),
		PipelineFilter: content.IsPipelineFilter(tc.Command),
		PathProv:       prov,
		Path:           filePath,
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
// attributePath works out which file a payload came from.
//
// A path the tool reported is observed, so using it is derived. A path parsed
// out of a shell command line is a guess about what the command read, so it
// is inferred. Content with no attributable path keeps none rather than being
// assigned one.
func attributePath(command string, meta claudecode.ResultMeta) (string, model.Provenance) {
	if meta.Path != "" {
		return meta.Path, model.Derived
	}
	if command != "" {
		if paths := content.PathsFromCommand(command); len(paths) > 0 {
			return paths[0], model.Inferred
		}
	}
	return "", model.Observed
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
	// Two things this has to get right, and it got both wrong.
	//
	// The call count is RealCalls, the same way the report's own "API calls"
	// line counts. Error and synthetic entries carry no prompt and are
	// excluded from every cost figure, so counting them here printed two
	// different totals for "API calls" on one page of output.
	//
	// The overstatement is measured rather than approximated by the ratio of
	// entries to calls. Every entry sharing a requestId repeats the same
	// usage object, so what summing per entry would have produced is each
	// call's usage times its entry count -- which is arithmetic over observed
	// numbers, not a proxy. The proxy was wrong in both directions: it
	// counted zero-usage entries as though they inflated the total, and it
	// weighted a two-entry call the same as a twelve-entry one.
	if calls := s.RealCalls(); s.AssistantEntries > calls {
		var naive, deduplicated float64
		for _, inv := range s.Invocations {
			if !inv.IsRealCall() {
				continue
			}
			usage := float64(inv.Usage.PromptTokens() + inv.Usage.Output)
			deduplicated += usage
			naive += usage * float64(max(inv.Entries, 1))
		}
		if deduplicated > 0 {
			s.Warn("entries_collapsed", fmt.Sprintf(
				"%d assistant entries collapsed into %d API calls; summing per entry would overstate usage by %.0f%%",
				s.AssistantEntries, calls, 100*(naive/deduplicated-1)))
		}
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
	// Two different situations used to share one warning, and the one that
	// mattered was the one it described wrongly. Subagent spend is recorded
	// in sibling transcripts, so "not in this transcript" was true and read
	// as unmeasurable. It is only really missing when those files are gone
	// too -- which happens, because cleanup deletes them on the same
	// schedule as everything else.
	if agentCalls > 0 && len(s.Subagents) == 0 && !hasSidechain(s) {
		s.Warn("subagent_usage_missing", fmt.Sprintf(
			"%d Agent calls were made, and neither sidechain invocations nor subagent "+
				"transcripts were found; their token usage is not counted here",
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

// relativise rewrites retrieval paths to be relative to the repository the
// session ran in.
//
// Two reasons, and the second is the one that matters. A tree of file content
// rooted at /Users/<someone> spends its first two levels on directories that
// are the same for every file, so the drill-down starts one useful level deep.
// And an absolute path carries the name of whoever ran the session: these
// reports get pasted into issues and talks, and a profiler should not be the
// thing that publishes a home directory.
//
// A path outside the repository keeps its shape but loses the home directory,
// because where it is relative to the work is the informative part.
func relativise(s *model.Session) {
	if s.CWD == "" {
		return
	}
	home, _ := os.UserHomeDir()
	shorten := func(p string) string {
		if p == "" || !filepath.IsAbs(p) {
			return p
		}
		if rel, err := filepath.Rel(s.CWD, p); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
		if home != "" && strings.HasPrefix(p, home+string(filepath.Separator)) {
			return "~" + p[len(home):]
		}
		return p
	}
	for i := range s.Retrievals {
		s.Retrievals[i].Path = shorten(s.Retrievals[i].Path)
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
