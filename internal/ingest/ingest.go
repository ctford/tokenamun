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
	"strings"

	"github.com/ctford/tokenamun/internal/claudecode"
	"github.com/ctford/tokenamun/internal/model"
)

// maxLine is generous enough for a transcript line carrying a large tool
// result. Lines above it are reported rather than silently truncated.
const maxLine = 64 << 20

// Load parses the transcript a ref points at.
func Load(ref model.SessionRef) (*model.Session, error) {
	f, err := os.Open(ref.Transcript)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f, ref)
}

// Parse reads a transcript from r.
//
// A transcript that is still being appended to can end in a partial line, so
// an unparseable final line is tolerated and reported as a warning. An
// unparseable line anywhere else is also survivable -- one bad line should not
// cost the whole profile -- but it is counted.
func Parse(r io.Reader, ref model.SessionRef) (*model.Session, error) {
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
			for _, b := range results {
				absorbResult(s, b, toolIndex)
			}
			return
		}
		// A user entry that is not carrying tool results is a prompt. isMeta
		// marks harness-injected content rather than something a human typed.
		if !e.IsMeta {
			s.Prompts++
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
		})
	}
}

// absorbResult attaches an observed tool result to its call.
func absorbResult(s *model.Session, b claudecode.Block, toolIndex map[string]int) {
	idx, ok := toolIndex[b.ToolUseID]
	if !ok {
		// A result with no matching call: the call may predate a resumed
		// transcript. Recorded so the bytes are not lost.
		s.ToolCalls = append(s.ToolCalls, model.ToolCall{
			Seq:           len(s.ToolCalls),
			ID:            b.ToolUseID,
			Name:          "(unpaired)",
			ResultBytes:   b.Content.Len(),
			IsError:       b.IsError,
			Resolved:      true,
			InvocationSeq: -1,
		})
		return
	}
	s.ToolCalls[idx].ResultBytes = b.Content.Len()
	s.ToolCalls[idx].IsError = b.IsError
	s.ToolCalls[idx].Resolved = true
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
