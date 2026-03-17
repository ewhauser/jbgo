package shell

import (
	"strings"
	"testing"

	"github.com/ewhauser/gbash/third_party/mvdan-sh/syntax"
)

func TestRewritePipelineSubshellsWrapsFinalPipelineStage(t *testing.T) {
	t.Parallel()

	program := parseShellTestProgram(t, "printf 'hello\\n' | read -r value\n")
	synthetic := rewritePipelineSubshells(program)

	pipeline, ok := program.Stmts[0].Cmd.(*syntax.BinaryCmd)
	if !ok {
		t.Fatalf("Cmd = %T, want *syntax.BinaryCmd", program.Stmts[0].Cmd)
	}
	sub, ok := pipeline.Y.Cmd.(*syntax.Subshell)
	if !ok {
		t.Fatalf("pipeline.Y.Cmd = %T, want *syntax.Subshell", pipeline.Y.Cmd)
	}
	if got, ok := synthetic[pipeline.Y]; !ok || got != sub.Stmts[0] {
		t.Fatalf("synthetic metadata missing wrapped stmt")
	}
}

func TestRewritePipelineSubshellsWrapsNestedRightHandStages(t *testing.T) {
	t.Parallel()

	program := parseShellTestProgram(t, "printf a | tr a b | wc -c\n")
	synthetic := rewritePipelineSubshells(program)

	outer, ok := program.Stmts[0].Cmd.(*syntax.BinaryCmd)
	if !ok {
		t.Fatalf("outer Cmd = %T, want *syntax.BinaryCmd", program.Stmts[0].Cmd)
	}
	outerSub, ok := outer.Y.Cmd.(*syntax.Subshell)
	if !ok {
		t.Fatalf("outer Y.Cmd = %T, want *syntax.Subshell", outer.Y.Cmd)
	}
	if got, ok := synthetic[outer.Y]; !ok || got != outerSub.Stmts[0] {
		t.Fatalf("synthetic metadata missing outer wrapped stmt")
	}

	inner, ok := outer.X.Cmd.(*syntax.BinaryCmd)
	if !ok {
		t.Fatalf("outer X.Cmd = %T, want nested *syntax.BinaryCmd", outer.X.Cmd)
	}
	innerSub, ok := inner.Y.Cmd.(*syntax.Subshell)
	if !ok {
		t.Fatalf("inner Y.Cmd = %T, want *syntax.Subshell", inner.Y.Cmd)
	}
	if got, ok := synthetic[inner.Y]; !ok || got != innerSub.Stmts[0] {
		t.Fatalf("synthetic metadata missing inner wrapped stmt")
	}
}

func TestRewritePipelineSubshellsSkipsExistingSubshell(t *testing.T) {
	t.Parallel()

	program := parseShellTestProgram(t, "printf a | (read -r value)\n")
	synthetic := rewritePipelineSubshells(program)

	pipeline, ok := program.Stmts[0].Cmd.(*syntax.BinaryCmd)
	if !ok {
		t.Fatalf("Cmd = %T, want *syntax.BinaryCmd", program.Stmts[0].Cmd)
	}
	right, ok := pipeline.Y.Cmd.(*syntax.Subshell)
	if !ok {
		t.Fatalf("pipeline.Y.Cmd = %T, want *syntax.Subshell", pipeline.Y.Cmd)
	}
	if len(right.Stmts) != 1 {
		t.Fatalf("len(right.Stmts) = %d, want 1", len(right.Stmts))
	}
	if _, ok := right.Stmts[0].Cmd.(*syntax.Subshell); ok {
		t.Fatalf("existing subshell was wrapped twice")
	}
	if len(synthetic) != 0 {
		t.Fatalf("len(synthetic) = %d, want 0", len(synthetic))
	}
}

func parseShellTestProgram(t testing.TB, script string) *syntax.File {
	t.Helper()

	program, err := syntax.NewParser().Parse(strings.NewReader(script), "test.sh")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return program
}
