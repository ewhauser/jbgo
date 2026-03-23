package codingtools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"net/http"
	"os"
	"path"
	"strings"

	gbfs "github.com/ewhauser/gbash/fs"
)

type normalizedConfig struct {
	fsys       gbfs.FileSystem
	workingDir string
	homeDir    string
	truncation TruncationOptions
}

// Toolset owns the coding tools for a specific filesystem view.
type Toolset struct {
	cfg    normalizedConfig
	queues *mutationQueue
}

// New constructs a read/edit/write toolset.
func New(cfg Config) *Toolset {
	return &Toolset{
		cfg:    normalizeConfig(cfg),
		queues: newMutationQueue(),
	}
}

// ToolDefinitions returns the provider-neutral tool definitions in upstream order.
func (t *Toolset) ToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		t.ReadToolDefinition(),
		t.EditToolDefinition(),
		t.WriteToolDefinition(),
	}
}

// ReadToolDefinition returns the read tool definition.
func (t *Toolset) ReadToolDefinition() ToolDefinition {
	opts := normalizeTruncationOptions(t.cfg.truncation)
	return ToolDefinition{
		Name: "read",
		Description: fmt.Sprintf(
			"Read the contents of a file. Supports text files and images (jpg, png, gif, webp). For text files, output is truncated to %d lines or %s (whichever is hit first). Use offset/limit for large files. When you need the full file, continue with offset until complete.",
			opts.MaxLines,
			FormatSize(opts.MaxBytes),
		),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the file to read (relative or absolute)",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Line number to start reading from (1-indexed)",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of lines to read",
				},
			},
			"required":             []string{"path"},
			"additionalProperties": false,
		},
	}
}

// EditToolDefinition returns the edit tool definition.
func (t *Toolset) EditToolDefinition() ToolDefinition {
	return ToolDefinition{
		Name:        "edit",
		Description: "Edit a file by replacing exact text. The oldText must match exactly, including whitespace and newlines. Use this for precise, surgical edits.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the file to edit (relative or absolute)",
				},
				"oldText": map[string]any{
					"type":        "string",
					"description": "Exact text to find and replace",
				},
				"newText": map[string]any{
					"type":        "string",
					"description": "New text to replace the old text with",
				},
			},
			"required":             []string{"path", "oldText", "newText"},
			"additionalProperties": false,
		},
	}
}

// WriteToolDefinition returns the write tool definition.
func (t *Toolset) WriteToolDefinition() ToolDefinition {
	return ToolDefinition{
		Name:        "write",
		Description: "Write content to a file. Creates the file if it does not exist, overwrites if it does, and automatically creates parent directories.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the file to write (relative or absolute)",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Content to write to the file",
				},
			},
			"required":             []string{"path", "content"},
			"additionalProperties": false,
		},
	}
}

// Read runs the read tool.
func (t *Toolset) Read(ctx context.Context, req ReadRequest) (ReadResponse, error) {
	if ctx == nil {
		return ReadResponse{}, errors.New("context is required")
	}
	if req.Path == "" {
		return ReadResponse{}, errors.New("path is required")
	}
	if req.Offset != nil && *req.Offset < 0 {
		return ReadResponse{}, errors.New("offset must be non-negative")
	}
	if req.Limit != nil && *req.Limit < 0 {
		return ReadResponse{}, errors.New("limit must be non-negative")
	}

	absolutePath := resolveReadPath(ctx, t.cfg.fsys, req.Path, t.cfg.workingDir, t.cfg.homeDir)
	buffer, err := readFile(ctx, t.cfg.fsys, absolutePath)
	if err != nil {
		return ReadResponse{}, err
	}

	if mimeType := detectSupportedImageMimeType(buffer); mimeType != "" {
		return ReadResponse{
			Content: []ContentBlock{
				{Type: "text", Text: fmt.Sprintf("Read image file [%s]", mimeType)},
				{Type: "image", Data: base64.StdEncoding.EncodeToString(buffer), MIMEType: mimeType},
			},
		}, nil
	}

	textContent := string(buffer)
	allLines := strings.Split(textContent, "\n")
	totalFileLines := len(allLines)

	startLine := 0
	if req.Offset != nil {
		startLine = maxInt(0, *req.Offset-1)
	}
	startLineDisplay := startLine + 1
	if startLine >= len(allLines) {
		return ReadResponse{}, fmt.Errorf("offset %d is beyond end of file (%d lines total)", valueOrDefault(req.Offset, 0), len(allLines))
	}

	selectedContent := strings.Join(allLines[startLine:], "\n")
	userLimitedLines := -1
	if req.Limit != nil {
		endLine := minInt(startLine+*req.Limit, len(allLines))
		selectedContent = strings.Join(allLines[startLine:endLine], "\n")
		userLimitedLines = endLine - startLine
	}

	truncation := TruncateHead(selectedContent, t.cfg.truncation)
	var outputText string
	var details *ReadDetails

	switch {
	case truncation.FirstLineExceedsLimit:
		firstLineSize := FormatSize(len(allLines[startLine]))
		outputText = fmt.Sprintf(
			"[Line %d is %s and exceeds the %s read limit. This tool does not return partial lines.]",
			startLineDisplay,
			firstLineSize,
			FormatSize(truncation.MaxBytes),
		)
		details = &ReadDetails{Truncation: &truncation}
	case truncation.Truncated:
		endLineDisplay := startLineDisplay + truncation.OutputLines - 1
		nextOffset := endLineDisplay + 1
		outputText = truncation.Content
		if truncation.TruncatedBy == "lines" {
			outputText += fmt.Sprintf(
				"\n\n[Showing lines %d-%d of %d. Use offset=%d to continue.]",
				startLineDisplay,
				endLineDisplay,
				totalFileLines,
				nextOffset,
			)
		} else {
			outputText += fmt.Sprintf(
				"\n\n[Showing lines %d-%d of %d (%s limit). Use offset=%d to continue.]",
				startLineDisplay,
				endLineDisplay,
				totalFileLines,
				FormatSize(truncation.MaxBytes),
				nextOffset,
			)
		}
		details = &ReadDetails{Truncation: &truncation}
	case userLimitedLines >= 0 && startLine+userLimitedLines < len(allLines):
		remaining := len(allLines) - (startLine + userLimitedLines)
		nextOffset := startLine + userLimitedLines + 1
		outputText = truncation.Content + fmt.Sprintf("\n\n[%d more lines in file. Use offset=%d to continue.]", remaining, nextOffset)
	default:
		outputText = truncation.Content
	}

	return ReadResponse{
		Content: []ContentBlock{{Type: "text", Text: outputText}},
		Details: details,
	}, nil
}

// Write runs the write tool.
func (t *Toolset) Write(ctx context.Context, req WriteRequest) (WriteResponse, error) {
	if ctx == nil {
		return WriteResponse{}, errors.New("context is required")
	}
	if req.Path == "" {
		return WriteResponse{}, errors.New("path is required")
	}

	absolutePath := resolveToCwd(req.Path, t.cfg.workingDir, t.cfg.homeDir)
	key := mutationQueueKey(ctx, t.cfg.fsys, absolutePath)
	return withMutationQueue(ctx, t.queues, key, func() (WriteResponse, error) {
		if err := writeFile(ctx, t.cfg.fsys, absolutePath, req.Content); err != nil {
			return WriteResponse{}, err
		}
		return WriteResponse{
			Content: []ContentBlock{
				{Type: "text", Text: fmt.Sprintf("Successfully wrote %d bytes to %s", len(req.Content), req.Path)},
			},
		}, nil
	})
}

// Edit runs the edit tool.
func (t *Toolset) Edit(ctx context.Context, req EditRequest) (EditResponse, error) {
	if ctx == nil {
		return EditResponse{}, errors.New("context is required")
	}
	if req.Path == "" {
		return EditResponse{}, errors.New("path is required")
	}
	if req.OldText == "" {
		return EditResponse{}, errors.New("oldText must be non-empty")
	}

	absolutePath := resolveToCwd(req.Path, t.cfg.workingDir, t.cfg.homeDir)
	key := mutationQueueKey(ctx, t.cfg.fsys, absolutePath)

	return withMutationQueue(ctx, t.queues, key, func() (EditResponse, error) {
		buffer, err := readFile(ctx, t.cfg.fsys, absolutePath)
		if err != nil {
			if errors.Is(err, stdfs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
				return EditResponse{}, fmt.Errorf("file not found: %s", req.Path)
			}
			return EditResponse{}, err
		}

		rawContent := string(buffer)
		bom, content := stripBOM(rawContent)
		originalEnding := detectLineEnding(content)
		normalizedContent := normalizeToLF(content)
		normalizedOldText := normalizeToLF(req.OldText)
		normalizedNewText := normalizeToLF(req.NewText)

		matchResult := fuzzyFindText(normalizedContent, normalizedOldText)
		if !matchResult.found {
			return EditResponse{}, fmt.Errorf(
				"could not find the exact text in %s. The old text must match exactly including all whitespace and newlines",
				req.Path,
			)
		}

		fuzzyContent := normalizeForFuzzyMatch(normalizedContent)
		fuzzyOldText := normalizeForFuzzyMatch(normalizedOldText)
		occurrences := strings.Count(fuzzyContent, fuzzyOldText)
		if occurrences > 1 {
			return EditResponse{}, fmt.Errorf(
				"found %d occurrences of the text in %s. The text must be unique. Please provide more context to make it unique",
				occurrences,
				req.Path,
			)
		}

		baseContent := matchResult.contentForReplacement
		newContent := baseContent[:matchResult.index] + normalizedNewText + baseContent[matchResult.index+matchResult.matchLength:]
		if baseContent == newContent {
			return EditResponse{}, fmt.Errorf(
				"no changes made to %s. The replacement produced identical content",
				req.Path,
			)
		}

		finalContent := bom + restoreLineEndings(newContent, originalEnding)
		if err := writeFile(ctx, t.cfg.fsys, absolutePath, finalContent); err != nil {
			return EditResponse{}, err
		}

		diff, firstChangedLine := generateDiffString(baseContent, newContent, 4)
		return EditResponse{
			Content: []ContentBlock{
				{Type: "text", Text: fmt.Sprintf("Successfully replaced text in %s.", req.Path)},
			},
			Details: &EditDetails{
				Diff:             diff,
				FirstChangedLine: firstChangedLine,
			},
		}, nil
	})
}

// ParseReadRequest decodes a provider tool-call payload.
func ParseReadRequest(input map[string]any) (ReadRequest, error) {
	if input == nil {
		return ReadRequest{}, fmt.Errorf("tool arguments must be a JSON object")
	}

	filePath, err := parsePath(input)
	if err != nil {
		return ReadRequest{}, err
	}

	var req ReadRequest
	req.Path = filePath
	if raw, ok := input["offset"]; ok {
		value, err := parseNonNegativeInt(raw)
		if err != nil {
			return ReadRequest{}, fmt.Errorf("`offset` must be a non-negative integer")
		}
		req.Offset = &value
	}
	if raw, ok := input["limit"]; ok {
		value, err := parseNonNegativeInt(raw)
		if err != nil {
			return ReadRequest{}, fmt.Errorf("`limit` must be a non-negative integer")
		}
		req.Limit = &value
	}
	return req, nil
}

// ParseWriteRequest decodes a provider tool-call payload.
func ParseWriteRequest(input map[string]any) (WriteRequest, error) {
	if input == nil {
		return WriteRequest{}, fmt.Errorf("tool arguments must be a JSON object")
	}

	filePath, err := parsePath(input)
	if err != nil {
		return WriteRequest{}, err
	}
	content, ok := input["content"]
	if !ok {
		return WriteRequest{}, fmt.Errorf("`content` is required")
	}
	text, ok := content.(string)
	if !ok {
		return WriteRequest{}, fmt.Errorf("`content` must be a string")
	}
	return WriteRequest{Path: filePath, Content: text}, nil
}

// ParseEditRequest decodes a provider tool-call payload.
func ParseEditRequest(input map[string]any) (EditRequest, error) {
	if input == nil {
		return EditRequest{}, fmt.Errorf("tool arguments must be a JSON object")
	}

	filePath, err := parsePath(input)
	if err != nil {
		return EditRequest{}, err
	}
	oldText, err := parseRequiredString(input, "oldText")
	if err != nil {
		return EditRequest{}, err
	}
	newText, err := parseRequiredString(input, "newText")
	if err != nil {
		return EditRequest{}, err
	}
	return EditRequest{Path: filePath, OldText: oldText, NewText: newText}, nil
}

func normalizeConfig(cfg Config) normalizedConfig {
	fsys := cfg.FS
	if fsys == nil {
		fsys = gbfs.NewMemory()
	}
	homeDir := strings.TrimSpace(cfg.HomeDir)
	if homeDir == "" {
		homeDir = defaultHomeDir
	}
	homeDir = gbfs.Clean(homeDir)

	workingDir := strings.TrimSpace(cfg.WorkingDir)
	switch {
	case workingDir == "":
		workingDir = homeDir
	case strings.HasPrefix(workingDir, "/"):
		workingDir = gbfs.Clean(workingDir)
	default:
		workingDir = gbfs.Resolve(homeDir, workingDir)
	}

	return normalizedConfig{
		fsys:       fsys,
		workingDir: workingDir,
		homeDir:    homeDir,
		truncation: normalizeTruncationOptions(cfg.Truncation),
	}
}

func parsePath(input map[string]any) (string, error) {
	if raw, ok := input["path"]; ok {
		text, ok := raw.(string)
		if !ok {
			return "", fmt.Errorf("`path` must be a string")
		}
		if text == "" {
			return "", fmt.Errorf("`path` is required")
		}
		return text, nil
	}
	if raw, ok := input["file_path"]; ok {
		text, ok := raw.(string)
		if !ok {
			return "", fmt.Errorf("`file_path` must be a string")
		}
		if text == "" {
			return "", fmt.Errorf("`path` is required")
		}
		return text, nil
	}
	return "", fmt.Errorf("`path` is required")
}

func parseRequiredString(input map[string]any, field string) (string, error) {
	raw, ok := input[field]
	if !ok {
		return "", fmt.Errorf("`%s` is required", field)
	}
	text, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("`%s` must be a string", field)
	}
	return text, nil
}

func parseNonNegativeInt(raw any) (int, error) {
	switch value := raw.(type) {
	case int:
		if value < 0 {
			return 0, fmt.Errorf("negative")
		}
		return value, nil
	case int64:
		if value < 0 {
			return 0, fmt.Errorf("negative")
		}
		return int(value), nil
	case float64:
		if value < 0 || value != float64(int(value)) {
			return 0, fmt.Errorf("not an integer")
		}
		return int(value), nil
	case json.Number:
		parsed, err := value.Int64()
		if err != nil || parsed < 0 {
			return 0, fmt.Errorf("invalid integer")
		}
		return int(parsed), nil
	default:
		return 0, fmt.Errorf("invalid integer")
	}
}

func readFile(ctx context.Context, fsys gbfs.FileSystem, absolutePath string) ([]byte, error) {
	file, err := fsys.Open(ctx, absolutePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	var output []byte
	buf := make([]byte, 32*1024)
	for {
		n, readErr := file.Read(buf)
		if n > 0 {
			output = append(output, buf[:n]...)
		}
		if errors.Is(readErr, io.EOF) {
			return output, nil
		}
		if readErr != nil {
			return nil, readErr
		}
	}
}

func writeFile(ctx context.Context, fsys gbfs.FileSystem, absolutePath, content string) error {
	if err := fsys.MkdirAll(ctx, path.Dir(absolutePath), 0o755); err != nil {
		return err
	}
	file, err := fsys.OpenFile(ctx, absolutePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	_, err = io.Copy(file, strings.NewReader(content))
	return err
}

func detectSupportedImageMimeType(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	mimeType := http.DetectContentType(data)
	switch mimeType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return mimeType
	default:
		return ""
	}
}

func valueOrDefault(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
