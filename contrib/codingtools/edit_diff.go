package codingtools

import (
	"fmt"
	"strings"

	"golang.org/x/text/unicode/norm"
)

type fuzzyMatchResult struct {
	found                 bool
	index                 int
	matchLength           int
	usedFuzzyMatch        bool
	contentForReplacement string
}

func detectLineEnding(content string) string {
	crlfIdx := strings.Index(content, "\r\n")
	lfIdx := strings.Index(content, "\n")
	switch {
	case lfIdx == -1:
		return "\n"
	case crlfIdx == -1:
		return "\n"
	case crlfIdx < lfIdx:
		return "\r\n"
	default:
		return "\n"
	}
}

func normalizeToLF(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}

func restoreLineEndings(text, ending string) string {
	if ending == "\r\n" {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}

func normalizeForFuzzyMatch(text string) string {
	lines := strings.Split(norm.NFKC.String(text), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	text = strings.Join(lines, "\n")
	replacer := strings.NewReplacer(
		"\u2018", "'",
		"\u2019", "'",
		"\u201A", "'",
		"\u201B", "'",
		"\u201C", "\"",
		"\u201D", "\"",
		"\u201E", "\"",
		"\u201F", "\"",
		"\u2010", "-",
		"\u2011", "-",
		"\u2012", "-",
		"\u2013", "-",
		"\u2014", "-",
		"\u2015", "-",
		"\u2212", "-",
		"\u00A0", " ",
		"\u2002", " ",
		"\u2003", " ",
		"\u2004", " ",
		"\u2005", " ",
		"\u2006", " ",
		"\u2007", " ",
		"\u2008", " ",
		"\u2009", " ",
		"\u200A", " ",
		"\u202F", " ",
		"\u205F", " ",
		"\u3000", " ",
	)
	return replacer.Replace(text)
}

func fuzzyFindText(content, oldText string) fuzzyMatchResult {
	exactIndex := strings.Index(content, oldText)
	if exactIndex != -1 {
		return fuzzyMatchResult{
			found:                 true,
			index:                 exactIndex,
			matchLength:           len(oldText),
			contentForReplacement: content,
		}
	}

	fuzzyContent := normalizeForFuzzyMatch(content)
	fuzzyOldText := normalizeForFuzzyMatch(oldText)
	fuzzyIndex := strings.Index(fuzzyContent, fuzzyOldText)
	if fuzzyIndex == -1 {
		return fuzzyMatchResult{
			found:                 false,
			index:                 -1,
			contentForReplacement: content,
		}
	}

	return fuzzyMatchResult{
		found:                 true,
		index:                 fuzzyIndex,
		matchLength:           len(fuzzyOldText),
		usedFuzzyMatch:        true,
		contentForReplacement: fuzzyContent,
	}
}

func stripBOM(content string) (string, string) {
	if strings.HasPrefix(content, "\uFEFF") {
		return "\uFEFF", strings.TrimPrefix(content, "\uFEFF")
	}
	return "", content
}

func generateDiffString(oldContent, newContent string, contextLines int) (string, int) {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")
	maxLineNum := max(len(oldLines), len(newLines))
	lineNumWidth := len(fmt.Sprintf("%d", maxLineNum))

	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}

	suffix := 0
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix &&
		oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}

	firstChangedLine := prefix + 1
	if prefix == len(newLines) && suffix == 0 {
		firstChangedLine = len(newLines)
	}

	startContext := max(0, prefix-contextLines)
	endContext := min(suffix, contextLines)

	var output []string
	if startContext > 0 {
		output = append(output, fmt.Sprintf(" %*s ...", lineNumWidth, ""))
	}

	for i := startContext; i < prefix; i++ {
		output = append(output, fmt.Sprintf(" %*d %s", lineNumWidth, i+1, oldLines[i]))
	}

	oldChangedEnd := len(oldLines) - suffix
	newChangedEnd := len(newLines) - suffix
	for i := prefix; i < oldChangedEnd; i++ {
		output = append(output, fmt.Sprintf("-%*d %s", lineNumWidth, i+1, oldLines[i]))
	}
	for i := prefix; i < newChangedEnd; i++ {
		output = append(output, fmt.Sprintf("+%*d %s", lineNumWidth, i+1, newLines[i]))
	}

	for i := 0; i < endContext; i++ {
		oldIdx := oldChangedEnd + i
		output = append(output, fmt.Sprintf(" %*d %s", lineNumWidth, oldIdx+1, oldLines[oldIdx]))
	}
	if suffix > endContext {
		output = append(output, fmt.Sprintf(" %*s ...", lineNumWidth, ""))
	}

	return strings.Join(output, "\n"), firstChangedLine
}
