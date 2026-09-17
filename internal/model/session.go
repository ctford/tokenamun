package model

import "time"

// Origin records where a session's transcript came from. The two sources carry
// different evidence, so it is not a cosmetic field: Entire adds checkpoints,
// git attribution and files_touched that a local transcript does not have.
type Origin string

const (
	// FromEntire is a session recorded by Entire inside a repository.
	FromEntire Origin = "entire"
	// FromLocal is a Claude Code session read from ~/.claude/projects.
	FromLocal Origin = "local"
)

// SessionRef is a transcript we know about but have not yet parsed.
type SessionRef struct {
	ID         string    `json:"id"`
	Transcript string    `json:"transcript"`
	Origin     Origin    `json:"origin"`
	Repo       string    `json:"repo,omitempty"`
	Modified   time.Time `json:"modified"`
	// Current is true when this is the session the tool is running inside,
	// which means the transcript is still being appended to.
	Current bool `json:"current"`
}

// ModelInvocation is one API call. Building these correctly -- one per
// requestId, not one per transcript line -- is the tool's central correctness
// rule. See METHODOLOGY.md section 2.
type ModelInvocation struct {
	Seq       int        `json:"seq"`
	RequestID string     `json:"request_id"`
	MessageID string     `json:"message_id"`
	Model     string     `json:"model"`
	Version   string     `json:"version"`
	Effort    string     `json:"effort"`
	Timestamp time.Time  `json:"timestamp"`
	Sidechain bool       `json:"sidechain"`
	Usage     TokenUsage `json:"usage"`
	// Entries is how many transcript lines collapsed into this call. Greater
	// than one is normal and is why naive summing overstates.
	Entries int `json:"entries"`
}

// SyntheticModel is the marker Claude Code writes when an entry stands in for
// a failed request rather than recording a real one.
const SyntheticModel = "<synthetic>"

// IsRealCall reports whether this invocation was an actual API request.
//
// It matters for any comparison between consecutive calls: an error entry
// carries no prompt and a placeholder model, so treating it as the predecessor
// makes the next real call look like a model switch when nothing switched.
func (m ModelInvocation) IsRealCall() bool {
	return m.Usage.PromptTokens() > 0 && m.Model != SyntheticModel
}

// ToolCall pairs a tool invocation with its result.
type ToolCall struct {
	Seq           int    `json:"seq"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	InputBytes    int    `json:"input_bytes"`
	ResultBytes   int    `json:"result_bytes"`
	IsError       bool   `json:"is_error"`
	Resolved      bool   `json:"resolved"`
	InvocationSeq int    `json:"invocation_seq"`
	// Command is the shell command line, when the tool was Bash. Kept because
	// file reads go through the shell in auto mode, so it is the only way to
	// attribute that output to a path.
	Command string `json:"command,omitempty"`
}

// PromptEntry is one thing the user typed.
type PromptEntry struct {
	Bytes int `json:"bytes"`
	// InvocationSeq is the call that first carried it.
	InvocationSeq int `json:"invocation_seq"`
}

// Warning is something the reader needs to know about the data rather than
// about the session. Warnings are reported, never swallowed.
type Warning struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// Session is a parsed transcript in normalized form.
// Session is also a published wire format: it is most of the evidence
// document an external intervention reads on stdin, so the collection fields
// are serialised even when empty. A script that indexes into
// evidence.session.retrievals should find an empty list, not a missing key.
type Session struct {
	Ref         SessionRef         `json:"ref"`
	Invocations []ModelInvocation  `json:"invocations"`
	ToolCalls   []ToolCall         `json:"tool_calls"`
	Retrievals  []RetrievedContent `json:"retrievals"`
	Repeats     []Repeat           `json:"repeats"`
	Prompts     int                `json:"user_prompts"`
	// PromptEntries record each user prompt's size and where it entered, so
	// what you typed can be carried like anything else.
	PromptEntries []PromptEntry `json:"prompt_entries"`
	// ProseBytes is assistant text, excluding thinking and tool arguments.
	// Output tokens are observed in total but not broken down, so the split
	// between prose and tool arguments is apportioned by these byte counts.
	ProseBytes int       `json:"prose_bytes"`
	Branch     string    `json:"branch,omitempty"`
	CWD        string    `json:"cwd,omitempty"`
	Warnings   []Warning `json:"warnings"`
	// TranscriptLines and AssistantEntries support the dedup diagnostic.
	TranscriptLines  int            `json:"transcript_lines"`
	AssistantEntries int            `json:"assistant_entries"`
	Estimator        TokenEstimator `json:"token_estimator"`
	// ClassifierSource says where content categories came from, so a reader
	// can tell a declared layout from a guess at naming.
	ClassifierSource string `json:"classifier_source,omitempty"`
}

// TokenEstimator records how content token counts were arrived at, so the
// report can print the method next to the numbers.
type TokenEstimator struct {
	Method          string  `json:"method"`
	BytesPerToken   float64 `json:"bytes_per_token"`
	PerCallOverhead float64 `json:"per_call_overhead_tokens"`
	Residual        float64 `json:"unattributed_share"`
	Calibrated      bool    `json:"calibrated"`
	Samples         int     `json:"samples"`
}

// Usage totals the session's deduplicated invocations.
func (s *Session) Usage() TokenUsage {
	var t TokenUsage
	for _, inv := range s.Invocations {
		t = t.Add(inv.Usage)
	}
	return t
}

// Models lists the distinct models used, in first-seen order. A session with
// more than one has paid for at least one cache-invalidating model switch.
func (s *Session) Models() []string {
	var out []string
	seen := map[string]bool{}
	for _, inv := range s.Invocations {
		if inv.Model != "" && !seen[inv.Model] {
			seen[inv.Model] = true
			out = append(out, inv.Model)
		}
	}
	return out
}

// Warn appends a warning.
func (s *Session) Warn(code, detail string) {
	s.Warnings = append(s.Warnings, Warning{Code: code, Detail: detail})
}

// Duration is the span between the first and last observed API call.
func (s *Session) Duration() time.Duration {
	var first, last time.Time
	for _, inv := range s.Invocations {
		if inv.Timestamp.IsZero() {
			continue
		}
		if first.IsZero() || inv.Timestamp.Before(first) {
			first = inv.Timestamp
		}
		if inv.Timestamp.After(last) {
			last = inv.Timestamp
		}
	}
	if first.IsZero() {
		return 0
	}
	return last.Sub(first)
}
