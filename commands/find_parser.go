package commands

import (
	"fmt"
	stdfs "io/fs"
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

func parseFindCommandArgs(inv *Invocation) ([]string, findCommandOptions, findExpr, []findAction, error) {
	args := inv.Args
	paths := make([]string, 0, len(args))
	for len(args) > 0 && !findStartsExpression(args[0]) {
		paths = append(paths, args[0])
		args = args[1:]
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}

	opts, expr, actions, err := parseFindExpressionArgs(inv, args)
	if err != nil {
		return nil, findCommandOptions{}, nil, nil, err
	}
	return paths, opts, expr, actions, nil
}

func findStartsExpression(arg string) bool {
	return strings.HasPrefix(arg, "-") || arg == "!" || arg == "(" || arg == ")" || arg == "\\(" || arg == "\\)"
}

func parseFindExpressionArgs(inv *Invocation, args []string) (findCommandOptions, findExpr, []findAction, error) {
	var opts findCommandOptions
	tokens := make([]findToken, 0, len(args))
	actions := make([]findAction, 0)

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
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: missing argument to -name")
			}
			i++
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: &findNameExpr{pattern: args[i]}})
		case "-iname":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: missing argument to -iname")
			}
			i++
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: &findNameExpr{pattern: args[i], ignoreCase: true}})
		case "-path":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: missing argument to -path")
			}
			i++
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: &findPathExpr{pattern: args[i]}})
		case "-ipath":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: missing argument to -ipath")
			}
			i++
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: &findPathExpr{pattern: args[i], ignoreCase: true}})
		case "-regex":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: missing argument to -regex")
			}
			i++
			re, err := regexp.Compile(args[i])
			if err != nil {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: invalid regular expression %q", args[i])
			}
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: &findRegexExpr{regex: re}})
		case "-iregex":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: missing argument to -iregex")
			}
			i++
			pattern := "(?i)" + args[i]
			re, err := regexp.Compile(pattern)
			if err != nil {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: invalid regular expression %q", args[i])
			}
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: &findRegexExpr{regex: re}})
		case "-type":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: missing argument to -type")
			}
			i++
			if args[i] != "f" && args[i] != "d" {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: Unknown argument to -type: %s", args[i])
			}
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: &findTypeExpr{fileType: args[i][0]}})
		case "-empty":
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: &findEmptyExpr{}})
		case "-mtime":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: missing argument to -mtime")
			}
			i++
			expr, err := parseFindMTimeExpr(args[i])
			if err != nil {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: invalid mtime %q", args[i])
			}
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: expr})
		case "-newer":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: missing argument to -newer")
			}
			i++
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: &findNewerExpr{refPath: args[i]}})
		case "-size":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: missing argument to -size")
			}
			i++
			expr, err := parseFindSizeExpr(args[i])
			if err != nil {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: invalid size %q", args[i])
			}
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: expr})
		case "-perm":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: missing argument to -perm")
			}
			i++
			expr, err := parseFindPermExpr(args[i])
			if err != nil {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: invalid perm %q", args[i])
			}
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: expr})
		case "-prune":
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: &findPruneExpr{}})
		case "-maxdepth":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: missing argument to -maxdepth")
			}
			i++
			maxDepth, err := strconv.Atoi(args[i])
			if err != nil || maxDepth < 0 {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: invalid maxdepth %q", args[i])
			}
			opts.maxDepth = maxDepth
			opts.hasMaxDepth = true
		case "-mindepth":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: missing argument to -mindepth")
			}
			i++
			minDepth, err := strconv.Atoi(args[i])
			if err != nil || minDepth < 0 {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: invalid mindepth %q", args[i])
			}
			opts.minDepth = minDepth
			opts.hasMinDepth = true
		case "-depth":
			opts.depthFirst = true
		case "-a", "-and":
			tokens = append(tokens, findToken{kind: findTokenOp, op: findCompareExact})
		case "-o", "-or":
			tokens = append(tokens, findToken{kind: findTokenOp, op: findCompareMore})
		case "-not", "!":
			tokens = append(tokens, findToken{kind: findTokenNot})
		case "-print":
			tokens = append(tokens, findToken{kind: findTokenExpr, expr: &findPrintExpr{}})
			actions = append(actions, &findPrintAction{})
		case "-print0":
			actions = append(actions, &findPrint0Action{})
		case "-printf":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: missing argument to -printf")
			}
			i++
			actions = append(actions, &findPrintfAction{format: args[i]})
		case "-delete":
			actions = append(actions, &findDeleteAction{})
		case "-exec":
			if i+1 >= len(args) {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: missing argument to -exec")
			}
			command := make([]string, 0)
			i++
			for ; i < len(args) && args[i] != ";" && args[i] != "+"; i++ {
				command = append(command, args[i])
			}
			if i >= len(args) {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: missing argument to -exec")
			}
			actions = append(actions, &findExecAction{command: command, batchMode: args[i] == "+"})
		default:
			if strings.HasPrefix(arg, "-") {
				return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: unknown predicate %q", arg)
			}
			return findCommandOptions{}, nil, nil, exitf(inv, 1, "find: unexpected argument %q", arg)
		}
	}

	if len(tokens) == 0 {
		return opts, nil, actions, nil
	}
	expr, err := buildFindExprTree(tokens)
	if err != nil {
		return findCommandOptions{}, nil, nil, err
	}
	return opts, expr, actions, nil
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

func parseFindPermExpr(value string) (*findPermExpr, error) {
	matchType := findPermExact
	modeValue := value
	switch {
	case strings.HasPrefix(value, "-"):
		matchType = findPermAll
		modeValue = value[1:]
	case strings.HasPrefix(value, "/"):
		matchType = findPermAny
		modeValue = value[1:]
	}
	parsed, err := strconv.ParseUint(modeValue, 8, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid perm")
	}
	return &findPermExpr{mode: stdfs.FileMode(parsed), matchType: matchType}, nil
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
