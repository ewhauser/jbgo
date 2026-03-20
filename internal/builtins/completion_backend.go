package builtins

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/ewhauser/gbash/internal/completionutil"
	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/interp"
	"github.com/ewhauser/gbash/internal/shell/pattern"
)

type staticCompletionBackend struct {
	inv *Invocation
}

func completionBackend(ctx context.Context, inv *Invocation) completionutil.Backend {
	if backend, ok := interp.NewCompletionBackend(ctx, registeredCommandsGetter(inv)); ok {
		return backend
	}
	return &staticCompletionBackend{inv: inv}
}

func registeredCommandsGetter(inv *Invocation) func() []string {
	if inv == nil || inv.GetRegisteredCommands == nil {
		return nil
	}
	return inv.GetRegisteredCommands
}

func (b *staticCompletionBackend) ValidateWordlistSyntax(wordlist string) error {
	_, err := completionutil.ParseWordlistDocument(wordlist)
	return err
}

func (b *staticCompletionBackend) ExpandWordlist(wordlist string) ([]string, error) {
	word, err := completionutil.ParseWordlistDocument(wordlist)
	if err != nil {
		if b.inv != nil && b.inv.Stderr != nil {
			_, _ = fmt.Fprintln(b.inv.Stderr, completionutil.WordlistErrorText(wordlist, err))
		}
		return nil, err
	}
	cfg := &expand.Config{
		Env: envAsEnviron(b.inv),
	}
	expanded, err := expand.Document(cfg, word)
	if err != nil {
		err = expand.WithArithmSource(err, wordlist, 0, uint(len(wordlist)))
		if b.inv != nil && b.inv.Stderr != nil {
			_, _ = fmt.Fprintln(b.inv.Stderr, completionutil.WordlistErrorText(wordlist, err))
		}
		return nil, err
	}
	return expand.ReadFields(cfg, expanded, -1, false), nil
}

func (b *staticCompletionBackend) FunctionExists(string) bool {
	return false
}

func (b *staticCompletionBackend) FunctionNames(string) []string {
	return nil
}

func (b *staticCompletionBackend) VariableNames(prefix string, exportedOnly bool) []string {
	if b.inv == nil {
		return nil
	}
	names := make([]string, 0, len(b.inv.Env))
	for name := range b.inv.Env {
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (b *staticCompletionBackend) AliasNames(string) []string {
	return nil
}

func (b *staticCompletionBackend) SetoptNames(prefix string) []string {
	names := []string{
		"allexport",
		"errexit",
		"noexec",
		"noglob",
		"nounset",
		"pipefail",
		"verbose",
		"vi",
		"xtrace",
	}
	return filterBackendNames(names, prefix)
}

func (b *staticCompletionBackend) ShoptNames(prefix string) []string {
	names := []string{
		"dotglob",
		"expand_aliases",
		"extglob",
		"globstar",
		"lastpipe",
		"nocaseglob",
		"nullglob",
	}
	return filterBackendNames(names, prefix)
}

func (b *staticCompletionBackend) ExternalCommandNames(prefix string) ([]string, error) {
	if b.inv == nil || b.inv.FS == nil {
		return nil, nil
	}
	var out []string
	for elem := range strings.SplitSeq(strings.TrimSpace(b.inv.Env["PATH"]), ":") {
		dir := elem
		switch dir {
		case "", ".":
			dir = b.inv.Cwd
		default:
			if !path.IsAbs(dir) {
				dir = b.inv.FS.Resolve(dir)
			}
		}
		entries, err := b.inv.FS.ReadDir(context.Background(), dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if prefix != "" && !strings.HasPrefix(name, prefix) {
				continue
			}
			out = append(out, name)
		}
	}
	return out, nil
}

func (b *staticCompletionBackend) FileNames(prefix string, dirsOnly bool) ([]string, error) {
	if b.inv == nil || b.inv.FS == nil {
		return nil, nil
	}
	dirPart, base, searchDir := completionutil.SplitCompletionPath(prefix)
	searchPath := searchDir
	if searchPath != "/" && !path.IsAbs(searchPath) {
		searchPath = b.inv.FS.Resolve(searchDir)
	}
	entries, err := b.inv.FS.ReadDir(context.Background(), searchPath)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, entry := range entries {
		name := entry.Name()
		if base != "" && !strings.HasPrefix(name, base) {
			continue
		}
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".") {
			continue
		}
		if dirsOnly && !entry.IsDir() {
			continue
		}
		out = append(out, dirPart+name)
	}
	slices.Sort(out)
	return out, nil
}

func (b *staticCompletionBackend) UserNames(prefix string) ([]string, error) {
	if b.inv == nil || b.inv.FS == nil {
		return nil, nil
	}
	var out []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			return
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	add(b.inv.Env["USER"])
	data, err := b.inv.FS.ReadFile(context.Background(), "/etc/passwd")
	if err == nil {
		for line := range strings.SplitSeq(string(data), "\n") {
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			name, _, _ := strings.Cut(line, ":")
			add(name)
		}
	}
	slices.Sort(out)
	return out, nil
}

func (b *staticCompletionBackend) MatchFilterPattern(filter, candidate string) (bool, error) {
	matcher, err := pattern.ExtendedPatternMatcher(filter, pattern.EntireString)
	if err != nil {
		return false, err
	}
	return matcher(candidate), nil
}

func (b *staticCompletionBackend) RunFunction(string, completionutil.HookRequest) completionutil.HookResult {
	return completionutil.HookResult{Status: 1}
}

func (b *staticCompletionBackend) RunCommandHook(string, completionutil.HookRequest) completionutil.HookResult {
	return completionutil.HookResult{Status: 1}
}

func (b *staticCompletionBackend) CompArgv() []string {
	return nil
}

func (b *staticCompletionBackend) SetScalar(string, string) error {
	return nil
}

func (b *staticCompletionBackend) SetArray(string, []string) error {
	return nil
}

func filterBackendNames(names []string, prefix string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if prefix == "" || strings.HasPrefix(name, prefix) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

func envAsEnviron(inv *Invocation) expand.Environ {
	if inv == nil {
		return expand.ListEnviron()
	}
	pairs := make([]string, 0, len(inv.Env))
	for name, value := range inv.Env {
		pairs = append(pairs, name+"="+value)
	}
	return expand.ListEnviron(pairs...)
}
