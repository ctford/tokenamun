package whatif

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/ctford/tokenamun/internal/claudecode"
	"github.com/ctford/tokenamun/internal/content"
	"github.com/ctford/tokenamun/internal/model"
)

// ReplayResult is what a real compressor did to this session's own content.
//
// This is the difference between an estimate and a measurement. Published
// compression figures vary by a factor of seven between the vendor's benchmark
// and an independent test, and both were measured on someone else's content.
// Piping a given repository's observed output through the actual tool is the
// only way to know which number applies to it.
type ReplayResult struct {
	Command string `json:"command"`
	// Scope says which content was measured, because a ratio is only valid
	// over the set it was measured on. Tool output and file content compress
	// differently -- one is repetitive machine chatter, the other is source
	// somebody wrote -- so a single number for both would be wrong for each.
	Scope       string `json:"scope"`
	Items       int    `json:"items"`
	InputBytes  int    `json:"input_bytes"`
	OutputBytes int    `json:"output_bytes"`
	Failures    int    `json:"failures"`
}

// Ratio is the surviving fraction of the replayed content.
func (r ReplayResult) Ratio() float64 {
	if r.InputBytes == 0 {
		return 1
	}
	return float64(r.OutputBytes) / float64(r.InputBytes)
}

// Replay streams a session's eligible tool output through an external command,
// one payload per invocation, and measures what comes back.
//
// The command reads the payload on stdin and writes the compressed form to
// stdout, which is the shape every compressor in this space already offers.
// A command that fails on a payload is counted rather than aborting the run:
// a compressor that cannot handle some input is a finding about the
// compressor, and hiding it would flatter the result.
func Replay(ref model.SessionRef, command string) (*ReplayResult, error) {
	return replay(ref, command, "tool output", eligible)
}

// ReplayFiles measures the same compressor against the file content this
// session read, which is a different question with a different answer: what
// would shrinking the files themselves have been worth.
func ReplayFiles(ref model.SessionRef, command string) (*ReplayResult, error) {
	return replay(ref, command, "file content", eligibleFile)
}

func replay(ref model.SessionRef, command, scope string,
	want func(name, command string, meta claudecode.ResultMeta) bool) (*ReplayResult, error) {
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("no replay command given")
	}
	f, err := os.Open(ref.Transcript)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	result := &ReplayResult{Command: command, Scope: scope}
	names := map[string]string{}
	commands := map[string]string{}

	dec := json.NewDecoder(f)
	for {
		var e claudecode.Entry
		if err := dec.Decode(&e); err != nil {
			break // including a partial final line on a running session
		}
		switch e.Type {
		case "assistant":
			for _, b := range e.Message.ToolUses() {
				names[b.ID] = b.Name
				if cmd := commandOf(b.Input); cmd != "" {
					commands[b.ID] = cmd
				}
			}
		case "user":
			if e.Message == nil {
				continue
			}
			for _, b := range e.Message.ToolResults() {
				// Eligibility must match the estimator exactly, or the
				// measured ratio would describe a different set of content
				// than the one the saving is applied to.
				meta := claudecode.ParseResultMeta(e.ToolUseResult)
				if !want(names[b.ToolUseID], commands[b.ToolUseID], meta) {
					continue
				}
				payload := b.Content.String()
				if payload == "" {
					continue
				}
				out, err := pipe(command, payload)
				if err != nil {
					result.Failures++
					continue
				}
				result.Items++
				result.InputBytes += len(payload)
				result.OutputBytes += len(out)
			}
		}
	}
	if result.Items == 0 {
		return result, fmt.Errorf("no eligible %s was replayed successfully (%d failures)",
			scope, result.Failures)
	}
	return result, nil
}

// eligible mirrors the estimator's eligibility: content that classifies as
// tool or MCP output. A shell command that read a file is excluded, because
// the estimator classifies that as source rather than output, and a ratio
// measured over a different set than the saving is applied to would be
// quietly wrong.
func eligible(name, command string, meta claudecode.ResultMeta) bool {
	if strings.HasPrefix(name, "mcp__") {
		return true
	}
	switch name {
	case "Bash", "BashOutput", "WebFetch", "WebSearch":
	default:
		return false
	}
	if meta.Path != "" {
		return false
	}
	return len(content.PathsFromCommand(command)) == 0
}

// eligibleFile is the complement of eligible over the content a compressor
// could be pointed at: a payload that is the contents of a file, whether it
// arrived through the Read tool or through a shell command that read one.
//
// Kept beside eligible on purpose. The two must not overlap, or a saving would
// be counted twice across the two interventions that use them.
func eligibleFile(name, command string, meta claudecode.ResultMeta) bool {
	if meta.Path != "" {
		return true
	}
	switch name {
	case "Read", "NotebookRead":
		return true
	case "Bash", "BashOutput":
		return len(content.PathsFromCommand(command)) > 0
	default:
		return false
	}
}

// commandOf pulls the command line out of a tool input.
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

// pipe runs the command with payload on stdin and returns stdout.
func pipe(command, payload string) ([]byte, error) {
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdin = strings.NewReader(payload)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return out.Bytes(), nil
}
