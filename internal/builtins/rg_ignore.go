package builtins

import (
	"context"
	"path"
	"strings"

	shellpattern "github.com/ewhauser/gbash/internal/shellpattern"
)

type rgGlobRule struct {
	pattern         string
	include         bool
	caseInsensitive bool
}

type rgIgnoreRule struct {
	baseDir  string
	pattern  string
	negated  bool
	dirOnly  bool
	anchored bool
}

func rgParseGlobRule(raw string, caseInsensitive bool) rgGlobRule {
	rule := rgGlobRule{
		pattern:         strings.TrimPrefix(raw, "./"),
		include:         true,
		caseInsensitive: caseInsensitive,
	}
	if strings.HasPrefix(rule.pattern, "!") {
		rule.include = false
		rule.pattern = strings.TrimPrefix(rule.pattern, "!")
	}
	return rule
}

func rgGlobAllows(rules []rgGlobRule, displayPath string) (bool, error) {
	if len(rules) == 0 {
		return true, nil
	}

	candidate := strings.TrimPrefix(displayPath, "./")
	hasInclude := false
	for _, rule := range rules {
		if rule.include {
			hasInclude = true
			break
		}
	}

	allowed := !hasInclude
	for _, rule := range rules {
		if rule.pattern == "" {
			continue
		}
		matched, err := rgMatchPathPattern(rule.pattern, candidate, !strings.Contains(strings.TrimPrefix(rule.pattern, "/"), "/"), rule.caseInsensitive)
		if err != nil {
			return false, err
		}
		if matched {
			allowed = rule.include
		}
	}
	return allowed, nil
}

func rgParseIgnoreRules(data, baseDir string) []rgIgnoreRule {
	lines := strings.Split(data, "\n")
	rules := make([]rgIgnoreRule, 0, len(lines))
	for _, rawLine := range lines {
		line := strings.TrimRight(rawLine, "\r")
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, `\#`) || strings.HasPrefix(line, `\!`) {
			line = line[1:]
		} else if strings.HasPrefix(line, "#") {
			continue
		}

		rule := rgIgnoreRule{baseDir: baseDir}
		if strings.HasPrefix(line, "!") {
			rule.negated = true
			line = line[1:]
		}
		if strings.HasPrefix(line, "/") {
			rule.anchored = true
			line = strings.TrimPrefix(line, "/")
		}
		if strings.HasSuffix(line, "/") {
			rule.dirOnly = true
			line = strings.TrimSuffix(line, "/")
		}
		if line == "" {
			continue
		}

		rule.pattern = line
		rules = append(rules, rule)
	}
	return rules
}

func rgLoadIgnoreRules(ctx context.Context, inv *Invocation, filename, baseDir string) ([]rgIgnoreRule, error) {
	data, _, err := readAllFile(ctx, inv, filename)
	if err != nil {
		return nil, err
	}
	return rgParseIgnoreRules(string(data), baseDir), nil
}

func rgShouldIgnorePath(rules []rgIgnoreRule, candidateAbs string, isDir bool) (bool, error) {
	ignored := false
	for _, rule := range rules {
		rel, ok := rgRelativePath(rule.baseDir, candidateAbs)
		if !ok || rel == "" || rel == "." {
			continue
		}
		matched, err := rgMatchPathPattern(rule.pattern, rel, !rule.anchored && !strings.Contains(rule.pattern, "/"), false)
		if err != nil {
			return false, err
		}
		if !matched {
			continue
		}
		if rule.dirOnly && !isDir {
			continue
		}
		ignored = !rule.negated
	}
	return ignored, nil
}

func rgMatchPathPattern(pattern, candidate string, anywhere, caseInsensitive bool) (bool, error) {
	if pattern == "" {
		return false, nil
	}

	normalizedPattern := strings.TrimPrefix(pattern, "/")
	if anywhere && !strings.HasPrefix(normalizedPattern, "**/") {
		normalizedPattern = "**/" + normalizedPattern
	}

	mode := shellpattern.Filenames | shellpattern.EntireString | shellpattern.GlobLeadingDot
	if caseInsensitive {
		mode |= shellpattern.NoGlobCase
	}
	return shellpattern.Match(normalizedPattern, candidate, mode)
}

func rgRelativePath(baseDir, target string) (string, bool) {
	baseDir = path.Clean(baseDir)
	target = path.Clean(target)

	if baseDir == "/" {
		if target == "/" {
			return ".", true
		}
		return strings.TrimPrefix(target, "/"), true
	}
	if target == baseDir {
		return ".", true
	}

	prefix := strings.TrimSuffix(baseDir, "/") + "/"
	if !strings.HasPrefix(target, prefix) {
		return "", false
	}
	return strings.TrimPrefix(target, prefix), true
}
