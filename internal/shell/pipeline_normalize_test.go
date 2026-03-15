package shell

import (
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func TestRewritePipelineSubshellsWrapsFinalPipelineStage(t *testing.T) {
	t.Parallel()

	program := parseShellTestProgram(t, "printf 'hello\\n' | read -r value\n")
	rewritePipelineSubshells(program)

	pipeline, ok := program.Stmts[0].Cmd.(*syntax.BinaryCmd)
	if !ok {
		t.Fatalf("Cmd = %T, want *syntax.BinaryCmd", program.Stmts[0].Cmd)
	}
	if _, ok := pipeline.Y.Cmd.(*syntax.Subshell); !ok {
		t.Fatalf("pipeline.Y.Cmd = %T, want *syntax.Subshell", pipeline.Y.Cmd)
	}
}

func TestRewritePipelineSubshellsWrapsNestedRightHandStages(t *testing.T) {
	t.Parallel()

	program := parseShellTestProgram(t, "printf a | tr a b | wc -c\n")
	rewritePipelineSubshells(program)

	outer, ok := program.Stmts[0].Cmd.(*syntax.BinaryCmd)
	if !ok {
		t.Fatalf("outer Cmd = %T, want *syntax.BinaryCmd", program.Stmts[0].Cmd)
	}
	if _, ok := outer.Y.Cmd.(*syntax.Subshell); !ok {
		t.Fatalf("outer Y.Cmd = %T, want *syntax.Subshell", outer.Y.Cmd)
	}

	inner, ok := outer.X.Cmd.(*syntax.BinaryCmd)
	if !ok {
		t.Fatalf("outer X.Cmd = %T, want nested *syntax.BinaryCmd", outer.X.Cmd)
	}
	if _, ok := inner.Y.Cmd.(*syntax.Subshell); !ok {
		t.Fatalf("inner Y.Cmd = %T, want *syntax.Subshell", inner.Y.Cmd)
	}
}

func TestRewritePipelineSubshellsSkipsExistingSubshell(t *testing.T) {
	t.Parallel()

	program := parseShellTestProgram(t, "printf a | (read -r value)\n")
	rewritePipelineSubshells(program)

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
}

func parseShellTestProgram(t testing.TB, script string) *syntax.File {
	t.Helper()

	program, err := syntax.NewParser().Parse(strings.NewReader(script), "test.sh")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return program
}
