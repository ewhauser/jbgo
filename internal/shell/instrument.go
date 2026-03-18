package shell

import (
	"fmt"
	"strings"

	"github.com/ewhauser/gbash/internal/shfork/syntax"
	"github.com/ewhauser/gbash/policy"
)

const loopIterCommandName = "__jb_loop_iter"

func instrumentLoopBudgets(program *syntax.File, pol policy.Policy) error {
	if program == nil || pol == nil || pol.Limits().MaxLoopIterations <= 0 {
		return nil
	}

	var (
		nextLoopID int
		walkErr    error
	)

	syntax.Walk(program, func(node syntax.Node) bool {
		if walkErr != nil {
			return false
		}

		switch node := node.(type) {
		case *syntax.WhileClause:
			if hasLoopGuard(node.Do) {
				return true
			}
			guard, err := newLoopGuardStmt(loopKind(node), nextLoopID)
			if err != nil {
				walkErr = err
				return false
			}
			nextLoopID++
			node.Do = append([]*syntax.Stmt{guard}, node.Do...)
		case *syntax.ForClause:
			if hasLoopGuard(node.Do) {
				return true
			}
			guard, err := newLoopGuardStmt("for", nextLoopID)
			if err != nil {
				walkErr = err
				return false
			}
			nextLoopID++
			node.Do = append([]*syntax.Stmt{guard}, node.Do...)
		}
		return true
	})

	return walkErr
}

func loopKind(clause *syntax.WhileClause) string {
	if clause != nil && clause.Until {
		return "until"
	}
	return "while"
}

func hasLoopGuard(stmts []*syntax.Stmt) bool {
	if len(stmts) == 0 || stmts[0] == nil {
		return false
	}
	return stmtStartsWithLoopGuard(stmts[0])
}

func newLoopGuardStmt(kind string, id int) (*syntax.Stmt, error) {
	file, err := syntax.NewParser().Parse(strings.NewReader(fmt.Sprintf("%s %s %d || exit $?\n", loopIterCommandName, kind, id)), "loop-guard")
	if err != nil {
		return nil, err
	}
	if len(file.Stmts) != 1 {
		return nil, fmt.Errorf("unexpected loop guard statement count: %d", len(file.Stmts))
	}
	return file.Stmts[0], nil
}

func wordLiteral(word *syntax.Word) string {
	if word == nil {
		return ""
	}
	return word.Lit()
}

func stmtStartsWithLoopGuard(stmt *syntax.Stmt) bool {
	if stmt == nil || stmt.Cmd == nil {
		return false
	}

	switch cmd := stmt.Cmd.(type) {
	case *syntax.CallExpr:
		return callStartsWithLoopGuard(cmd)
	case *syntax.BinaryCmd:
		return stmtStartsWithLoopGuard(cmd.X)
	default:
		return false
	}
}

func callStartsWithLoopGuard(call *syntax.CallExpr) bool {
	if call == nil || len(call.Args) == 0 {
		return false
	}
	return wordLiteral(call.Args[0]) == loopIterCommandName
}
