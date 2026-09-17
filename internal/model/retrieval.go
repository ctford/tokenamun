package model

// Category classifies retrieved content. Categories are deliberately about
// what the content *is*, not which tool fetched it, because the same tool
// fetches very different things.
type Category string

const (
	CatSourceCode    Category = "source code"
	CatTest          Category = "tests"
	CatADR           Category = "adrs"
	CatSpecification Category = "specifications"
	CatPlan          Category = "plans"
	CatDocumentation Category = "documentation"
	CatInstructions  Category = "instructions"
	CatMixed         Category = "mixed"
	CatToolOutput    Category = "tool output"
	CatMCPOutput     Category = "mcp output"
	CatOther         Category = "other"
)

// Categories lists every category in report order.
func Categories() []Category {
	return []Category{
		CatSourceCode, CatTest, CatADR, CatSpecification, CatPlan,
		CatDocumentation, CatInstructions, CatMixed, CatToolOutput, CatMCPOutput, CatOther,
	}
}

// Channel is how content reached the context. It answers a different question
// from Category: a decision record read through `cat` is an ADR by category
// and file reading by channel, and both are worth seeing.
type Channel string

const (
	ChanFileRead   Channel = "Read tool"
	ChanShell      Channel = "shell output"
	ChanWeb        Channel = "web"
	ChanSubagent   Channel = "subagents"
	ChanEdit       Channel = "edits"
	ChanMCP        Channel = "mcp"
	ChanOtherTool  Channel = "other tools"
	ChanUserPrompt Channel = "prompts"
)

// Channels lists every channel in report order.
func Channels() []Channel {
	return []Channel{
		ChanFileRead, ChanShell, ChanWeb, ChanSubagent,
		ChanEdit, ChanMCP, ChanOtherTool, ChanUserPrompt,
	}
}

// RetrievedContent is one payload that entered the model's context as the
// result of a tool call.
//
// Bytes is measured from the tool_result block in the user message, which is
// what the model actually received. It is deliberately NOT taken from the
// transcript's richer toolUseResult field: on real sessions those disagree by
// more than an order of magnitude, because Claude Code spills large output to
// a file and passes the model only an excerpt. toolUseResult is used for
// attribution metadata -- path, line range, truncation -- and never for size.
type RetrievedContent struct {
	Seq      int      `json:"seq"`
	ToolID   string   `json:"tool_id"`
	Tool     string   `json:"tool"`
	Category Category `json:"category"`
	// Channel is how this content arrived, independent of what it is.
	Channel Channel `json:"channel"`
	// CommandClass groups shell commands by what they do -- tests, git,
	// exploration -- so shell output can be drilled into meaningfully.
	CommandClass string `json:"command_class,omitempty"`
	// CommandDetail is the specific command within that class, so git can be
	// opened up into git status, git diff and the rest.
	CommandDetail string `json:"command_detail,omitempty"`
	// CategoryProv is derived when the path was observed in the tool result,
	// and inferred when it was parsed out of a shell command line.
	CategoryProv Provenance `json:"category_provenance"`
	// Declared is true when the category came from a configured subtree rather
	// than from a guess about directory naming.
	Declared bool   `json:"declared,omitempty"`
	Path     string `json:"path,omitempty"`
	// Paths lists every path the content was attributed to. A compound shell
	// command can read several files in one result, and pretending otherwise
	// would either lose content or invent precision about which file it came
	// from.
	Paths []string `json:"paths,omitempty"`
	// Bytes is the observed size of what entered context.
	Bytes int `json:"bytes"`
	// Tokens is an estimate unless a real tokenizer was used; Prov says which.
	Tokens     float64    `json:"tokens"`
	TokensProv Provenance `json:"tokens_provenance"`
	// Hash identifies the content so repeated retrieval can be detected.
	Hash string `json:"hash"`
	// InvocationSeq is the API call that carried this result, which is what
	// makes cost-of-carry computable later.
	InvocationSeq int `json:"invocation_seq"`

	// Line range, when the tool reported one. A partial read is counted as a
	// partial read, never as the whole file.
	StartLine  int  `json:"start_line,omitempty"`
	Lines      int  `json:"lines,omitempty"`
	TotalLines int  `json:"total_lines,omitempty"`
	Partial    bool `json:"partial"`

	// Images counts image blocks in the result, and ImageBytes the base64 they
	// carried. Both are observed. Image token cost is NOT estimated from those
	// bytes, because an image is priced by its dimensions -- a byte ratio
	// would overstate a screenshot by more than an order of magnitude.
	Images     int `json:"images,omitempty"`
	ImageBytes int `json:"image_bytes,omitempty"`

	// Truncated records that the harness withheld content from the model.
	Truncated bool `json:"truncated"`
	// WithheldBytes is how much the harness kept out of context, observed from
	// a persisted-output size. This is content you did not pay for.
	WithheldBytes int  `json:"withheld_bytes,omitempty"`
	IsError       bool `json:"is_error"`
}

// ObservedBytes is everything that entered the context for this retrieval,
// text and image payload together. Use it for size and redundancy; use Bytes
// for anything that feeds a byte-per-token estimate, because image bytes must
// not.
func (r RetrievedContent) ObservedBytes() int {
	return r.Bytes + r.ImageBytes
}

// Repeat describes content retrieved more than once in a session.
type Repeat struct {
	Hash     string   `json:"hash"`
	Category Category `json:"category"`
	Path     string   `json:"path,omitempty"`
	Tool     string   `json:"tool"`
	Count    int      `json:"count"`
	Bytes    int      `json:"bytes"`
	// ImageBytes is how much of Bytes was image payload. Re-sending an image
	// costs image tokens again, so the repeat is real; it just cannot be
	// priced by a byte ratio.
	ImageBytes int   `json:"image_bytes,omitempty"`
	WasteByte  int   `json:"redundant_bytes"`
	Seqs       []int `json:"invocation_seqs"`
	// RetrievalSeqs identifies the retrievals themselves, in order. Joining on
	// path cannot work: most retrieved content is shell output and has none.
	RetrievalSeqs []int `json:"retrieval_seqs"`
}
