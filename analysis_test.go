package gbash_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/shell/analysis"
	"github.com/ewhauser/gbash/shell/syntax"
)

type analysisRecord struct {
	event   analysis.Event
	run     analysis.RunMetadata
	file    analysis.FileMetadata
	stmt    *syntax.Stmt
	cmd     syntax.Command
	scopes  []analysis.Scope
	options analysis.Options
	control analysis.ControlFlow
	source  string
}

type analysisRecorder struct {
	mu     sync.Mutex
	events []analysisRecord
}

func (r *analysisRecorder) Observe(ctx analysis.Context, event analysis.Event) {
	record := analysisRecord{
		event:   event,
		run:     ctx.Run(),
		file:    ctx.File(),
		stmt:    ctx.Statement(),
		cmd:     ctx.Command(),
		scopes:  ctx.Scopes(),
		options: ctx.Options(),
		control: ctx.ControlFlow(),
	}
	switch {
	case record.cmd != nil:
		record.source = strings.TrimSpace(ctx.Source(record.cmd))
	case record.stmt != nil:
		record.source = strings.TrimSpace(ctx.Source(record.stmt))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, record)
}

func (r *analysisRecorder) snapshot() []analysisRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]analysisRecord, len(r.events))
	copy(out, r.events)
	return out
}

func TestAnalysisObserverReportsShellSemantics(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "helper.sh"), []byte("helper_var=2\necho \"$helper_var\" >/dev/null\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	recorder := &analysisRecorder{}
	rt, err := gbash.New(
		gbash.WithWorkspace(root),
		gbash.WithAnalysisObserver(recorder),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	script := strings.Join([]string{
		"set -e",
		"foo=1",
		"echo \"$foo\" >/dev/null",
		"f() { local bar=\"$foo\"; unset foo; echo \"$bar\" >/dev/null; }",
		"f",
		"source helper.sh",
		"eval 'qux=4; echo \"$qux\" >/dev/null'",
		"echo left | cat >/dev/null",
		"(echo sub >/dev/null)",
		"false || true",
		"",
	}, "\n")
	result, err := rt.Run(context.Background(), &gbash.ExecutionRequest{
		Name:   "analysis.sh",
		Script: script,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d", got, want)
	}

	events := recorder.snapshot()
	if countAnalysisEvents(events, func(event analysis.Event) bool {
		_, ok := event.(analysis.RunStart)
		return ok
	}) != 1 {
		t.Fatalf("RunStart count = %d, want 1", countAnalysisEvents(events, func(event analysis.Event) bool {
			_, ok := event.(analysis.RunStart)
			return ok
		}))
	}
	if countAnalysisEvents(events, func(event analysis.Event) bool {
		_, ok := event.(analysis.RunFinish)
		return ok
	}) != 1 {
		t.Fatalf("RunFinish count = %d, want 1", countAnalysisEvents(events, func(event analysis.Event) bool {
			_, ok := event.(analysis.RunFinish)
			return ok
		}))
	}
	if countAnalysisEvents(events, func(event analysis.Event) bool {
		_, ok := event.(analysis.StatementEnter)
		return ok
	}) == 0 || countAnalysisEvents(events, func(event analysis.Event) bool {
		_, ok := event.(analysis.StatementExit)
		return ok
	}) == 0 {
		t.Fatalf("missing statement lifecycle events: %#v", events)
	}
	if countAnalysisEvents(events, func(event analysis.Event) bool {
		_, ok := event.(analysis.CommandEnter)
		return ok
	}) == 0 || countAnalysisEvents(events, func(event analysis.Event) bool {
		_, ok := event.(analysis.CommandExit)
		return ok
	}) == 0 {
		t.Fatalf("missing command lifecycle events: %#v", events)
	}
	fileStartCount := countAnalysisEvents(events, func(event analysis.Event) bool {
		_, ok := event.(analysis.FileStart)
		return ok
	})
	fileFinishCount := countAnalysisEvents(events, func(event analysis.Event) bool {
		_, ok := event.(analysis.FileFinish)
		return ok
	})
	if fileStartCount < 3 || fileFinishCount < 3 {
		t.Fatalf("file lifecycle counts = (%d,%d), want at least 3",
			fileStartCount,
			fileFinishCount,
		)
	}

	scopeKinds := map[analysis.ScopeKind]bool{}
	for _, event := range events {
		scopeEnter, ok := event.event.(analysis.ScopeEnter)
		if !ok {
			continue
		}
		scopeKinds[scopeEnter.Scope.Kind] = true
	}
	for _, kind := range []analysis.ScopeKind{
		analysis.ScopeFunction,
		analysis.ScopeSource,
		analysis.ScopeEval,
		analysis.ScopeSubshell,
		analysis.ScopePipeline,
	} {
		if !scopeKinds[kind] {
			t.Fatalf("missing scope kind %q in %#v", kind, scopeKinds)
		}
	}

	writes := observedVariableNames(events, "write")
	for _, name := range []string{"foo", "bar", "helper_var", "qux"} {
		if !writes[name] {
			t.Fatalf("missing variable write for %q in %#v", name, writes)
		}
	}

	reads := observedVariableNames(events, "read")
	for _, name := range []string{"foo", "bar", "helper_var", "qux"} {
		if !reads[name] {
			t.Fatalf("missing variable read for %q in %#v", name, reads)
		}
	}

	unsets := observedVariableNames(events, "unset")
	if !unsets["foo"] {
		t.Fatalf("missing variable unset for foo in %#v", unsets)
	}

	suppressed := false
	for _, event := range events {
		if _, ok := event.event.(analysis.CommandExit); !ok {
			continue
		}
		if event.source == "false" && event.options.Errexit && event.control.ErrExitSuppressed {
			suppressed = true
			break
		}
	}
	if !suppressed {
		t.Fatal("missing set -e suppressed command exit for `false || true`")
	}
}

func TestAnalysisObserverReportsChunkedFileBoundaries(t *testing.T) {
	t.Parallel()

	recorder := &analysisRecorder{}
	rt, err := gbash.New(gbash.WithAnalysisObserver(recorder))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := rt.Run(context.Background(), &gbash.ExecutionRequest{
		Name:   "chunks.sh",
		Script: "echo one\necho two\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d", got, want)
	}

	var files []analysis.FileMetadata
	for _, event := range recorder.snapshot() {
		if _, ok := event.event.(analysis.FileStart); ok {
			files = append(files, event.file)
		}
	}
	if got, want := len(files), 2; got != want {
		t.Fatalf("len(FileStart) = %d, want %d", got, want)
	}
	if got, want := files[0].ChunkIndex, 0; got != want {
		t.Fatalf("first ChunkIndex = %d, want %d", got, want)
	}
	if got, want := files[1].ChunkIndex, 1; got != want {
		t.Fatalf("second ChunkIndex = %d, want %d", got, want)
	}
	if got, want := files[0].ChunkLine, uint(1); got != want {
		t.Fatalf("first ChunkLine = %d, want %d", got, want)
	}
	if got, want := files[1].ChunkLine, uint(2); got != want {
		t.Fatalf("second ChunkLine = %d, want %d", got, want)
	}
}

func TestAnalysisObserverRunsForInteractiveShells(t *testing.T) {
	t.Parallel()

	recorder := &analysisRecorder{}
	rt, err := gbash.New(gbash.WithAnalysisObserver(recorder))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	session, err := rt.NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	result, err := session.Interact(context.Background(), &gbash.InteractiveRequest{
		Stdin:  strings.NewReader("echo hi\n"),
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if err != nil {
		t.Fatalf("Interact() error = %v", err)
	}
	if got, want := result.ExitCode, 0; got != want {
		t.Fatalf("ExitCode = %d, want %d", got, want)
	}

	events := recorder.snapshot()
	if countAnalysisEvents(events, func(event analysis.Event) bool {
		_, ok := event.(analysis.RunStart)
		return ok
	}) != 1 {
		t.Fatalf("RunStart count = %d, want 1", countAnalysisEvents(events, func(event analysis.Event) bool {
			_, ok := event.(analysis.RunStart)
			return ok
		}))
	}
	if countAnalysisEvents(events, func(event analysis.Event) bool {
		_, ok := event.(analysis.RunFinish)
		return ok
	}) != 1 {
		t.Fatalf("RunFinish count = %d, want 1", countAnalysisEvents(events, func(event analysis.Event) bool {
			_, ok := event.(analysis.RunFinish)
			return ok
		}))
	}
	if countAnalysisEvents(events, func(event analysis.Event) bool {
		_, ok := event.(analysis.CommandEnter)
		return ok
	}) == 0 {
		t.Fatalf("missing command events for interactive shell: %#v", events)
	}
}

func countAnalysisEvents(events []analysisRecord, match func(analysis.Event) bool) int {
	count := 0
	for i := range events {
		if match(events[i].event) {
			count++
		}
	}
	return count
}

func observedVariableNames(events []analysisRecord, kind string) map[string]bool {
	out := map[string]bool{}
	for i := range events {
		switch value := events[i].event.(type) {
		case analysis.VariableRead:
			if kind == "read" {
				out[value.Name] = true
			}
		case analysis.VariableWrite:
			if kind == "write" {
				out[value.Name] = true
			}
		case analysis.VariableUnset:
			if kind == "unset" {
				out[value.Name] = true
			}
		}
	}
	return out
}
