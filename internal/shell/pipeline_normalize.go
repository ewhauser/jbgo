package shell

import "github.com/ewhauser/gbash/third_party/mvdan-sh/syntax"

func normalizeExecutionProgram(program *syntax.File) error {
	rewritePipelineSubshells(program)
	return nil
}

func rewritePipelineSubshells(program *syntax.File) {
	if program == nil {
		return
	}

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

		cmd.Y = wrapStmtInSubshell(cmd.Y)
		return true
	})
}

func wrapStmtInSubshell(stmt *syntax.Stmt) *syntax.Stmt {
	if stmt == nil {
		return nil
	}
	return &syntax.Stmt{
		Position: stmt.Pos(),
		Cmd: &syntax.Subshell{
			Lparen: stmt.Pos(),
			Rparen: stmt.End(),
			Stmts:  []*syntax.Stmt{stmt},
		},
	}
}
