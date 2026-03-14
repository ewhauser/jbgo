package builtins

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type xanExpr interface {
	eval(*xanEvalContext) (any, error)
}

type xanEvalContext struct {
	values   map[string]any
	rowIndex int
	current  any
}

type xanLiteralExpr struct {
	value any
}

type xanIdentifierExpr struct {
	name string
}

type xanUnderscoreExpr struct{}

type xanUnaryExpr struct {
	op   string
	expr xanExpr
}

type xanBinaryExpr struct {
	op    string
	left  xanExpr
	right xanExpr
}

type xanCallExpr struct {
	name string
	args []xanExpr
}

type xanIndexExpr struct {
	target xanExpr
	index  xanExpr
}

func (e xanLiteralExpr) eval(_ *xanEvalContext) (any, error) {
	return e.value, nil
}

func (e xanIdentifierExpr) eval(ctx *xanEvalContext) (any, error) {
	if ctx == nil {
		return nil, nil
	}
	return ctx.values[e.name], nil
}

func (xanUnderscoreExpr) eval(ctx *xanEvalContext) (any, error) {
	if ctx == nil {
		return nil, nil
	}
	return ctx.current, nil
}

func (e xanUnaryExpr) eval(ctx *xanEvalContext) (any, error) {
	value, err := e.expr.eval(ctx)
	if err != nil {
		return nil, err
	}
	switch e.op {
	case "-":
		if num, ok := xanAsFloat(value); ok {
			return -num, nil
		}
		return nil, nil
	case "!":
		return !xanTruthy(value), nil
	default:
		return nil, fmt.Errorf("unsupported unary operator %q", e.op)
	}
}

func (e xanBinaryExpr) eval(ctx *xanEvalContext) (any, error) {
	left, err := e.left.eval(ctx)
	if err != nil {
		return nil, err
	}
	right, err := e.right.eval(ctx)
	if err != nil {
		return nil, err
	}

	switch e.op {
	case "+":
		if lnum, lok := xanAsFloat(left); lok {
			if rnum, rok := xanAsFloat(right); rok {
				return lnum + rnum, nil
			}
		}
		return xanValueString(left) + xanValueString(right), nil
	case "-":
		return xanNumericBinary(left, right, func(a, b float64) float64 { return a - b })
	case "*":
		return xanNumericBinary(left, right, func(a, b float64) float64 { return a * b })
	case "/":
		return xanNumericBinary(left, right, func(a, b float64) float64 { return a / b })
	case "%":
		return xanNumericBinary(left, right, func(a, b float64) float64 { return math.Mod(a, b) })
	case "==":
		return xanCompareEq(left, right), nil
	case "!=":
		return !xanCompareEq(left, right), nil
	case "eq":
		return xanValueString(left) == xanValueString(right), nil
	case "ne":
		return xanValueString(left) != xanValueString(right), nil
	case "<":
		return xanCompareOrdered(left, right, func(c int) bool { return c < 0 })
	case "<=":
		return xanCompareOrdered(left, right, func(c int) bool { return c <= 0 })
	case ">":
		return xanCompareOrdered(left, right, func(c int) bool { return c > 0 })
	case ">=":
		return xanCompareOrdered(left, right, func(c int) bool { return c >= 0 })
	case "lt":
		return xanCompareOrderedStrings(left, right, func(c int) bool { return c < 0 }), nil
	case "le":
		return xanCompareOrderedStrings(left, right, func(c int) bool { return c <= 0 }), nil
	case "gt":
		return xanCompareOrderedStrings(left, right, func(c int) bool { return c > 0 }), nil
	case "ge":
		return xanCompareOrderedStrings(left, right, func(c int) bool { return c >= 0 }), nil
	case "&&", "and":
		return xanTruthy(left) && xanTruthy(right), nil
	case "||", "or":
		return xanTruthy(left) || xanTruthy(right), nil
	default:
		return nil, fmt.Errorf("unsupported operator %q", e.op)
	}
}

func (e xanCallExpr) eval(ctx *xanEvalContext) (any, error) {
	args := make([]any, 0, len(e.args))
	for _, arg := range e.args {
		value, err := arg.eval(ctx)
		if err != nil {
			return nil, err
		}
		args = append(args, value)
	}
	return xanEvalFunction(e.name, args, ctx)
}

func (e xanIndexExpr) eval(ctx *xanEvalContext) (any, error) {
	target, err := e.target.eval(ctx)
	if err != nil {
		return nil, err
	}
	indexValue, err := e.index.eval(ctx)
	if err != nil {
		return nil, err
	}
	index, ok := xanAsIndex(indexValue)
	if !ok {
		return nil, nil
	}
	switch v := target.(type) {
	case []any:
		if index < 0 || index >= len(v) {
			return nil, nil
		}
		return v[index], nil
	case []string:
		if index < 0 || index >= len(v) {
			return nil, nil
		}
		return v[index], nil
	case string:
		runes := []rune(v)
		if index < 0 || index >= len(runes) {
			return nil, nil
		}
		return string(runes[index]), nil
	default:
		return nil, nil
	}
}

func xanEvalFunction(name string, args []any, ctx *xanEvalContext) (any, error) {
	switch strings.ToLower(name) {
	case "index":
		if ctx == nil {
			return 0, nil
		}
		return ctx.rowIndex, nil
	case "add":
		var total float64
		for _, arg := range args {
			num, ok := xanAsFloat(arg)
			if !ok {
				return nil, nil
			}
			total += num
		}
		return total, nil
	case "mul":
		if len(args) == 0 {
			return 0, nil
		}
		total := 1.0
		for _, arg := range args {
			num, ok := xanAsFloat(arg)
			if !ok {
				return nil, nil
			}
			total *= num
		}
		return total, nil
	case "split":
		if len(args) == 0 {
			return []any{}, nil
		}
		source := xanValueString(args[0])
		sep := ""
		if len(args) > 1 {
			sep = xanValueString(args[1])
		}
		parts := strings.Split(source, sep)
		values := make([]any, 0, len(parts))
		for _, part := range parts {
			values = append(values, part)
		}
		return values, nil
	case "upper":
		if len(args) == 0 {
			return "", nil
		}
		return strings.ToUpper(xanValueString(args[0])), nil
	case "lower":
		if len(args) == 0 {
			return "", nil
		}
		return strings.ToLower(xanValueString(args[0])), nil
	case "trim":
		if len(args) == 0 {
			return "", nil
		}
		return strings.TrimSpace(xanValueString(args[0])), nil
	case "len":
		if len(args) == 0 {
			return 0, nil
		}
		switch v := args[0].(type) {
		case []any:
			return len(v), nil
		case []string:
			return len(v), nil
		default:
			return utf8.RuneCountInString(xanValueString(v)), nil
		}
	case "abs":
		if len(args) == 0 {
			return 0, nil
		}
		num, ok := xanAsFloat(args[0])
		if !ok {
			return nil, nil
		}
		return math.Abs(num), nil
	case "round":
		if len(args) == 0 {
			return 0, nil
		}
		num, ok := xanAsFloat(args[0])
		if !ok {
			return nil, nil
		}
		return math.Round(num), nil
	case "min":
		return xanMinMax(args, true), nil
	case "max":
		return xanMinMax(args, false), nil
	case "startswith":
		if len(args) < 2 {
			return false, nil
		}
		return strings.HasPrefix(xanValueString(args[0]), xanValueString(args[1])), nil
	case "endswith":
		if len(args) < 2 {
			return false, nil
		}
		return strings.HasSuffix(xanValueString(args[0]), xanValueString(args[1])), nil
	case "contains":
		if len(args) < 2 {
			return false, nil
		}
		return strings.Contains(xanValueString(args[0]), xanValueString(args[1])), nil
	case "if":
		if len(args) < 2 {
			return nil, nil
		}
		if xanTruthy(args[0]) {
			return args[1], nil
		}
		if len(args) > 2 {
			return args[2], nil
		}
		return nil, nil
	case "coalesce":
		for _, arg := range args {
			if !xanIsBlank(arg) {
				return arg, nil
			}
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported function %q", name)
	}
}

func xanNumericBinary(left, right any, fn func(float64, float64) float64) (any, error) {
	lnum, lok := xanAsFloat(left)
	rnum, rok := xanAsFloat(right)
	if !lok || !rok {
		return nil, nil
	}
	return fn(lnum, rnum), nil
}

func xanTruthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0 && !math.IsNaN(v)
	case float32:
		return v != 0 && !math.IsNaN(float64(v))
	case []any:
		return len(v) > 0
	case []string:
		return len(v) > 0
	default:
		return true
	}
}

func xanIsBlank(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return v == ""
	default:
		return false
	}
}

func xanAsFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case string:
		if v == "" {
			return 0, false
		}
		num, err := strconv.ParseFloat(v, 64)
		return num, err == nil
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}

func xanAsIndex(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		if math.Trunc(v) != v {
			return 0, false
		}
		return int(v), true
	case string:
		i, err := strconv.Atoi(v)
		return i, err == nil
	default:
		return 0, false
	}
}

func xanCompareEq(left, right any) bool {
	if lnum, lok := xanAsFloat(left); lok {
		if rnum, rok := xanAsFloat(right); rok {
			return lnum == rnum
		}
	}
	return xanValueString(left) == xanValueString(right)
}

func xanCompareOrdered(left, right any, match func(int) bool) (bool, error) {
	if lnum, lok := xanAsFloat(left); lok {
		if rnum, rok := xanAsFloat(right); rok {
			switch {
			case lnum < rnum:
				return match(-1), nil
			case lnum > rnum:
				return match(1), nil
			default:
				return match(0), nil
			}
		}
	}
	return match(strings.Compare(xanValueString(left), xanValueString(right))), nil
}

func xanCompareOrderedStrings(left, right any, match func(int) bool) bool {
	return match(strings.Compare(xanValueString(left), xanValueString(right)))
}

func xanMinMax(args []any, wantMin bool) any {
	if len(args) == 0 {
		return nil
	}
	allNumeric := true
	nums := make([]float64, 0, len(args))
	for _, arg := range args {
		num, ok := xanAsFloat(arg)
		if !ok {
			allNumeric = false
			break
		}
		nums = append(nums, num)
	}
	if allNumeric {
		best := nums[0]
		for _, num := range nums[1:] {
			if wantMin && num < best {
				best = num
			}
			if !wantMin && num > best {
				best = num
			}
		}
		return best
	}
	best := xanValueString(args[0])
	for _, arg := range args[1:] {
		value := xanValueString(arg)
		if wantMin && value < best {
			best = value
		}
		if !wantMin && value > best {
			best = value
		}
	}
	return best
}

func xanParseNamedExpressions(input string) ([]xanNamedExpr, error) {
	parts, err := xanSplitTopLevel(input)
	if err != nil {
		return nil, err
	}

	result := make([]xanNamedExpr, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		exprText, alias := xanSplitAlias(part)
		expr, err := xanParseExpr(exprText)
		if err != nil {
			return nil, err
		}
		if alias == "" {
			alias = strings.TrimSpace(exprText)
		}
		result = append(result, xanNamedExpr{Alias: alias, Expr: expr})
	}
	return result, nil
}

func xanParseAggSpecs(input string) ([]xanAggSpec, error) {
	parts, err := xanSplitTopLevel(input)
	if err != nil {
		return nil, err
	}

	specs := make([]xanAggSpec, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		exprText, alias := xanSplitAlias(part)
		funcName, inner, err := xanParseAggCall(exprText)
		if err != nil {
			return nil, err
		}
		var expr xanExpr
		if strings.TrimSpace(inner) != "" {
			expr, err = xanParseExpr(inner)
			if err != nil {
				return nil, err
			}
		}
		if alias == "" {
			alias = strings.TrimSpace(exprText)
		}
		specs = append(specs, xanAggSpec{
			Func:  strings.ToLower(funcName),
			Expr:  expr,
			Alias: alias,
			Raw:   strings.TrimSpace(exprText),
		})
	}
	return specs, nil
}

func xanParseAggCall(input string) (string, string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", fmt.Errorf("empty aggregation expression")
	}
	i := 0
	for i < len(input) && (unicode.IsLetter(rune(input[i])) || unicode.IsDigit(rune(input[i])) || input[i] == '_') {
		i++
	}
	if i == 0 || i >= len(input) || input[i] != '(' {
		return "", "", fmt.Errorf("invalid aggregation expression %q", input)
	}
	funcName := input[:i]
	depth := 0
	for j := i; j < len(input); j++ {
		switch input[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				if strings.TrimSpace(input[j+1:]) != "" {
					return "", "", fmt.Errorf("invalid aggregation expression %q", input)
				}
				return funcName, input[i+1 : j], nil
			}
		}
	}
	return "", "", fmt.Errorf("invalid aggregation expression %q", input)
}

func xanSplitTopLevel(input string) ([]string, error) {
	var (
		parts      []string
		start      int
		parenDepth int
		brackDepth int
		quote      byte
	)

	for i := 0; i < len(input); i++ {
		ch := input[i]
		if quote != 0 {
			if ch == '\\' {
				i++
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"':
			quote = ch
		case '(':
			parenDepth++
		case ')':
			parenDepth--
		case '[':
			brackDepth++
		case ']':
			brackDepth--
		case ',':
			if parenDepth == 0 && brackDepth == 0 {
				parts = append(parts, input[start:i])
				start = i + 1
			}
		}
		if parenDepth < 0 || brackDepth < 0 {
			return nil, fmt.Errorf("invalid expression %q", input)
		}
	}
	if quote != 0 || parenDepth != 0 || brackDepth != 0 {
		return nil, fmt.Errorf("invalid expression %q", input)
	}
	parts = append(parts, input[start:])
	return parts, nil
}

func xanSplitAlias(input string) (string, string) {
	lower := strings.ToLower(input)
	var (
		parenDepth int
		brackDepth int
		quote      byte
		last       = -1
	)
	for i := 0; i < len(input)-3; i++ {
		ch := input[i]
		if quote != 0 {
			if ch == '\\' {
				i++
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"':
			quote = ch
		case '(':
			parenDepth++
		case ')':
			parenDepth--
		case '[':
			brackDepth++
		case ']':
			brackDepth--
		default:
			if parenDepth == 0 && brackDepth == 0 && strings.HasPrefix(lower[i:], " as ") {
				last = i
			}
		}
	}
	if last == -1 {
		return strings.TrimSpace(input), ""
	}
	return strings.TrimSpace(input[:last]), strings.TrimSpace(input[last+4:])
}

func xanNewEvalContext(headers, row []string, rowIndex int, current any) *xanEvalContext {
	values := make(map[string]any, len(headers))
	for i, header := range headers {
		if i < len(row) {
			values[header] = xanParseScalar(row[i])
		} else {
			values[header] = ""
		}
	}
	return &xanEvalContext{values: values, rowIndex: rowIndex, current: current}
}

func xanEvalRowExpr(headers, row []string, rowIndex int, expr xanExpr, current any) (any, error) {
	return expr.eval(xanNewEvalContext(headers, row, rowIndex, current))
}

func xanComputeAgg(headers []string, rows [][]string, spec xanAggSpec) (any, error) {
	if spec.Func == "count" && spec.Expr == nil {
		return len(rows), nil
	}

	values := make([]any, 0, len(rows))
	for rowIndex, row := range rows {
		var value any
		if spec.Expr != nil {
			var err error
			value, err = xanEvalRowExpr(headers, row, rowIndex, spec.Expr, nil)
			if err != nil {
				return nil, err
			}
		}
		if spec.Expr == nil {
			value = nil
		}
		if value != nil {
			values = append(values, value)
		}
	}

	switch spec.Func {
	case "count":
		if spec.Expr == nil {
			return len(rows), nil
		}
		count := 0
		for rowIndex, row := range rows {
			value, err := xanEvalRowExpr(headers, row, rowIndex, spec.Expr, nil)
			if err != nil {
				return nil, err
			}
			if xanTruthy(value) {
				count++
			}
		}
		return count, nil
	case "sum":
		total := 0.0
		for _, value := range values {
			num, ok := xanAsFloat(value)
			if !ok {
				continue
			}
			total += num
		}
		return total, nil
	case "mean", "avg":
		nums := xanCollectNumbers(values)
		if len(nums) == 0 {
			return 0, nil
		}
		total := 0.0
		for _, num := range nums {
			total += num
		}
		return total / float64(len(nums)), nil
	case "min":
		nums := xanCollectNumbers(values)
		if len(nums) == 0 {
			return nil, nil
		}
		best := nums[0]
		for _, num := range nums[1:] {
			if num < best {
				best = num
			}
		}
		return best, nil
	case "max":
		nums := xanCollectNumbers(values)
		if len(nums) == 0 {
			return nil, nil
		}
		best := nums[0]
		for _, num := range nums[1:] {
			if num > best {
				best = num
			}
		}
		return best, nil
	case "first":
		if len(values) == 0 {
			return nil, nil
		}
		return values[0], nil
	case "last":
		if len(values) == 0 {
			return nil, nil
		}
		return values[len(values)-1], nil
	case "median":
		nums := xanCollectNumbers(values)
		if len(nums) == 0 {
			return nil, nil
		}
		sort.Float64s(nums)
		mid := len(nums) / 2
		if len(nums)%2 == 0 {
			return (nums[mid-1] + nums[mid]) / 2, nil
		}
		return nums[mid], nil
	case "mode":
		counts := make(map[string]int)
		order := make([]string, 0)
		for _, value := range values {
			key := xanValueString(value)
			if counts[key] == 0 {
				order = append(order, key)
			}
			counts[key]++
		}
		best := ""
		bestCount := -1
		for _, key := range order {
			if counts[key] > bestCount {
				best = key
				bestCount = counts[key]
			}
		}
		if bestCount < 0 {
			return nil, nil
		}
		return best, nil
	case "cardinality":
		seen := make(map[string]struct{})
		for _, value := range values {
			seen[xanValueString(value)] = struct{}{}
		}
		return len(seen), nil
	case "values":
		out := make([]string, 0, len(values))
		for _, value := range values {
			out = append(out, xanValueString(value))
		}
		return strings.Join(out, "|"), nil
	case "distinct_values":
		seen := make(map[string]struct{})
		out := make([]string, 0)
		for _, value := range values {
			key := xanValueString(value)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, key)
		}
		sort.Strings(out)
		return strings.Join(out, "|"), nil
	case "all":
		if len(rows) == 0 {
			return true, nil
		}
		for rowIndex, row := range rows {
			value, err := xanEvalRowExpr(headers, row, rowIndex, spec.Expr, nil)
			if err != nil {
				return nil, err
			}
			if !xanTruthy(value) {
				return false, nil
			}
		}
		return true, nil
	case "any":
		for rowIndex, row := range rows {
			value, err := xanEvalRowExpr(headers, row, rowIndex, spec.Expr, nil)
			if err != nil {
				return nil, err
			}
			if xanTruthy(value) {
				return true, nil
			}
		}
		return false, nil
	default:
		return nil, nil
	}
}

func xanCollectNumbers(values []any) []float64 {
	nums := make([]float64, 0, len(values))
	for _, value := range values {
		num, ok := xanAsFloat(value)
		if ok {
			nums = append(nums, num)
		}
	}
	return nums
}

type xanTokenKind int

const (
	xanTokenEOF xanTokenKind = iota
	xanTokenIdentifier
	xanTokenNumber
	xanTokenString
	xanTokenUnderscore
	xanTokenLParen
	xanTokenRParen
	xanTokenLBracket
	xanTokenRBracket
	xanTokenComma
	xanTokenOperator
)

type xanToken struct {
	kind xanTokenKind
	text string
}

type xanParser struct {
	tokens []xanToken
	pos    int
}

func xanParseExpr(input string) (xanExpr, error) {
	tokens, err := xanTokenize(input)
	if err != nil {
		return nil, err
	}
	parser := xanParser{tokens: tokens}
	expr, err := parser.parseExpr(0)
	if err != nil {
		return nil, err
	}
	if parser.peek().kind != xanTokenEOF {
		return nil, fmt.Errorf("unexpected token %q", parser.peek().text)
	}
	return expr, nil
}

var xanOperatorPrecedence = map[string]int{
	"||":  1,
	"or":  1,
	"&&":  2,
	"and": 2,
	"==":  3,
	"!=":  3,
	"eq":  3,
	"ne":  3,
	"<":   4,
	"<=":  4,
	">":   4,
	">=":  4,
	"lt":  4,
	"le":  4,
	"gt":  4,
	"ge":  4,
	"+":   5,
	"-":   5,
	"*":   6,
	"/":   6,
	"%":   6,
}

func (p *xanParser) parseExpr(minPrec int) (xanExpr, error) {
	left, err := p.parsePrefix()
	if err != nil {
		return nil, err
	}

	for {
		token := p.peek()
		if token.kind != xanTokenOperator {
			break
		}
		prec := xanOperatorPrecedence[token.text]
		if prec < minPrec {
			break
		}
		p.pos++
		right, err := p.parseExpr(prec + 1)
		if err != nil {
			return nil, err
		}
		left = xanBinaryExpr{op: token.text, left: left, right: right}
	}
	return left, nil
}

func (p *xanParser) parsePrefix() (xanExpr, error) {
	token := p.next()
	switch token.kind {
	case xanTokenNumber:
		if strings.Contains(token.text, ".") {
			value, err := strconv.ParseFloat(token.text, 64)
			if err != nil {
				return nil, err
			}
			return xanLiteralExpr{value: value}, nil
		}
		value, err := strconv.ParseInt(token.text, 10, 64)
		if err != nil {
			return nil, err
		}
		return xanLiteralExpr{value: value}, nil
	case xanTokenString:
		return xanLiteralExpr{value: token.text}, nil
	case xanTokenUnderscore:
		return p.parsePostfix(xanUnderscoreExpr{})
	case xanTokenIdentifier:
		lower := strings.ToLower(token.text)
		switch lower {
		case "true":
			return xanLiteralExpr{value: true}, nil
		case "false":
			return xanLiteralExpr{value: false}, nil
		case "null":
			return xanLiteralExpr{value: nil}, nil
		}
		if p.peek().kind == xanTokenLParen {
			p.pos++
			args, err := p.parseArguments()
			if err != nil {
				return nil, err
			}
			return p.parsePostfix(xanCallExpr{name: token.text, args: args})
		}
		return p.parsePostfix(xanIdentifierExpr{name: token.text})
	case xanTokenOperator:
		if token.text == "-" || token.text == "!" {
			expr, err := p.parseExpr(7)
			if err != nil {
				return nil, err
			}
			return xanUnaryExpr{op: token.text, expr: expr}, nil
		}
		return nil, fmt.Errorf("unexpected token %q", token.text)
	case xanTokenLParen:
		expr, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if p.peek().kind != xanTokenRParen {
			return nil, fmt.Errorf("expected )")
		}
		p.pos++
		return p.parsePostfix(expr)
	default:
		return nil, fmt.Errorf("unexpected token %q", token.text)
	}
}

func (p *xanParser) parseArguments() ([]xanExpr, error) {
	if p.peek().kind == xanTokenRParen {
		p.pos++
		return nil, nil
	}
	var args []xanExpr
	for {
		expr, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		args = append(args, expr)
		switch p.peek().kind {
		case xanTokenComma:
			p.pos++
		case xanTokenRParen:
			p.pos++
			return args, nil
		default:
			return nil, fmt.Errorf("expected , or )")
		}
	}
}

func (p *xanParser) parsePostfix(expr xanExpr) (xanExpr, error) {
	for p.peek().kind == xanTokenLBracket {
		p.pos++
		index, err := p.parseExpr(0)
		if err != nil {
			return nil, err
		}
		if p.peek().kind != xanTokenRBracket {
			return nil, fmt.Errorf("expected ]")
		}
		p.pos++
		expr = xanIndexExpr{target: expr, index: index}
	}
	return expr, nil
}

func (p *xanParser) peek() xanToken {
	if p.pos >= len(p.tokens) {
		return xanToken{kind: xanTokenEOF}
	}
	return p.tokens[p.pos]
}

func (p *xanParser) next() xanToken {
	token := p.peek()
	if p.pos < len(p.tokens) {
		p.pos++
	}
	return token
}

func xanTokenize(input string) ([]xanToken, error) {
	var tokens []xanToken
	for i := 0; i < len(input); {
		ch := input[i]
		switch {
		case unicode.IsSpace(rune(ch)):
			i++
		case ch == '(':
			tokens = append(tokens, xanToken{kind: xanTokenLParen, text: "("})
			i++
		case ch == ')':
			tokens = append(tokens, xanToken{kind: xanTokenRParen, text: ")"})
			i++
		case ch == '[':
			tokens = append(tokens, xanToken{kind: xanTokenLBracket, text: "["})
			i++
		case ch == ']':
			tokens = append(tokens, xanToken{kind: xanTokenRBracket, text: "]"})
			i++
		case ch == ',':
			tokens = append(tokens, xanToken{kind: xanTokenComma, text: ","})
			i++
		case ch == '_' && (i+1 >= len(input) || !isXanIdentPart(input[i+1])):
			tokens = append(tokens, xanToken{kind: xanTokenUnderscore, text: "_"})
			i++
		case ch == '\'' || ch == '"':
			text, next, err := xanReadQuoted(input, i)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, xanToken{kind: xanTokenString, text: text})
			i = next
		case isXanNumberStart(input, i):
			next := i + 1
			for next < len(input) && (unicode.IsDigit(rune(input[next])) || input[next] == '.') {
				next++
			}
			tokens = append(tokens, xanToken{kind: xanTokenNumber, text: input[i:next]})
			i = next
		case isXanIdentStart(ch):
			next := i + 1
			for next < len(input) && isXanIdentPart(input[next]) {
				next++
			}
			text := input[i:next]
			switch text {
			case "and", "or", "eq", "ne", "lt", "le", "gt", "ge":
				tokens = append(tokens, xanToken{kind: xanTokenOperator, text: text})
			default:
				tokens = append(tokens, xanToken{kind: xanTokenIdentifier, text: text})
			}
			i = next
		default:
			op, width := xanReadOperator(input[i:])
			if width == 0 {
				return nil, fmt.Errorf("unexpected character %q", ch)
			}
			tokens = append(tokens, xanToken{kind: xanTokenOperator, text: op})
			i += width
		}
	}
	tokens = append(tokens, xanToken{kind: xanTokenEOF})
	return tokens, nil
}

func xanReadQuoted(input string, start int) (string, int, error) {
	quote := input[start]
	var b strings.Builder
	for i := start + 1; i < len(input); i++ {
		ch := input[i]
		if ch == '\\' {
			if i+1 >= len(input) {
				return "", 0, fmt.Errorf("unterminated string")
			}
			i++
			b.WriteByte(input[i])
			continue
		}
		if ch == quote {
			return b.String(), i + 1, nil
		}
		b.WriteByte(ch)
	}
	return "", 0, fmt.Errorf("unterminated string")
}

func xanReadOperator(input string) (string, int) {
	for _, op := range []string{"&&", "||", "==", "!=", "<=", ">=", "+", "-", "*", "/", "%", "<", ">", "!"} {
		if strings.HasPrefix(input, op) {
			return op, len(op)
		}
	}
	return "", 0
}

func isXanIdentStart(ch byte) bool {
	return ch == '_' || unicode.IsLetter(rune(ch))
}

func isXanIdentPart(ch byte) bool {
	return ch == '_' || unicode.IsLetter(rune(ch)) || unicode.IsDigit(rune(ch))
}

func isXanNumberStart(input string, i int) bool {
	ch := input[i]
	if unicode.IsDigit(rune(ch)) {
		return true
	}
	if ch == '-' && i+1 < len(input) && unicode.IsDigit(rune(input[i+1])) {
		if i == 0 {
			return true
		}
		prev := input[i-1]
		return prev == '(' || prev == '[' || prev == ',' || unicode.IsSpace(rune(prev))
	}
	return false
}
