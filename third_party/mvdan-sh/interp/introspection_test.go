package interp_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/third_party/mvdan-sh/interp"
	"github.com/ewhauser/gbash/third_party/mvdan-sh/syntax"
)

func TestTopLevelScriptPathSetsBashIntrospection(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runIntrospectionScript(t, "main.sh", joinLines(
		`printf 'ZERO:%s\n' "$0"`,
		`printf 'SRC:%s\n' "${BASH_SOURCE[0]-}"`,
		`printf 'LINE:%s\n' "${BASH_LINENO[0]-}"`,
		`printf 'FUNC:%s\n' "${FUNCNAME-unset}"`,
	), nil, interp.TopLevelScriptPath("main.sh"))
	if err != nil {
		t.Fatalf("Runner.Run() error = %v; stderr=%q", err, stderr)
	}

	if got, want := stdout, joinLines(
		"ZERO:main.sh",
		"SRC:main.sh",
		"LINE:0",
		"FUNC:unset",
	); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestSourceAndFunctionStacksTrackDefinitionAndCalls(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runIntrospectionScript(t, "main.sh", joinLines(
		"source ./lib.sh",
		"mainfunc() {",
		"  libfunc",
		"}",
		"mainfunc",
	), map[string]string{
		"lib.sh": joinLines(
			"libfunc() {",
			`  printf 'FUNC:%s\n' "${FUNCNAME[*]-}"`,
			`  printf 'SRC:%s\n' "${BASH_SOURCE[*]-}"`,
			`  printf 'LINE:%s\n' "${BASH_LINENO[*]-}"`,
			"}",
		),
	}, interp.TopLevelScriptPath("main.sh"))
	if err != nil {
		t.Fatalf("Runner.Run() error = %v; stderr=%q", err, stderr)
	}

	if got, want := stdout, joinLines(
		"FUNC:libfunc mainfunc main",
		"SRC:./lib.sh main.sh main.sh",
		"LINE:3 5 0",
	); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestSourceFromInlineExecutionOmitsPseudoFrames(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runIntrospectionScript(t, "", "source ./lib.sh\n", map[string]string{
		"lib.sh": joinLines(
			`printf 'FUNC:%s\n' "${FUNCNAME[*]-}"`,
			`printf 'SRC:%s\n' "${BASH_SOURCE[*]-}"`,
			`printf 'LINE:%s\n' "${BASH_LINENO[*]-}"`,
		),
	})
	if err != nil {
		t.Fatalf("Runner.Run() error = %v; stderr=%q", err, stderr)
	}

	if got, want := stdout, joinLines(
		"FUNC:",
		"SRC:./lib.sh",
		"LINE:0",
	); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestInlineFunctionsKeepFuncnameButLeaveSourceUnset(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runIntrospectionScript(t, "stdin", joinLines(
		"f() {",
		`  printf 'FUNC:%s\n' "${FUNCNAME[*]-}"`,
		`  printf 'SRC:%s\n' "${BASH_SOURCE[*]-}"`,
		`  printf 'LINE:%s\n' "${BASH_LINENO[*]-}"`,
		"}",
		"f",
	), nil)
	if err != nil {
		t.Fatalf("Runner.Run() error = %v; stderr=%q", err, stderr)
	}

	if got, want := stdout, joinLines(
		"FUNC:f",
		"SRC:",
		"LINE:6",
	); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestProcessSubstitutionInheritsCallFrames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process substitution is not supported on windows")
	}
	t.Parallel()

	stdout, stderr, err := runIntrospectionScript(t, "main.sh", joinLines(
		"f() {",
		`  while IFS= read -r line; do printf '%s\n' "$line"; done < <(`,
		`    printf 'FUNC:%s\n' "${FUNCNAME[*]-}"`,
		`    printf 'SRC:%s\n' "${BASH_SOURCE[*]-}"`,
		`    printf 'LINE:%s\n' "${BASH_LINENO[*]-}"`,
		"  )",
		"}",
		"f",
	), nil, interp.TopLevelScriptPath("main.sh"))
	if err != nil {
		t.Fatalf("Runner.Run() error = %v; stderr=%q", err, stderr)
	}

	if got, want := stdout, joinLines(
		"FUNC:f main",
		"SRC:main.sh main.sh",
		"LINE:8 0",
	); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func runIntrospectionScript(t *testing.T, name, script string, files map[string]string, opts ...interp.RunnerOption) (string, string, error) {
	t.Helper()

	root := t.TempDir()
	for rel, data := range files {
		absPath := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(absPath), err)
		}
		if err := os.WriteFile(absPath, []byte(data), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", absPath, err)
		}
	}

	file, err := syntax.NewParser().Parse(strings.NewReader(script), name)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", name, err)
	}

	var stdout concBuffer
	var stderr concBuffer
	runnerOpts := append([]interp.RunnerOption{
		interp.Dir(root),
		interp.StdIO(nil, &stdout, &stderr),
	}, opts...)
	runner, err := interp.New(runnerOpts...)
	if err != nil {
		t.Fatalf("interp.New() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
	defer cancel()

	err = runner.Run(ctx, file)
	return stdout.String(), stderr.String(), err
}

func joinLines(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}
