package content

import (
	"testing"

	"github.com/ctford/tokenamun/internal/model"
)

func TestIsFileContentPartitionsTheShellCorrectly(t *testing.T) {
	// One rule serves both the viewer's "file content" branch and the
	// file-compression intervention, so these cases are the contract between
	// them.
	cases := []struct {
		name string
		c    model.RetrievedContent
		want bool
	}{{
		name: "the Read tool",
		c:    model.RetrievedContent{Channel: model.ChanFileRead, Path: "a.go"},
		want: true,
	}, {
		name: "a tool that returned a document",
		c:    model.RetrievedContent{Channel: model.ChanOtherTool, Path: "docs/plan.md"},
		want: true,
	}, {
		name: "a tool that returned no document",
		c:    model.RetrievedContent{Channel: model.ChanOtherTool},
		want: false,
	}, {
		name: "sed reading a file",
		c:    model.RetrievedContent{Channel: model.ChanShell, CommandBinary: "sed", Path: "a.go"},
		want: true,
	}, {
		name: "sed reading a file the path could not be recovered for",
		c:    model.RetrievedContent{Channel: model.ChanShell, CommandBinary: "sed"},
		want: true,
	}, {
		name: "head downstream of a pipe is shaping output, not reading",
		c: model.RetrievedContent{Channel: model.ChanShell, CommandBinary: "head",
			PipelineFilter: true},
		want: false,
	}, {
		name: "a test run",
		c:    model.RetrievedContent{Channel: model.ChanShell, CommandBinary: "go"},
		want: false,
	}, {
		name: "an MCP result, whatever it mentions",
		c:    model.RetrievedContent{Channel: model.ChanMCP, Path: "a.go"},
		want: false,
	}, {
		name: "an edit confirmation",
		c:    model.RetrievedContent{Channel: model.ChanEdit, Path: "a.go"},
		want: false,
	}, {
		name: "a fetched web page",
		c:    model.RetrievedContent{Channel: model.ChanWeb, Path: "index.html"},
		want: false,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsFileContent(tc.c); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
