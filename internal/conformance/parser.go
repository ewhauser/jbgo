package conformance

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
		return nil, fmt.Errorf("no spec files found in %s", specDir)
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
	var directiveBlock *directiveBlockCapture
	terminated := false

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
				Name:            strings.TrimSpace(strings.TrimPrefix(rawLine, "#### ")),
				StartLine:       lineNo,
				OracleOverrides: make(map[OracleMode]OracleOverride),
			}
			directiveBlock = nil
			terminated = false
			continue
		}

		if current == nil {
			key, value, ok := parseMetadataLine(rawLine)
			if ok {
				file.Metadata[key] = value
			}
			continue
		}
		if terminated {
			continue
		}

		if directiveBlock != nil {
			if trimmed == "## END" {
				directiveBlock.finish(current)
				directiveBlock = nil
				continue
			}
			if strings.HasPrefix(rawLine, "##") {
				directiveBlock.finish(current)
				directiveBlock = nil
			} else {
				directiveBlock.lines = append(directiveBlock.lines, rawLine)
				continue
			}
		}

		if strings.HasPrefix(rawLine, "##") {
			if applyDirectiveInline(current, rawLine) {
				continue
			}
			if block := parseDirectiveBlockHeader(rawLine); block != nil {
				directiveBlock = block
			}
			continue
		}

		scriptLines = append(scriptLines, rawLine)
		if trimmed == "exit" {
			terminated = true
		}
	}
	if err := scanner.Err(); err != nil {
		return SpecFile{}, err
	}

	if directiveBlock != nil {
		return SpecFile{}, fmt.Errorf("unterminated directive block in %s", relPath)
	}
	if err := flushCase(); err != nil {
		return SpecFile{}, err
	}
	if len(file.Cases) == 0 {
		return SpecFile{}, fmt.Errorf("no spec cases found in %s", relPath)
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

type directiveBlockCapture struct {
	oracle OracleMode
	kind   OracleOverrideKind
	field  string
	lines  []string
}

func (b *directiveBlockCapture) finish(specCase *SpecCase) {
	if b == nil || b.field == "" || specCase == nil {
		return
	}
	value := strings.Join(b.lines, "\n")
	if len(b.lines) > 0 {
		value += "\n"
	}
	if b.oracle == "" {
		switch b.field {
		case "stdout":
			specCase.Expectation.Stdout = &value
		case "stderr":
			specCase.Expectation.Stderr = &value
		}
		return
	}
	override := specCase.OracleOverrides[b.oracle]
	if override.Kind == "" {
		override.Kind = b.kind
	}
	switch b.field {
	case "stdout":
		override.Stdout = &value
	case "stderr":
		override.Stderr = &value
	}
	specCase.OracleOverrides[b.oracle] = override
}

func parseOracleMode(value string) (OracleMode, bool) {
	switch strings.TrimSpace(value) {
	case string(OracleBash):
		return OracleBash, true
	default:
		return "", false
	}
}

func parseOracleOverrideKind(value string) (OracleOverrideKind, bool) {
	switch strings.TrimSpace(value) {
	case string(OracleOverrideBug):
		return OracleOverrideBug, true
	case string(OracleOverrideOK):
		return OracleOverrideOK, true
	case string(OracleOverrideNI):
		return OracleOverrideNI, true
	default:
		return "", false
	}
}

func parseDirectiveBlockHeader(line string) *directiveBlockCapture {
	body := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "##"))
	switch body {
	case "STDOUT:", "STDERR:":
		field, _, ok := parseDirectiveField(body)
		if !ok {
			return nil
		}
		return &directiveBlockCapture{field: field}
	}
	kind, oracle, fieldToken, ok := parseOracleDirectivePrefix(strings.Fields(body))
	if !ok {
		return nil
	}
	field, _, ok := parseDirectiveField(fieldToken)
	if !ok {
		return nil
	}
	return &directiveBlockCapture{oracle: oracle, kind: kind, field: field}
}

func applyDirectiveInline(specCase *SpecCase, line string) bool {
	body := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "##"))
	parts := strings.Fields(body)
	switch {
	case len(parts) >= 3:
		kind, oracle, fieldToken, ok := parseOracleDirectivePrefix(parts)
		if !ok {
			return false
		}
		field, jsonEncoded, ok := parseDirectiveField(fieldToken)
		if !ok {
			return false
		}
		if len(parts) == 3 {
			return false
		}
		value := strings.TrimSpace(strings.Join(parts[3:], " "))
		return applyOracleDirectiveValue(specCase, oracle, kind, field, jsonEncoded, value)
	case len(parts) >= 1:
		field, jsonEncoded, ok := parseDirectiveField(parts[0])
		if !ok {
			return false
		}
		if len(parts) == 1 {
			return false
		}
		value := strings.TrimSpace(strings.Join(parts[1:], " "))
		return applyExpectationValue(specCase, field, jsonEncoded, value)
	default:
		return false
	}
}

func parseOracleDirectivePrefix(parts []string) (OracleOverrideKind, OracleMode, string, bool) {
	if len(parts) < 3 {
		return "", "", "", false
	}
	kind, ok := parseOracleOverrideKind(parts[0])
	if !ok {
		return "", "", "", false
	}
	oracle, ok := parseOracleMode(parts[1])
	if !ok {
		return "", "", "", false
	}
	return kind, oracle, parts[2], true
}

func parseDirectiveField(token string) (field string, jsonEncoded, ok bool) {
	token = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(token), ":"))
	switch token {
	case "stdout":
		return "stdout", false, true
	case "stderr":
		return "stderr", false, true
	case "status":
		return "status", false, true
	case "stdout-json":
		return "stdout", true, true
	case "stderr-json":
		return "stderr", true, true
	default:
		return "", false, false
	}
}

func applyExpectationValue(specCase *SpecCase, field string, jsonEncoded bool, value string) bool {
	switch field {
	case "status":
		n, err := strconv.Atoi(value)
		if err != nil {
			return false
		}
		specCase.Expectation.Status = &n
		return true
	case "stdout":
		v, ok := parseDirectiveStringValue(value, jsonEncoded)
		if !ok {
			return false
		}
		specCase.Expectation.Stdout = &v
		return true
	case "stderr":
		v, ok := parseDirectiveStringValue(value, jsonEncoded)
		if !ok {
			return false
		}
		specCase.Expectation.Stderr = &v
		return true
	default:
		return false
	}
}

func applyOracleDirectiveValue(specCase *SpecCase, oracle OracleMode, kind OracleOverrideKind, field string, jsonEncoded bool, value string) bool {
	override := specCase.OracleOverrides[oracle]
	if override.Kind == "" {
		override.Kind = kind
	}
	switch field {
	case "status":
		n, err := strconv.Atoi(value)
		if err != nil {
			return false
		}
		override.Status = &n
	case "stdout":
		v, ok := parseDirectiveStringValue(value, jsonEncoded)
		if !ok {
			return false
		}
		override.Stdout = &v
	case "stderr":
		v, ok := parseDirectiveStringValue(value, jsonEncoded)
		if !ok {
			return false
		}
		override.Stderr = &v
	default:
		return false
	}
	specCase.OracleOverrides[oracle] = override
	return true
}

func parseDirectiveStringValue(value string, jsonEncoded bool) (string, bool) {
	if !jsonEncoded {
		return value, true
	}
	var out string
	if err := json.Unmarshal([]byte(value), &out); err != nil {
		return "", false
	}
	return out, true
}
