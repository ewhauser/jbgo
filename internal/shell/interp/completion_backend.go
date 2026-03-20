package interp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"

	"github.com/ewhauser/gbash/internal/completionutil"
	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/pattern"
)

type runnerCompletionBackend struct {
	ctx           context.Context
	runner        *Runner
	registryNames func() []string
}

func NewCompletionBackend(ctx context.Context, registryNames func() []string) (completionutil.Backend, bool) {
	hc, ok := LookupHandlerContext(ctx)
	if !ok || hc.runner == nil {
		return nil, false
	}
	return newRunnerCompletionBackend(ctx, hc.runner, registryNames), true
}

func newRunnerCompletionBackend(ctx context.Context, runner *Runner, registryNames func() []string) completionutil.Backend {
	return &runnerCompletionBackend{
		ctx:           ctx,
		runner:        runner,
		registryNames: registryNames,
	}
}

func (b *runnerCompletionBackend) ValidateWordlistSyntax(wordlist string) error {
	_, err := completionutil.ParseWordlistDocument(wordlist)
	return err
}

func (b *runnerCompletionBackend) ExpandWordlist(wordlist string) ([]string, error) {
	if b.runner == nil || wordlist == "" {
		return nil, nil
	}
	word, err := completionutil.ParseWordlistDocument(wordlist)
	if err != nil {
		fmt.Fprintln(b.runner.stderr, completionutil.WordlistErrorText(wordlist, err))
		return nil, err
	}
	cfg := *b.runner.ecfg
	expanded, err := expand.Document(&cfg, word)
	if err != nil {
		err = expand.WithArithmSource(err, wordlist, 0, uint(len(wordlist)))
		fmt.Fprintln(b.runner.stderr, completionutil.WordlistErrorText(wordlist, err))
		return nil, err
	}
	return expand.ReadFields(&cfg, expanded, -1, false), nil
}

func (b *runnerCompletionBackend) FunctionExists(name string) bool {
	return b.runner != nil && b.runner.funcs[name] != nil
}

func (b *runnerCompletionBackend) FunctionNames(prefix string) []string {
	if b.runner == nil {
		return nil
	}
	names := make([]string, 0, len(b.runner.funcs))
	for name := range b.runner.funcs {
		if b.runner.funcInternal(name) {
			continue
		}
		if prefix == "" || strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func (b *runnerCompletionBackend) VariableNames(prefix string, exportedOnly bool) []string {
	if b.runner == nil || b.runner.writeEnv == nil {
		return nil
	}
	names := make([]string, 0, 32)
	seen := make(map[string]struct{})
	b.runner.writeEnv.Each(func(name string, vr expand.Variable) bool {
		if !vr.Declared() {
			return true
		}
		if exportedOnly && !vr.Exported && name != "PWD" {
			return true
		}
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			return true
		}
		if _, ok := seen[name]; ok {
			return true
		}
		seen[name] = struct{}{}
		names = append(names, name)
		return true
	})
	slices.Sort(names)
	return names
}

func (b *runnerCompletionBackend) AliasNames(prefix string) []string {
	if b.runner == nil {
		return nil
	}
	names := make([]string, 0, len(b.runner.alias))
	for name := range b.runner.alias {
		if prefix == "" || strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func (b *runnerCompletionBackend) SetoptNames(prefix string) []string {
	names := make([]string, 0, len(posixOptsTable)+1)
	for _, opt := range posixOptsTable {
		if prefix == "" || strings.HasPrefix(opt.name, prefix) {
			names = append(names, opt.name)
		}
	}
	if prefix == "" || strings.HasPrefix("vi", prefix) {
		names = append(names, "vi")
	}
	slices.Sort(names)
	return names
}

func (b *runnerCompletionBackend) ShoptNames(prefix string) []string {
	names := make([]string, 0, len(bashOptsTable))
	for _, opt := range bashOptsTable {
		if prefix == "" || strings.HasPrefix(opt.name, prefix) {
			names = append(names, opt.name)
		}
	}
	slices.Sort(names)
	return names
}

func (b *runnerCompletionBackend) ExternalCommandNames(prefix string) ([]string, error) {
	if b.runner == nil {
		return nil, nil
	}
	var names []string
	pathValue := b.runner.writeEnv.Get("PATH").String()
	for _, elem := range strings.Split(pathValue, ":") {
		dir := elem
		switch dir {
		case "", ".":
			dir = b.runner.Dir
		default:
			dir = absPath(b.runner.Dir, dir)
		}
		entries, err := b.runner.readDirHandler(b.runner.handlerCtx(b.ctx, handlerKindReadDir, todoPos), dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			if prefix != "" && !strings.HasPrefix(name, prefix) {
				continue
			}
			if entry.IsDir() {
				continue
			}
			if strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
				continue
			}
			fullPath := path.Join(dir, name)
			if err := b.runner.access(b.ctx, fullPath, access_X_OK); err != nil {
				continue
			}
			names = append(names, name)
		}
	}
	return names, nil
}

func (b *runnerCompletionBackend) FileNames(prefix string, dirsOnly bool) ([]string, error) {
	if b.runner == nil {
		return nil, nil
	}
	dirPart, base, searchDir := completionutil.SplitCompletionPath(prefix)
	searchAbs := searchDir
	if searchAbs != "/" {
		searchAbs = absPath(b.runner.Dir, searchDir)
	}
	entries, err := b.runner.readDirHandler(b.runner.handlerCtx(b.ctx, handlerKindReadDir, todoPos), searchAbs)
	if err != nil {
		return nil, err
	}
	matches := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if base != "" && !strings.HasPrefix(name, base) {
			continue
		}
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".") {
			continue
		}
		if dirsOnly && !entry.IsDir() {
			info, infoErr := entry.Info()
			if infoErr != nil || !info.IsDir() {
				continue
			}
		}
		matches = append(matches, dirPart+name)
	}
	slices.Sort(matches)
	return matches, nil
}

func (b *runnerCompletionBackend) UserNames(prefix string) ([]string, error) {
	names := make([]string, 0, 8)
	seen := make(map[string]struct{})
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	add(b.runner.writeEnv.Get("USER").String())
	f, err := b.runner.open(b.ctx, "/etc/passwd", 0, 0, false)
	if err == nil {
		defer func() { _ = f.Close() }()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			name, _, _ := strings.Cut(line, ":")
			add(name)
		}
	}
	slices.Sort(names)
	return names, nil
}

func (b *runnerCompletionBackend) MatchFilterPattern(filter, candidate string) (bool, error) {
	mode := pattern.EntireString
	if b.runner != nil && b.runner.opts[optExtGlob] {
		mode |= pattern.ExtendedOperators
	}
	matcher, err := pattern.ExtendedPatternMatcher(filter, mode)
	if err != nil {
		return false, err
	}
	return matcher(candidate), nil
}

func (b *runnerCompletionBackend) RunFunction(name string, req completionutil.HookRequest) completionutil.HookResult {
	if b.runner == nil || b.runner.funcs[name] == nil {
		return completionutil.HookResult{Status: 1}
	}
	restoreVars := b.pushCompletionEnv(req)
	defer restoreVars()
	oldExit := b.runner.exit
	oldLastExpandExit := b.runner.lastExpandExit
	oldStderr := b.runner.stderr
	var errBuf bytes.Buffer
	b.runner.setStderrWriter(io.MultiWriter(oldStderr, &errBuf))
	b.runner.lastExpandExit = exitStatus{}
	defer func() {
		b.runner.setStderrWriter(oldStderr)
		b.runner.exit = oldExit
		b.runner.lastExpandExit = oldLastExpandExit
	}()
	b.runner.call(b.ctx, todoPos, []string{name, "compgen", req.Word, ""})
	status := completionHookStatus(b.runner.exit, b.runner.lastExpandExit, &errBuf)
	candidates := completionReplyCandidates(b.runner.lookupVar("COMPREPLY"))
	if status != 0 {
		candidates = nil
	}
	return completionutil.HookResult{
		Candidates: candidates,
		Status:     status,
	}
}

func (b *runnerCompletionBackend) RunCommandHook(command string, req completionutil.HookRequest) completionutil.HookResult {
	if b.runner == nil {
		return completionutil.HookResult{Status: 1}
	}
	restoreVars := b.pushCompletionEnv(req)
	defer restoreVars()
	oldExit := b.runner.exit
	oldLastExpandExit := b.runner.lastExpandExit
	oldStdout := b.runner.stdout
	oldStderr := b.runner.stderr
	var out bytes.Buffer
	var errBuf bytes.Buffer
	b.runner.setStdoutWriter(&out)
	b.runner.setStderrWriter(io.MultiWriter(oldStderr, &errBuf))
	b.runner.lastExpandExit = exitStatus{}
	defer func() {
		b.runner.setStdoutWriter(oldStdout)
		b.runner.setStderrWriter(oldStderr)
		b.runner.exit = oldExit
		b.runner.lastExpandExit = oldLastExpandExit
	}()
	if simpleCompletionCommand(command) {
		b.runner.call(b.ctx, todoPos, []string{command})
	} else {
		src := command
		if !strings.HasSuffix(src, "\n") {
			src += "\n"
		}
		_ = b.runner.runShellReader(b.ctx, strings.NewReader(src), "", nil)
	}
	status := completionHookStatus(b.runner.exit, b.runner.lastExpandExit, &errBuf)
	lines := splitCompletionLines(out.String())
	if status != 0 {
		lines = nil
	}
	return completionutil.HookResult{
		Candidates: lines,
		Status:     status,
	}
}

func (b *runnerCompletionBackend) CompArgv() []string {
	if b.runner == nil {
		return nil
	}
	return completionReplyCandidates(b.runner.lookupVar("COMP_ARGV"))
}

func (b *runnerCompletionBackend) SetScalar(name, value string) error {
	if b.runner == nil {
		return nil
	}
	b.runner.setVar(name, expand.Variable{Set: true, Kind: expand.String, Str: value})
	return nil
}

func (b *runnerCompletionBackend) SetArray(name string, values []string) error {
	if b.runner == nil {
		return nil
	}
	b.runner.setVar(name, expand.Variable{Set: true, Kind: expand.Indexed, List: append([]string(nil), values...)})
	return nil
}

func (b *runnerCompletionBackend) pushCompletionEnv(req completionutil.HookRequest) func() {
	restoreNames := []string{"COMP_WORDS", "COMP_CWORD", "COMP_LINE", "COMP_POINT", "COMPREPLY"}
	restores := make([]restoreVar, 0, len(restoreNames))
	for _, name := range restoreNames {
		restores = append(restores, restoreVar{name: name, vr: b.runner.lookupVar(name)})
	}
	b.runner.setVar("COMP_WORDS", expand.Variable{Set: true, Kind: expand.Indexed})
	b.runner.setVar("COMP_CWORD", expand.Variable{Set: true, Kind: expand.String, Str: "-1"})
	b.runner.setVar("COMP_LINE", expand.Variable{Set: true, Kind: expand.String, Str: ""})
	b.runner.setVar("COMP_POINT", expand.Variable{Set: true, Kind: expand.String, Str: "0"})
	b.runner.delVar("COMPREPLY")
	return func() {
		for _, restore := range restores {
			if restore.vr.Declared() {
				b.runner.setVar(restore.name, restore.vr)
				continue
			}
			b.runner.delVar(restore.name)
		}
	}
}

func completionReplyCandidates(vr expand.Variable) []string {
	switch vr.Kind {
	case expand.Indexed:
		return append([]string(nil), vr.IndexedValues()...)
	case expand.String:
		if vr.IsSet() {
			return []string{vr.Str}
		}
	default:
		if vr.IsSet() {
			return []string{vr.String()}
		}
	}
	return nil
}

func simpleCompletionCommand(command string) bool {
	return command != "" && !strings.ContainsAny(command, " \t\n;&|()<>$`\"'\\")
}

func completionHookStatus(exit, expandExit exitStatus, errBuf *bytes.Buffer) int {
	status := exitCodeForCompletion(exit)
	if status == 0 {
		status = exitCodeForCompletion(expandExit)
	}
	if status == 0 && errBuf != nil && errBuf.Len() > 0 {
		status = 1
	}
	return status
}

func exitCodeForCompletion(exit exitStatus) int {
	switch {
	case exit.code != 0:
		return int(exit.code)
	case exit.exiting, exit.fatalExit:
		return 1
	default:
		return 0
	}
}

func splitCompletionLines(out string) []string {
	out = strings.ReplaceAll(out, "\r\n", "\n")
	lines := strings.Split(out, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

var _ completionutil.Backend = (*runnerCompletionBackend)(nil)
