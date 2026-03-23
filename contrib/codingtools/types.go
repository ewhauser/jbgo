package codingtools

import gbfs "github.com/ewhauser/gbash/fs"

const defaultHomeDir = "/home/agent"

// Config configures the coding toolset.
type Config struct {
	FS         gbfs.FileSystem
	WorkingDir string
	HomeDir    string
	Truncation TruncationOptions
}

// ToolDefinition is a provider-neutral function tool definition.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// ContentBlock is one structured tool-response item.
type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
}

// ReadRequest is the read tool input contract.
type ReadRequest struct {
	Path   string `json:"path"`
	Offset *int   `json:"offset,omitempty"`
	Limit  *int   `json:"limit,omitempty"`
}

// WriteRequest is the write tool input contract.
type WriteRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// EditRequest is the edit tool input contract.
type EditRequest struct {
	Path    string `json:"path"`
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

// ReadDetails carries extra read metadata.
type ReadDetails struct {
	Truncation *TruncationResult `json:"truncation,omitempty"`
}

// EditDetails carries extra edit metadata.
type EditDetails struct {
	Diff             string `json:"diff"`
	FirstChangedLine int    `json:"first_changed_line,omitempty"`
}

// ReadResponse is the read tool result contract.
type ReadResponse struct {
	Content []ContentBlock `json:"content"`
	Details *ReadDetails   `json:"details,omitempty"`
}

// WriteResponse is the write tool result contract.
type WriteResponse struct {
	Content []ContentBlock `json:"content"`
}

// EditResponse is the edit tool result contract.
type EditResponse struct {
	Content []ContentBlock `json:"content"`
	Details *EditDetails   `json:"details,omitempty"`
}

// TruncationOptions configures read truncation.
type TruncationOptions struct {
	MaxLines int `json:"max_lines,omitempty"`
	MaxBytes int `json:"max_bytes,omitempty"`
}

// TruncationResult captures truncation metadata.
type TruncationResult struct {
	Content               string `json:"content"`
	Truncated             bool   `json:"truncated"`
	TruncatedBy           string `json:"truncated_by,omitempty"`
	TotalLines            int    `json:"total_lines"`
	TotalBytes            int    `json:"total_bytes"`
	OutputLines           int    `json:"output_lines"`
	OutputBytes           int    `json:"output_bytes"`
	LastLinePartial       bool   `json:"last_line_partial"`
	FirstLineExceedsLimit bool   `json:"first_line_exceeds_limit"`
	MaxLines              int    `json:"max_lines"`
	MaxBytes              int    `json:"max_bytes"`
}
