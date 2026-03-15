package shell

import (
	"fmt"
	"strings"

	"github.com/ewhauser/gbash/policy"
	"mvdan.cc/sh/v3/syntax"
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

func rewriteLetClauses(program *syntax.File) error {
	if program == nil {
		return nil
	}

	printer := syntax.NewPrinter()
	parser := syntax.NewParser()
	var walkErr error

	syntax.Walk(program, func(node syntax.Node) bool {
		if walkErr != nil {
			return false
		}

		stmt, ok := node.(*syntax.Stmt)
		if !ok || stmt.Cmd == nil {
			return true
		}
		_, ok = stmt.Cmd.(*syntax.LetClause)
		if !ok {
			return true
		}

		call, err := rewriteLetClause(printer, parser, stmt.Cmd)
		if err != nil {
			walkErr = err
			return false
		}
		stmt.Cmd = call
		return true
	})

	return walkErr
}

func rewriteLetClause(printer *syntax.Printer, parser *syntax.Parser, cmd syntax.Command) (*syntax.CallExpr, error) {
	if printer == nil {
		printer = syntax.NewPrinter()
	}
	if parser == nil {
		parser = syntax.NewParser()
	}

	var rendered strings.Builder
	if err := printer.Print(&rendered, cmd); err != nil {
		return nil, err
	}

	source := strings.TrimSpace(rendered.String())
	rest, ok := strings.CutPrefix(source, "let")
	if !ok {
		return nil, fmt.Errorf("unexpected let rendering: %q", source)
	}

	file, err := parser.Parse(strings.NewReader(letHelperCommandName+rest+"\n"), "let-helper")
	if err != nil {
		return nil, err
	}
	if len(file.Stmts) != 1 {
		return nil, fmt.Errorf("unexpected let helper statement count: %d", len(file.Stmts))
	}

	call, ok := file.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok {
		return nil, fmt.Errorf("unexpected let helper command type: %T", file.Stmts[0].Cmd)
	}
	return call, nil
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
