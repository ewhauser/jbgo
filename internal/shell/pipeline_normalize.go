package shell

import (
	"sync"

	"github.com/ewhauser/gbash/third_party/mvdan-sh/syntax"
)

var pipelineSyntheticCache sync.Map // map[*syntax.File]map[*syntax.Stmt]*syntax.Stmt

func normalizeExecutionProgram(program *syntax.File) map[*syntax.Stmt]*syntax.Stmt {
	if program != nil {
		if cached, ok := pipelineSyntheticCache.Load(program); ok {
			return cached.(map[*syntax.Stmt]*syntax.Stmt)
		}
	}
	synthetic := rewritePipelineSubshells(program)
	if program != nil {
		pipelineSyntheticCache.Store(program, synthetic)
	}
	return synthetic
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
		if _, ok := cmd.Y.Cmd.(*syntax.Subshell); ok {
			return true
		}

		cmd.Y = wrapStmtInSubshell(cmd.Y, synthetic)
		return true
	})
	return synthetic
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
