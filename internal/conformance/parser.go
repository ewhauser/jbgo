package conformance

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

//nolint:forbidigo // Conformance specs are vendored host files read directly by the test harness.
func LoadSpecFiles(specDir string, specNames []string, mode OracleMode) ([]SpecFile, error) {
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
		if !supportsOracleMode(specFile, mode) {
			continue
		}
		files = append(files, specFile)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no OILS spec files found in %s for suite %s", specDir, mode)
	}
	return files, nil
}

func supportsOracleMode(specFile SpecFile, mode OracleMode) bool {
	if mode == "" || len(specFile.CompareShells) == 0 {
		return true
	}
	return slices.Contains(specFile.CompareShells, mode)
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
	file.CompareShells = parseCompareShells(file.Metadata["compare_shells"])
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
	oracles []OracleMode
	kind    OracleOverrideKind
	field   string
	lines   []string
}

func (b *directiveBlockCapture) finish(specCase *SpecCase) {
	if b == nil || b.field == "" || specCase == nil {
		return
	}
	value := strings.Join(b.lines, "\n")
	if len(b.lines) > 0 {
		value += "\n"
	}
	if len(b.oracles) == 0 {
		switch b.field {
		case "stdout":
			specCase.Expectation.Stdout = &value
		case "stderr":
			specCase.Expectation.Stderr = &value
		}
		return
	}
	for _, oracle := range b.oracles {
		override := specCase.OracleOverrides[oracle]
		if override.Kind == "" {
			override.Kind = b.kind
		}
		switch b.field {
		case "stdout":
			override.Stdout = &value
		case "stderr":
			override.Stderr = &value
		}
		specCase.OracleOverrides[oracle] = override
	}
}

func parseOracleMode(value string) (OracleMode, bool) {
	return canonicalOracleMode(value)
}

func parseOracleOverrideKind(value string) (OracleOverrideKind, bool) {
	value = strings.TrimSpace(value)
	if head, tail, ok := strings.Cut(value, "-"); ok {
		if _, err := strconv.Atoi(tail); err == nil {
			value = head
		}
	}
	switch value {
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
	return &directiveBlockCapture{oracles: oracle, kind: kind, field: field}
}

func applyDirectiveInline(specCase *SpecCase, line string) bool {
	body := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "##"))
	parts := strings.Fields(body)
	switch {
	case len(parts) >= 3:
		kind, oracles, fieldToken, ok := parseOracleDirectivePrefix(parts)
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
		for _, oracle := range oracles {
			if !applyOracleDirectiveValue(specCase, oracle, kind, field, jsonEncoded, value) {
				return false
			}
		}
		return true
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

func parseOracleDirectivePrefix(parts []string) (OracleOverrideKind, []OracleMode, string, bool) {
	if len(parts) < 3 {
		return "", nil, "", false
	}
	kind, ok := parseOracleOverrideKind(parts[0])
	if !ok {
		return "", nil, "", false
	}
	oracles, ok := parseOracleModes(parts[1])
	if !ok {
		return "", nil, "", false
	}
	return kind, oracles, parts[2], true
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

func parseCompareShells(value string) []OracleMode {
	seen := make(map[OracleMode]struct{})
	out := make([]OracleMode, 0, len(strings.Fields(value)))
	for token := range strings.FieldsSeq(value) {
		mode, ok := parseOracleMode(token)
		if !ok {
			continue
		}
		if _, exists := seen[mode]; exists {
			continue
		}
		seen[mode] = struct{}{}
		out = append(out, mode)
	}
	return out
}

func parseOracleModes(value string) ([]OracleMode, bool) {
	tokens := strings.Split(strings.TrimSpace(value), "/")
	seen := make(map[OracleMode]struct{}, len(tokens))
	out := make([]OracleMode, 0, len(tokens))
	for _, token := range tokens {
		mode, ok := parseOracleMode(token)
		if !ok {
			return nil, false
		}
		if _, exists := seen[mode]; exists {
			continue
		}
		seen[mode] = struct{}{}
		out = append(out, mode)
	}
	return out, len(out) > 0
}
