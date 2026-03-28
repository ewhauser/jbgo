package interp

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/shell/analysis"
	"github.com/ewhauser/gbash/shell/expand"
	"github.com/ewhauser/gbash/shell/syntax"
)

func TestAnalysisVariableWriteEventClonesVariableValues(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	runner, err := NewRunner(&RunnerConfig{
		Dir:    "/tmp",
		Stdout: &stdout,
		Stderr: &stderr,
		AnalysisObserver: analysis.ObserverFunc(func(_ analysis.Context, event analysis.Event) {
			write, ok := event.(analysis.VariableWrite)
			if !ok || write.Name != "foo" || write.Variable.Kind != expand.Indexed || len(write.Variable.List) == 0 {
				return
			}
			write.Variable.List[0] = "mutated"
		}),
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	err = runner.runShellReader(context.Background(), strings.NewReader("foo=(one two)\necho \"${foo[0]}\"\n"), "analysis-test.sh", nil)
	if err != nil {
		t.Fatalf("runShellReader() error = %v", err)
	}
	if got, want := stdout.String(), "one\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunReaderWithMetadataReportsTransformFailureStatusToAnalysis(t *testing.T) {
	t.Parallel()

	var statuses []analysis.Status
	runner, err := NewRunner(&RunnerConfig{
		Dir: "/tmp",
		AnalysisObserver: analysis.ObserverFunc(func(_ analysis.Context, event analysis.Event) {
			finish, ok := event.(analysis.FileFinish)
			if ok {
				statuses = append(statuses, finish.Status)
			}
		}),
	})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	transformErr := syntax.ParseError{
		Filename: "analysis-transform.sh",
		Pos:      syntax.NewPos(0, 1, 1),
		Text:     "synthetic transform failure",
	}
	err = runner.RunReaderWithMetadata(
		context.Background(),
		strings.NewReader("echo ok\n"),
		"analysis-transform.sh",
		"",
		func(*syntax.File) (map[*syntax.Stmt]*syntax.Stmt, error) {
			return nil, transformErr
		},
	)
	if err == nil {
		t.Fatal("RunReaderWithMetadata() error = nil, want parse error")
	}
	var gotParseErr syntax.ParseError
	if !errors.As(err, &gotParseErr) {
		t.Fatalf("RunReaderWithMetadata() error = %T, want syntax.ParseError", err)
	}
	if got, want := len(statuses), 1; got != want {
		t.Fatalf("len(FileFinish statuses) = %d, want %d", got, want)
	}
	if got, want := statuses[0].Code, 2; got != want {
		t.Fatalf("FileFinish status code = %d, want %d", got, want)
	}
}
