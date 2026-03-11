package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/ewhauser/jbgo/trace"
)

type executionFixture struct {
	Name       string            `json:"name"`
	Script     string            `json:"script"`
	WorkDir    string            `json:"work_dir,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	ReplaceEnv bool              `json:"replace_env,omitempty"`
	Files      map[string]string `json:"files,omitempty"`
	Stdout     string            `json:"stdout"`
	Stderr     string            `json:"stderr"`
	ExitCode   int               `json:"exit_code"`
	Events     []normalizedEvent `json:"events,omitempty"`
}

type normalizedExecutionResult struct {
	ExitCode int               `json:"exit_code"`
	Stdout   string            `json:"stdout"`
	Stderr   string            `json:"stderr"`
	Events   []normalizedEvent `json:"events,omitempty"`
}

type normalizedEvent struct {
	Kind    trace.Kind              `json:"kind"`
	Command *normalizedCommandEvent `json:"command,omitempty"`
	File    *normalizedFileEvent    `json:"file,omitempty"`
	Message string                  `json:"message,omitempty"`
	Error   string                  `json:"error,omitempty"`
}

type normalizedCommandEvent struct {
	Name     string   `json:"name"`
	Argv     []string `json:"argv"`
	Dir      string   `json:"dir"`
	ExitCode int      `json:"exit_code"`
	Builtin  bool     `json:"builtin,omitempty"`
}

type normalizedFileEvent struct {
	Action string `json:"action"`
	Path   string `json:"path"`
}

func loadExecutionFixtures(t testing.TB, pattern string) []executionFixture {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join("testdata", pattern))
	if err != nil {
		t.Fatalf("Glob(%q) error = %v", pattern, err)
	}
	if len(matches) == 0 {
		t.Fatalf("Glob(%q) returned no fixtures", pattern)
	}
	sort.Strings(matches)

	var fixtures []executionFixture
	for _, match := range matches {
		data, err := os.ReadFile(match)
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", match, err)
		}

		var fileFixtures []executionFixture
		if err := json.Unmarshal(data, &fileFixtures); err != nil {
			t.Fatalf("Unmarshal(%q) error = %v", match, err)
		}
		fixtures = append(fixtures, fileFixtures...)
	}

	return fixtures
}

func runExecutionFixture(t testing.TB, fixture *executionFixture) *ExecutionResult {
	t.Helper()

	session := newSession(t, &Config{})
	seedSessionFiles(t, session, fixture.Files)

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script:     fixture.Script,
		WorkDir:    fixture.WorkDir,
		Env:        fixture.Env,
		ReplaceEnv: fixture.ReplaceEnv,
	})
	if err != nil {
		t.Fatalf("Exec(%s) error = %v", fixture.Name, err)
	}

	return result
}

func seedSessionFiles(t testing.TB, session *Session, files map[string]string) {
	t.Helper()

	paths := make([]string, 0, len(files))
	for file := range files {
		paths = append(paths, file)
	}
	sort.Strings(paths)

	for _, file := range paths {
		writeSessionFile(t, session, file, []byte(files[file]))
	}
}

func normalizeResult(result *ExecutionResult) normalizedExecutionResult {
	if result == nil {
		return normalizedExecutionResult{}
	}

	return normalizedExecutionResult{
		ExitCode: result.ExitCode,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		Events:   normalizeEvents(result.Events),
	}
}

func canonicalizeResult(result normalizedExecutionResult) normalizedExecutionResult {
	result.Events = canonicalizeEvents(result.Events)
	return result
}

func canonicalizeEvents(events []normalizedEvent) []normalizedEvent {
	if len(events) == 0 {
		return nil
	}

	out := append([]normalizedEvent(nil), events...)
	sort.Slice(out, func(i, j int) bool {
		return normalizedEventKey(out[i]) < normalizedEventKey(out[j])
	})
	return out
}

func normalizedEventKey(event normalizedEvent) string {
	return mustJSONString(event)
}

func normalizeEvents(events []trace.Event) []normalizedEvent {
	if len(events) == 0 {
		return nil
	}

	out := make([]normalizedEvent, 0, len(events))
	for i := range events {
		event := events[i]
		normalized := normalizedEvent{
			Kind:    event.Kind,
			Message: event.Message,
			Error:   event.Error,
		}
		if event.Command != nil {
			normalized.Command = &normalizedCommandEvent{
				Name:     event.Command.Name,
				Argv:     append([]string(nil), event.Command.Argv...),
				Dir:      event.Command.Dir,
				ExitCode: event.Command.ExitCode,
				Builtin:  event.Command.Builtin,
			}
		}
		if event.File != nil {
			normalized.File = &normalizedFileEvent{
				Action: event.File.Action,
				Path:   event.File.Path,
			}
		}
		out = append(out, normalized)
	}
	return out
}

func assertNormalizedResult(t *testing.T, got, want normalizedExecutionResult) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized result mismatch\n\ngot:\n%s\n\nwant:\n%s", mustJSON(t, got), mustJSON(t, want))
	}
}

func assertExecutionOutcome(t *testing.T, got normalizedExecutionResult, exitCode int, stdout, stderr string) {
	t.Helper()

	if got.ExitCode != exitCode || got.Stdout != stdout || got.Stderr != stderr {
		t.Fatalf(
			"execution outcome mismatch\n\ngot:\n%s\n\nwant:\n%s",
			mustJSON(t, got),
			mustJSON(t, normalizedExecutionResult{
				ExitCode: exitCode,
				Stdout:   stdout,
				Stderr:   stderr,
			}),
		)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent() error = %v", err)
	}
	return string(data)
}

func mustJSONString(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}
