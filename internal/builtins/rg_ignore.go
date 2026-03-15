package builtins

import (
	"context"
	"path"
	"regexp"
	"strings"

	"github.com/ewhauser/gbash/policy"
)

type rgIgnoreRule struct {
	base          string
	regex         *regexp.Regexp
	negated       bool
	directoryOnly bool
}

type rgIgnoreMatcher struct {
	opts   *rgOptions
	rules  []rgIgnoreRule
	loaded map[string]bool
}

func newRGIgnoreMatcher(opts *rgOptions) *rgIgnoreMatcher {
	return &rgIgnoreMatcher{
		opts:   opts,
		loaded: make(map[string]bool),
	}
}

func (m *rgIgnoreMatcher) loadPath(ctx context.Context, inv *Invocation, targetAbs string) error {
	if m == nil {
		return nil
	}
	current := targetAbs
	info, _, exists, err := lstatMaybe(ctx, inv, policy.FileActionLstat, targetAbs)
	if err != nil {
		return err
	}
	if exists && info != nil && !info.IsDir() {
		current = path.Dir(targetAbs)
	}

	dirs := make([]string, 0)
	for {
		dirs = append(dirs, current)
		if current == "/" {
			break
		}
		current = path.Dir(current)
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := m.loadDir(ctx, inv, dirs[i]); err != nil {
			return err
		}
	}
	return nil
}

func (m *rgIgnoreMatcher) loadDir(ctx context.Context, inv *Invocation, dir string) error {
	if m == nil || m.loaded[dir] {
		return nil
	}
	m.loaded[dir] = true

	names := make([]string, 0, 3)
	if !m.opts.noIgnoreVcs {
		names = append(names, ".gitignore")
	}
	if !m.opts.noIgnoreDot {
		names = append(names, ".rgignore", ".ignore")
	}
	for _, name := range names {
		ignorePath := path.Join(dir, name)
		data, _, err := readAllFile(ctx, inv, ignorePath)
		if err != nil {
			if errorsIsNotExist(err) {
				continue
			}
			return err
		}
		m.parse(dir, string(data))
	}
	return nil
}

func (m *rgIgnoreMatcher) parse(base, content string) {
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimRight(line, " \t\r")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		negated := false
		if strings.HasPrefix(trimmed, "!") {
			negated = true
			trimmed = strings.TrimPrefix(trimmed, "!")
		}

		directoryOnly := false
		if strings.HasSuffix(trimmed, "/") {
			directoryOnly = true
			trimmed = strings.TrimSuffix(trimmed, "/")
		}
		if trimmed == "" {
			continue
		}

		rooted := false
		if strings.HasPrefix(trimmed, "/") {
			rooted = true
			trimmed = strings.TrimPrefix(trimmed, "/")
		} else if strings.Contains(trimmed, "/") && !strings.HasPrefix(trimmed, "**/") {
			rooted = true
		}

		re, err := rgIgnorePatternRegexp(trimmed, rooted)
		if err != nil {
			continue
		}
		m.rules = append(m.rules, rgIgnoreRule{
			base:          base,
			regex:         re,
			negated:       negated,
			directoryOnly: directoryOnly,
		})
	}
}

func rgIgnorePatternRegexp(pattern string, rooted bool) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	if !rooted {
		b.WriteString("(?:|.*/)")
	}
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					b.WriteString("(?:.*/)?")
					i += 2
					continue
				}
				b.WriteString(".*")
				i++
				continue
			}
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		case '[':
			j := i + 1
			if j < len(pattern) && pattern[j] == '!' {
				j++
			}
			if j < len(pattern) && pattern[j] == ']' {
				j++
			}
			for j < len(pattern) && pattern[j] != ']' {
				j++
			}
			if j >= len(pattern) {
				b.WriteString(`\[`)
				continue
			}
			class := pattern[i : j+1]
			if strings.HasPrefix(class, "[!") {
				class = "[^" + class[2:]
			}
			b.WriteString(class)
			i = j
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	b.WriteString("(?:/.*)?$")
	return regexp.Compile(b.String())
}

func (m *rgIgnoreMatcher) matches(abs string, isDir bool) bool {
	_, ignored, _ := m.state(abs, isDir)
	return ignored
}

func (m *rgIgnoreMatcher) whitelisted(abs string, isDir bool) bool {
	_, ignored, negated := m.state(abs, isDir)
	return !ignored && negated
}

func (m *rgIgnoreMatcher) state(abs string, isDir bool) (matched, ignored, negated bool) {
	if m == nil {
		return false, false, false
	}
	for _, rule := range m.rules {
		if rule.base != "/" && abs != rule.base && !strings.HasPrefix(abs, rule.base+"/") {
			continue
		}
		rel := strings.TrimPrefix(abs, rule.base)
		rel = strings.TrimPrefix(rel, "/")
		if rule.directoryOnly && !isDir {
			continue
		}
		if rule.regex.MatchString(rel) {
			matched = true
			ignored = !rule.negated
			negated = rule.negated
		}
	}
	return matched, ignored, negated
}
