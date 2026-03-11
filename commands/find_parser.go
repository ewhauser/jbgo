package commands

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type findTokenKind int

const (
	findTokenExpr findTokenKind = iota
	findTokenOp
	findTokenNot
	findTokenLParen
	findTokenRParen
)

type findToken struct {
	kind findTokenKind
	expr findExpr
	op   findCompare
}

func parseFindCommandArgs(inv *Invocation) ([]string, findCommandOptions, findExpr, error) {
	args := inv.Args
	paths := make([]string, 0, len(args))
	for len(args) > 0 && !findStartsExpression(args[0]) {
		paths = append(paths, args[0])
		args = args[1:]
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}

	opts, expr, err := parseFindExpressionArgs(inv, args)
	if err != nil {
		return nil, findCommandOptions{}, nil, err
	}
	return paths, opts, expr, nil
}

func findStartsExpression(arg string) bool {
	return strings.HasPrefix(arg, "-") || arg == "!" || arg == "(" || arg == ")" || arg == "\\(" || arg == "\\)"
}

func parseFindExpressionArgs(inv *Invocation, args []string) (findCommandOptions, findExpr, error) {
	var opts findCommandOptions
	tokens := make([]findToken, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "(",
			"\\(":
			tokens = append(tokens, findToken{kind: findTokenLParen})
		case ")",
			"\\)":
			tokens = append(tokens, findToken{kind: findTokenRParen})
		case "-name":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, exitf(inv, 1, "find: missing argument to -name")
			}
			i++
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: &findNameExpr{pattern: args[i]}})
		case "-iname":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, exitf(inv, 1, "find: missing argument to -iname")
			}
			i++
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: &findNameExpr{pattern: args[i], ignoreCase: true}})
		case "-path":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, exitf(inv, 1, "find: missing argument to -path")
			}
			i++
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: &findPathExpr{pattern: args[i]}})
		case "-ipath":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, exitf(inv, 1, "find: missing argument to -ipath")
			}
			i++
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: &findPathExpr{pattern: args[i], ignoreCase: true}})
		case "-regex":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, exitf(inv, 1, "find: missing argument to -regex")
			}
			i++
			re, err := regexp.Compile(args[i])
			if err != nil {
				return findCommandOptions{}, nil, exitf(inv, 1, "find: invalid regular expression %q", args[i])
			}
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: &findRegexExpr{regex: re}})
		case "-iregex":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, exitf(inv, 1, "find: missing argument to -iregex")
			}
			i++
			pattern := "(?i)" + args[i]
			re, err := regexp.Compile(pattern)
			if err != nil {
				return findCommandOptions{}, nil, exitf(inv, 1, "find: invalid regular expression %q", args[i])
			}
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: &findRegexExpr{regex: re}})
		case "-type":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, exitf(inv, 1, "find: missing argument to -type")
			}
			i++
			if args[i] != "f" && args[i] != "d" {
				return findCommandOptions{}, nil, exitf(inv, 1, "find: Unknown argument to -type: %s", args[i])
			}
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: &findTypeExpr{fileType: args[i][0]}})
		case "-empty":
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: &findEmptyExpr{}})
		case "-mtime":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, exitf(inv, 1, "find: missing argument to -mtime")
			}
			i++
			expr, err := parseFindMTimeExpr(args[i])
			if err != nil {
				return findCommandOptions{}, nil, exitf(inv, 1, "find: invalid mtime %q", args[i])
			}
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: expr})
		case "-newer":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, exitf(inv, 1, "find: missing argument to -newer")
			}
			i++
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: &findNewerExpr{refPath: args[i]}})
		case "-size":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, exitf(inv, 1, "find: missing argument to -size")
			}
			i++
			expr, err := parseFindSizeExpr(args[i])
			if err != nil {
				return findCommandOptions{}, nil, exitf(inv, 1, "find: invalid size %q", args[i])
			}
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: expr})
		case "-maxdepth":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, exitf(inv, 1, "find: missing argument to -maxdepth")
			}
			i++
			maxDepth, err := strconv.Atoi(args[i])
			if err != nil || maxDepth < 0 {
				return findCommandOptions{}, nil, exitf(inv, 1, "find: invalid maxdepth %q", args[i])
			}
			opts.maxDepth = maxDepth
			opts.hasMaxDepth = true
		case "-a", "-and":
			tokens = append(tokens, findToken{kind: findTokenOp, op: findCompareExact})
		case "-o", "-or":
			tokens = append(tokens, findToken{kind: findTokenOp, op: findCompareMore})
		case "-not", "!":
			tokens = append(tokens, findToken{kind: findTokenNot})
		case "-print":
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: &findTrueExpr{}})
		default:
			if strings.HasPrefix(arg, "-") {
				return findCommandOptions{}, nil, exitf(inv, 1, "find: unknown predicate %q", arg)
			}
			return findCommandOptions{}, nil, exitf(inv, 1, "find: unexpected argument %q", arg)
		}
	}

	if len(tokens) == 0 {
		return opts, nil, nil
	}
	expr, err := buildFindExprTree(tokens)
	if err != nil {
		return findCommandOptions{}, nil, err
	}
	return opts, expr, nil
}

func parseFindMTimeExpr(value string) (*findMTimeExpr, error) {
	comparison := findCompareExact
	daysValue := value
	if strings.HasPrefix(value, "+") {
		comparison = findCompareMore
		daysValue = value[1:]
	} else if strings.HasPrefix(value, "-") {
		comparison = findCompareLess
		daysValue = value[1:]
	}
	days, err := strconv.Atoi(daysValue)
	if err != nil || days < 0 {
		return nil, fmt.Errorf("invalid mtime")
	}
	return &findMTimeExpr{days: days, comparison: comparison}, nil
}

func parseFindSizeExpr(value string) (*findSizeExpr, error) {
	comparison := findCompareExact
	sizeValue := value
	if strings.HasPrefix(value, "+") {
		comparison = findCompareMore
		sizeValue = value[1:]
	} else if strings.HasPrefix(value, "-") {
		comparison = findCompareLess
		sizeValue = value[1:]
	}

	match := regexp.MustCompile(`^(\d+)([ckMGb])?$`).FindStringSubmatch(sizeValue)
	if match == nil {
		return nil, fmt.Errorf("invalid size")
	}
	number, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid size")
	}
	unit := byte('b')
	if match[2] != "" {
		unit = match[2][0]
	}
	return &findSizeExpr{value: number, unit: unit, comparison: comparison}, nil
}

func buildFindExprTree(tokens []findToken) (findExpr, error) {
	pos := 0

	var parseOr func() (findExpr, error)
	var parseAnd func() (findExpr, error)
	var parseUnary func() (findExpr, error)
	var parsePrimary func() (findExpr, error)

	isPrimaryStart := func(token findToken) bool {
		return token.kind == findTokenExpr || token.kind == findTokenNot || token.kind == findTokenLParen
	}

	parsePrimary = func() (findExpr, error) {
		if pos >= len(tokens) {
			return nil, nil
		}
		token := tokens[pos]
		switch token.kind {
		case findTokenExpr:
			pos++
			return token.expr, nil
		case findTokenLParen:
			pos++
			expr, err := parseOr()
			if err != nil {
				return nil, err
			}
			if pos >= len(tokens) || tokens[pos].kind != findTokenRParen {
				return nil, fmt.Errorf("find: missing closing ')'")
			}
			pos++
			return expr, nil
		default:
			return nil, nil
		}
	}

	parseUnary = func() (findExpr, error) {
		if pos < len(tokens) && tokens[pos].kind == findTokenNot {
			pos++
			expr, err := parseUnary()
			if err != nil {
				return nil, err
			}
			if expr == nil {
				return nil, fmt.Errorf("find: missing expression after not")
			}
			return &findNotExpr{expr: expr}, nil
		}
		return parsePrimary()
	}

	parseAnd = func() (findExpr, error) {
		left, err := parseUnary()
		if err != nil || left == nil {
			return left, err
		}
		for pos < len(tokens) {
			token := tokens[pos]
			if token.kind == findTokenRParen {
				break
			}
			if token.kind == findTokenOp && token.op == findCompareMore {
				break
			}
			if token.kind == findTokenOp {
				pos++
			} else if !isPrimaryStart(token) {
				break
			}

			right, err := parseUnary()
			if err != nil {
				return nil, err
			}
			if right == nil {
				return nil, fmt.Errorf("find: missing expression")
			}
			left = &findAndExpr{left: left, right: right}
		}
		return left, nil
	}

	parseOr = func() (findExpr, error) {
		left, err := parseAnd()
		if err != nil || left == nil {
			return left, err
		}
		for pos < len(tokens) {
			token := tokens[pos]
			if token.kind != findTokenOp || token.op != findCompareMore {
				break
			}
			pos++
			right, err := parseAnd()
			if err != nil {
				return nil, err
			}
			if right == nil {
				return nil, fmt.Errorf("find: missing expression after -o")
			}
			left = &findOrExpr{left: left, right: right}
		}
		return left, nil
	}

	expr, err := parseOr()
	if err != nil {
		return nil, err
	}
	if pos != len(tokens) {
		return nil, fmt.Errorf("find: unexpected expression")
	}
	return expr, nil
}
