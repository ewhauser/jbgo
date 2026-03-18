package conformance

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var excludedSpecFiles = map[string]struct{}{
	"errexit-osh.test.sh":         {},
	"posix.test.sh":               {},
	"toysh-posix.test.sh":         {},
	"toysh.test.sh":               {},
	"ysh-builtin-private.test.sh": {},
	"zsh-idioms.test.sh":          {},
}

//nolint:forbidigo // Conformance specs are vendored host files read directly by the test harness.
func LoadSpecFiles(specDir string, specNames []string) ([]SpecFile, error) {
	matches := make([]string, 0, len(specNames))
	if len(specNames) == 0 {
		var err error
		matches, err = filepath.Glob(filepath.Join(specDir, "*.test.sh"))
		if err != nil {
			return nil, err
		}
	} else {
		for _, specName := range specNames {
			specName = strings.TrimSpace(specName)
			if specName == "" {
				continue
			}
			matches = append(matches, filepath.Join(specDir, filepath.Base(specName)))
		}
	}
	filtered := matches[:0]
	for _, match := range matches {
		if isBashSpecFile(match) {
			filtered = append(filtered, match)
		}
	}
	matches = filtered
	if len(matches) == 0 {
		return nil, fmt.Errorf("no OILS spec files found in %s", specDir)
	}
	sort.Strings(matches)

	files := make([]SpecFile, 0, len(matches))
	for _, match := range matches {
		data, err := os.ReadFile(match)
		if err != nil {
			return nil, err
		}
		relPath := filepath.ToSlash(filepath.Join(filepath.Base(specDir), filepath.Base(match)))
		specFile, err := ParseSpecFile(relPath, string(data))
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", relPath, err)
		}
		files = append(files, specFile)
	}
	return files, nil
}

func isBashSpecFile(path string) bool {
	_, excluded := excludedSpecFiles[filepath.Base(path)]
	return !excluded
}

func ParseSpecFile(relPath, content string) (SpecFile, error) {
	file := SpecFile{
		Path:     filepath.ToSlash(relPath),
		Metadata: make(map[string]string),
	}

	var current *SpecCase
	var scriptLines []string
	ignoreDirectiveBlock := false

	flushCase := func() error {
		if current == nil {
			return nil
		}
		current.Script = strings.Join(scriptLines, "\n")
		if len(scriptLines) > 0 {
			current.Script += "\n"
		}
		if strings.TrimSpace(current.Name) == "" {
			return fmt.Errorf("empty case name in %s", relPath)
		}
		file.Cases = append(file.Cases, *current)
		current = nil
		scriptLines = nil
		return nil
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		rawLine := scanner.Text()
		trimmed := strings.TrimSpace(rawLine)

		if strings.HasPrefix(rawLine, "#### ") {
			if err := flushCase(); err != nil {
				return SpecFile{}, err
			}
			current = &SpecCase{
				Name:      strings.TrimSpace(strings.TrimPrefix(rawLine, "#### ")),
				StartLine: lineNo,
			}
			ignoreDirectiveBlock = false
			continue
		}

		if current == nil {
			key, value, ok := parseMetadataLine(rawLine)
			if ok {
				file.Metadata[key] = value
			}
			continue
		}

		if ignoreDirectiveBlock {
			if trimmed == "## END" {
				ignoreDirectiveBlock = false
				continue
			}
			if strings.HasPrefix(rawLine, "##") {
				ignoreDirectiveBlock = false
			} else {
				continue
			}
		}

		if strings.HasPrefix(rawLine, "##") {
			if startsIgnoredBlock(rawLine) {
				ignoreDirectiveBlock = true
			}
			continue
		}

		scriptLines = append(scriptLines, rawLine)
	}
	if err := scanner.Err(); err != nil {
		return SpecFile{}, err
	}

	if ignoreDirectiveBlock {
		return SpecFile{}, fmt.Errorf("unterminated directive block in %s", relPath)
	}
	if err := flushCase(); err != nil {
		return SpecFile{}, err
	}
	if len(file.Cases) == 0 {
		return SpecFile{}, fmt.Errorf("no OILS cases found in %s", relPath)
	}
	return file, nil
}

func parseMetadataLine(line string) (key, value string, ok bool) {
	if !strings.HasPrefix(strings.TrimSpace(line), "## ") {
		return "", "", false
	}
	body := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "## "))
	parts := strings.SplitN(body, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	key = strings.TrimSpace(parts[0])
	value = strings.TrimSpace(parts[1])
	if key == "" {
		return "", "", false
	}
	return key, value, true
}

func startsIgnoredBlock(line string) bool {
	body := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "##"))
	return strings.HasSuffix(body, "STDOUT:") || strings.HasSuffix(body, "STDERR:")
}
