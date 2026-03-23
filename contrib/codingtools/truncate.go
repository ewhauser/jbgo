package codingtools

import (
	"fmt"
	"strings"
)

const (
	// DefaultMaxLines is the default line limit for read output.
	DefaultMaxLines = 2000
	// DefaultMaxBytes is the default byte limit for read output.
	DefaultMaxBytes = 50 * 1024
)

func normalizeTruncationOptions(opts TruncationOptions) TruncationOptions {
	if opts.MaxLines <= 0 {
		opts.MaxLines = DefaultMaxLines
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultMaxBytes
	}
	return opts
}

// FormatSize formats a byte count for user-facing messages.
func FormatSize(bytes int) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%dB", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
	}
}

// TruncateHead truncates content from the start without returning partial lines.
func TruncateHead(content string, options TruncationOptions) TruncationResult {
	opts := normalizeTruncationOptions(options)
	maxLines := opts.MaxLines
	maxBytes := opts.MaxBytes

	totalBytes := len(content)
	lines := stringsSplit(content)
	totalLines := len(lines)

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{
			Content:               content,
			Truncated:             false,
			TotalLines:            totalLines,
			TotalBytes:            totalBytes,
			OutputLines:           totalLines,
			OutputBytes:           totalBytes,
			LastLinePartial:       false,
			FirstLineExceedsLimit: false,
			MaxLines:              maxLines,
			MaxBytes:              maxBytes,
		}
	}

	firstLineBytes := len(lines[0])
	if firstLineBytes > maxBytes {
		return TruncationResult{
			Truncated:             true,
			TruncatedBy:           "bytes",
			TotalLines:            totalLines,
			TotalBytes:            totalBytes,
			OutputLines:           0,
			OutputBytes:           0,
			LastLinePartial:       false,
			FirstLineExceedsLimit: true,
			MaxLines:              maxLines,
			MaxBytes:              maxBytes,
		}
	}

	outputLines := make([]string, 0, min(totalLines, maxLines))
	outputBytes := 0
	truncatedBy := "lines"

	for i := 0; i < len(lines) && i < maxLines; i++ {
		lineBytes := len(lines[i])
		if i > 0 {
			lineBytes++
		}
		if outputBytes+lineBytes > maxBytes {
			truncatedBy = "bytes"
			break
		}
		outputLines = append(outputLines, lines[i])
		outputBytes += lineBytes
	}
	if len(outputLines) >= maxLines && outputBytes <= maxBytes {
		truncatedBy = "lines"
	}

	outputContent := joinLines(outputLines)
	return TruncationResult{
		Content:               outputContent,
		Truncated:             true,
		TruncatedBy:           truncatedBy,
		TotalLines:            totalLines,
		TotalBytes:            totalBytes,
		OutputLines:           len(outputLines),
		OutputBytes:           len(outputContent),
		LastLinePartial:       false,
		FirstLineExceedsLimit: false,
		MaxLines:              maxLines,
		MaxBytes:              maxBytes,
	}
}

func stringsSplit(content string) []string {
	return strings.Split(content, "\n")
}

func joinLines(lines []string) string {
	return strings.Join(lines, "\n")
}
