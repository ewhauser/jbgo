// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"fmt"

	"github.com/ewhauser/gbash/third_party/mvdan-sh/syntax"
)

const illegalTok = 0

type testParser struct {
	args []string
	err  func(err error)
}

func (p *testParser) errf(format string, a ...any) {
	p.err(fmt.Errorf(format, a...))
}

func testWord(val string) *syntax.Word {
	return &syntax.Word{Parts: []syntax.WordPart{
		&syntax.Lit{Value: val},
	}}
}

func (p *testParser) classicTest() syntax.TestExpr {
	expr, err := p.parseArgs(p.args)
	if err != nil {
		p.err(err)
		return nil
	}
	return expr
}

func (p *testParser) parseArgs(args []string) (syntax.TestExpr, error) {
	switch len(args) {
	case 0:
		return nil, nil
	case 1:
		return testWord(args[0]), nil
	case 2:
		return p.parseTwoArgs(args)
	case 3:
		return p.parseThreeArgs(args)
	case 4:
		switch {
		case args[0] == "!":
			expr, err := p.parseArgs(args[1:])
			if err != nil {
				return nil, err
			}
			return &syntax.UnaryTest{Op: syntax.TsNot, X: expr}, nil
		case args[0] == "(" && args[3] == ")":
			expr, err := p.parseArgs(args[1:3])
			if err != nil {
				return nil, err
			}
			return &syntax.ParenTest{X: expr}, nil
		}
	}
	expr, pos, err := p.parseOr(args, 0)
	if err != nil {
		return nil, err
	}
	if pos != len(args) {
		return nil, fmt.Errorf("extra argument %q", args[pos])
	}
	return expr, nil
}

func (p *testParser) parseTwoArgs(args []string) (syntax.TestExpr, error) {
	switch {
	case args[1] == "-a" || args[1] == "-o":
		return nil, fmt.Errorf("%s must be followed by an expression", args[1])
	case testExprBinaryOp(args[1]) != illegalTok:
		return nil, fmt.Errorf("%s must be followed by a word", args[1])
	case args[0] == "!":
		return &syntax.UnaryTest{Op: syntax.TsNot, X: testWord(args[1])}, nil
	case isClassicUnaryOp(args[0]):
		return &syntax.UnaryTest{Op: testUnaryOp(args[0]), X: testWord(args[1])}, nil
	default:
		return nil, fmt.Errorf("%s: unary operator expected", args[0])
	}
}

func (p *testParser) parseThreeArgs(args []string) (syntax.TestExpr, error) {
	if op := testBinaryOp(args[1]); op != illegalTok {
		return &syntax.BinaryTest{
			Op: op,
			X:  testWord(args[0]),
			Y:  testWord(args[2]),
		}, nil
	}
	switch {
	case args[0] == "!":
		expr, err := p.parseArgs(args[1:])
		if err != nil {
			return nil, err
		}
		return &syntax.UnaryTest{Op: syntax.TsNot, X: expr}, nil
	case args[0] == "(" && args[2] == ")":
		return testWord(args[1]), nil
	default:
		return nil, fmt.Errorf("not a valid test operator: %#q", args[1])
	}
}

func (p *testParser) parseOr(args []string, pos int) (syntax.TestExpr, int, error) {
	expr, pos, err := p.parseAnd(args, pos)
	if err != nil {
		return nil, pos, err
	}
	for pos < len(args) && args[pos] == "-o" {
		if pos+1 >= len(args) {
			return nil, pos, fmt.Errorf("argument expected")
		}
		right, next, err := p.parseAnd(args, pos+1)
		if err != nil {
			return nil, pos, err
		}
		expr = &syntax.BinaryTest{Op: syntax.OrTest, X: expr, Y: right}
		pos = next
	}
	return expr, pos, nil
}

func (p *testParser) parseAnd(args []string, pos int) (syntax.TestExpr, int, error) {
	expr, pos, err := p.parseNot(args, pos)
	if err != nil {
		return nil, pos, err
	}
	for pos < len(args) && args[pos] == "-a" {
		if pos+1 >= len(args) {
			return nil, pos, fmt.Errorf("argument expected")
		}
		right, next, err := p.parseNot(args, pos+1)
		if err != nil {
			return nil, pos, err
		}
		expr = &syntax.BinaryTest{Op: syntax.AndTest, X: expr, Y: right}
		pos = next
	}
	return expr, pos, nil
}

func (p *testParser) parseNot(args []string, pos int) (syntax.TestExpr, int, error) {
	if pos >= len(args) {
		return nil, pos, fmt.Errorf("argument expected")
	}
	if args[pos] == "!" {
		expr, next, err := p.parseNot(args, pos+1)
		if err != nil {
			return nil, pos, err
		}
		return &syntax.UnaryTest{Op: syntax.TsNot, X: expr}, next, nil
	}
	return p.parsePrimary(args, pos)
}

func (p *testParser) parsePrimary(args []string, pos int) (syntax.TestExpr, int, error) {
	if pos >= len(args) {
		return nil, pos, fmt.Errorf("argument expected")
	}
	if args[pos] == "(" {
		expr, next, err := p.parseOr(args, pos+1)
		if err != nil {
			return nil, pos, err
		}
		if next >= len(args) || args[next] != ")" {
			return nil, pos, fmt.Errorf("reached %q without matching '(' with ')'", args[len(args)-1])
		}
		return &syntax.ParenTest{X: expr}, next + 1, nil
	}
	if pos+2 < len(args) {
		if op := testExprBinaryOp(args[pos+1]); op != illegalTok {
			return &syntax.BinaryTest{
				Op: op,
				X:  testWord(args[pos]),
				Y:  testWord(args[pos+2]),
			}, pos + 3, nil
		}
	}
	if isClassicUnaryOp(args[pos]) && pos+1 < len(args) {
		return &syntax.UnaryTest{
			Op: testUnaryOp(args[pos]),
			X:  testWord(args[pos+1]),
		}, pos + 2, nil
	}
	return testWord(args[pos]), pos + 1, nil
}

func isClassicUnaryOp(val string) bool {
	op := testUnaryOp(val)
	return op != illegalTok && op != syntax.TsNot && op != syntax.TsParen
}

// testUnaryOp is an exact copy of syntax's.
func testUnaryOp(val string) syntax.UnTestOperator {
	switch val {
	case "!":
		return syntax.TsNot
	case "(":
		return syntax.TsParen
	case "-e", "-a":
		return syntax.TsExists
	case "-f":
		return syntax.TsRegFile
	case "-d":
		return syntax.TsDirect
	case "-c":
		return syntax.TsCharSp
	case "-b":
		return syntax.TsBlckSp
	case "-p":
		return syntax.TsNmPipe
	case "-S":
		return syntax.TsSocket
	case "-L", "-h":
		return syntax.TsSmbLink
	case "-k":
		return syntax.TsSticky
	case "-g":
		return syntax.TsGIDSet
	case "-u":
		return syntax.TsUIDSet
	case "-G":
		return syntax.TsGrpOwn
	case "-O":
		return syntax.TsUsrOwn
	case "-N":
		return syntax.TsModif
	case "-r":
		return syntax.TsRead
	case "-w":
		return syntax.TsWrite
	case "-x":
		return syntax.TsExec
	case "-s":
		return syntax.TsNoEmpty
	case "-t":
		return syntax.TsFdTerm
	case "-z":
		return syntax.TsEmpStr
	case "-n":
		return syntax.TsNempStr
	case "-o":
		return syntax.TsOptSet
	case "-v":
		return syntax.TsVarSet
	case "-R":
		return syntax.TsRefVar
	default:
		return illegalTok
	}
}

func testExprBinaryOp(val string) syntax.BinTestOperator {
	switch val {
	case "==", "=":
		return syntax.TsMatch
	case "!=":
		return syntax.TsNoMatch
	case "<":
		return syntax.TsBefore
	case ">":
		return syntax.TsAfter
	case "-nt":
		return syntax.TsNewer
	case "-ot":
		return syntax.TsOlder
	case "-ef":
		return syntax.TsDevIno
	case "-eq":
		return syntax.TsEql
	case "-ne":
		return syntax.TsNeq
	case "-le":
		return syntax.TsLeq
	case "-ge":
		return syntax.TsGeq
	case "-lt":
		return syntax.TsLss
	case "-gt":
		return syntax.TsGtr
	default:
		return illegalTok
	}
}

// testBinaryOp is like syntax's, but with -a and -o, and without =~.
func testBinaryOp(val string) syntax.BinTestOperator {
	switch val {
	case "-a":
		return syntax.AndTest
	case "-o":
		return syntax.OrTest
	default:
		return testExprBinaryOp(val)
	}
}
