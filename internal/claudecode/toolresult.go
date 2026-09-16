package claudecode

import "encoding/json"

// ResultMeta is the attribution metadata a tool reports about its own result.
//
// It is NOT the measure of what entered context -- see
// model.RetrievedContent.Bytes for why. Shapes below were read off real
// transcripts (Claude Code 2.1.x):
//
//	Bash   stdout, stderr, interrupted, isImage, noOutputExpected,
//	       sometimes persistedOutputPath + persistedOutputSize
//	Read   file: {content, filePath, numLines, startLine, totalLines}
//	       or file: {base64, dimensions, originalSize, type} for images
//	Edit   filePath, oldString, newString, structuredPatch, replaceAll
//	Write  content, filePath, structuredPatch, type
type ResultMeta struct {
	Path string
	// StartLine, Lines and TotalLines describe the fragment returned, so a
	// range read is not counted as a whole file.
	StartLine  int
	Lines      int
	TotalLines int
	// PersistedBytes is output the harness wrote to a file instead of putting
	// it in context. Observed, and worth reporting: it is content the session
	// did not pay to carry.
	PersistedBytes int
	Truncated      bool
	Interrupted    bool
	IsImage        bool
}

// rawResult covers the union of observed toolUseResult shapes. Absent fields
// simply stay zero, which is the right behaviour for a format we do not own.
type rawResult struct {
	Stdout              string          `json:"stdout"`
	Stderr              string          `json:"stderr"`
	Interrupted         bool            `json:"interrupted"`
	IsImage             bool            `json:"isImage"`
	PersistedOutputPath string          `json:"persistedOutputPath"`
	PersistedOutputSize int             `json:"persistedOutputSize"`
	FilePath            string          `json:"filePath"`
	StructuredPatch     json.RawMessage `json:"structuredPatch"`
	File                *struct {
		FilePath   string `json:"filePath"`
		Content    string `json:"content"`
		StartLine  int    `json:"startLine"`
		NumLines   int    `json:"numLines"`
		TotalLines int    `json:"totalLines"`
		Type       string `json:"type"`
		Base64     string `json:"base64"`
	} `json:"file"`
}

// ParseResultMeta extracts attribution metadata from a toolUseResult payload.
// A payload that is a bare string or an unrecognised shape yields zero
// metadata rather than an error: we would rather attribute nothing than
// attribute wrongly.
func ParseResultMeta(raw json.RawMessage) ResultMeta {
	var m ResultMeta
	if len(raw) == 0 || raw[0] != '{' {
		return m
	}
	var r rawResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return m
	}

	m.Interrupted = r.Interrupted
	m.IsImage = r.IsImage
	m.Path = r.FilePath
	m.PersistedBytes = r.PersistedOutputSize
	// Output spilled to a file is output the model was not shown in full.
	m.Truncated = r.PersistedOutputPath != ""

	if r.File != nil {
		if r.File.FilePath != "" {
			m.Path = r.File.FilePath
		}
		m.StartLine = r.File.StartLine
		m.Lines = r.File.NumLines
		m.TotalLines = r.File.TotalLines
		if r.File.Base64 != "" {
			m.IsImage = true
		}
	}
	return m
}

// Partial reports whether only a fragment of the file was returned.
func (m ResultMeta) Partial() bool {
	return m.TotalLines > 0 && m.Lines > 0 && m.Lines < m.TotalLines
}
