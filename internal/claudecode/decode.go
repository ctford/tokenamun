// Package claudecode decodes Claude Code's native JSONL transcript. It is one
// of only two packages permitted to know a field name from someone else's
// format (see internal/entire for the other); everything downstream consumes
// internal/model types.
package claudecode

import (
	"encoding/json"
	"strings"
	"time"
)

// Entry is one line of a transcript. A transcript line is NOT an API call: an
// assistant message is split across one entry per content block, all repeating
// the same usage. See internal/ingest for the deduplication that matters.
type Entry struct {
	Type          string          `json:"type"`
	UUID          string          `json:"uuid"`
	ParentUUID    string          `json:"parentUuid"`
	SessionID     string          `json:"sessionId"`
	RequestID     string          `json:"requestId"`
	Timestamp     string          `json:"timestamp"`
	Version       string          `json:"version"`
	Effort        string          `json:"effort"`
	GitBranch     string          `json:"gitBranch"`
	CWD           string          `json:"cwd"`
	IsSidechain   bool            `json:"isSidechain"`
	IsMeta        bool            `json:"isMeta"`
	Message       *Message        `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
	ToolUseID     string          `json:"toolUseID"`
	Subtype       string          `json:"subtype"`
}

// Time parses the entry timestamp. A missing or malformed timestamp yields the
// zero time, which callers must treat as unknown rather than as the epoch.
func (e Entry) Time() time.Time {
	if e.Timestamp == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, e.Timestamp)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Message is the API message carried by an assistant or user entry.
type Message struct {
	ID      string  `json:"id"`
	Role    string  `json:"role"`
	Model   string  `json:"model"`
	Usage   *Usage  `json:"usage"`
	Content Content `json:"content"`
}

// Usage mirrors the Messages API usage object.
type Usage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	OutputTokensDetails      *struct {
		ThinkingTokens int64 `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
	CacheCreation *struct {
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

// Block is one content block. Tool results carry their payload inline, and
// that payload is itself either a string or a list of blocks.
type Block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   Content         `json:"content"`
}

// Content is a message body, which the transcript writes either as a bare
// string or as a list of blocks. Decoding both into one shape here keeps the
// branch out of every consumer.
type Content struct {
	Text   string
	Blocks []Block
}

// UnmarshalJSON accepts a string, a block list, or null.
func (c *Content) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		return json.Unmarshal(b, &c.Text)
	}
	if b[0] == '[' {
		return json.Unmarshal(b, &c.Blocks)
	}
	// An object body is not a shape we have seen; record it as text so its
	// size is still counted rather than silently dropped.
	c.Text = string(b)
	return nil
}

// Len returns the byte length of the content as it appeared, which is what a
// retrieved-content measurement needs.
func (c Content) Len() int {
	n := len(c.Text)
	for _, b := range c.Blocks {
		n += len(b.Text) + b.Content.Len()
	}
	return n
}

// String flattens the content to the text that entered the context. Used for
// hashing, so that identical retrievals can be recognised.
func (c Content) String() string {
	if len(c.Blocks) == 0 {
		return c.Text
	}
	var b strings.Builder
	b.WriteString(c.Text)
	for _, blk := range c.Blocks {
		b.WriteString(blk.Text)
		b.WriteString(blk.Content.String())
	}
	return b.String()
}

// ToolUses returns the tool calls in an assistant message.
func (m *Message) ToolUses() []Block {
	if m == nil {
		return nil
	}
	var out []Block
	for _, b := range m.Content.Blocks {
		if b.Type == "tool_use" {
			out = append(out, b)
		}
	}
	return out
}

// ToolResults returns the tool results in a user message.
func (m *Message) ToolResults() []Block {
	if m == nil {
		return nil
	}
	var out []Block
	for _, b := range m.Content.Blocks {
		if b.Type == "tool_result" {
			out = append(out, b)
		}
	}
	return out
}
