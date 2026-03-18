package shell

import (
	"github.com/ewhauser/gbash/internal/shell/interp"
	"github.com/ewhauser/gbash/internal/shell/syntax"
)

func normalizeExecutionProgram(program *syntax.File) map[*syntax.Stmt]*syntax.Stmt {
	return rewritePipelineSubshells(program)
}

func applyRunnerPipelineSubshells(runner *interp.Runner, nodes map[*syntax.Stmt]*syntax.Stmt) {
	if runner == nil {
		return
	}
	runner.SetSyntheticPipelineSubshells(nodes)
}

func rewritePipelineSubshells(program *syntax.File) map[*syntax.Stmt]*syntax.Stmt {
	if program == nil {
		return nil
	}
	synthetic := make(map[*syntax.Stmt]*syntax.Stmt)

	syntax.Walk(program, func(node syntax.Node) bool {
		cmd, ok := node.(*syntax.BinaryCmd)
		if !ok {
			return true
		}
		if cmd.Op != syntax.Pipe && cmd.Op != syntax.PipeAll {
			return true
		}
		if cmd.Y == nil || cmd.Y.Cmd == nil {
			return true
		}
		if inner, ok := syntheticWrappedStmt(cmd.Y); ok {
			synthetic[cmd.Y] = inner
			return true
		}
		if _, ok := cmd.Y.Cmd.(*syntax.Subshell); ok {
			return true
		}

		cmd.Y = wrapStmtInSubshell(cmd.Y, synthetic)
		return true
	})
	return synthetic
}

func syntheticWrappedStmt(stmt *syntax.Stmt) (*syntax.Stmt, bool) {
	if stmt == nil {
		return nil, false
	}
	sub, ok := stmt.Cmd.(*syntax.Subshell)
	if !ok || len(sub.Stmts) != 1 {
		return nil, false
	}
	inner := sub.Stmts[0]
	if inner == nil {
		return nil, false
	}
	// Synthetic wrappers reuse the inner statement's span exactly; user-authored
	// subshells include their parentheses and therefore have a wider span.
	if sub.Lparen != inner.Pos() || sub.Rparen != inner.End() {
		return nil, false
	}
	return inner, true
}

func wrapStmtInSubshell(stmt *syntax.Stmt, synthetic map[*syntax.Stmt]*syntax.Stmt) *syntax.Stmt {
	if stmt == nil {
		return nil
	}
	sub := &syntax.Subshell{
		Lparen: stmt.Pos(),
		Rparen: stmt.End(),
		Stmts:  []*syntax.Stmt{stmt},
	}
	wrapped := &syntax.Stmt{
		Position: stmt.Pos(),
		Cmd:      sub,
	}
	if synthetic != nil {
		synthetic[wrapped] = stmt
	}
	return wrapped
}
