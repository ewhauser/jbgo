package builtins

import (
	"context"
	"fmt"
	stdfs "io/fs"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
	"golang.org/x/term"
)

type Test struct {
	name      string
	bracketed bool
}

func NewTest() *Test {
	return &Test{name: "test"}
}

func NewBracketTest() *Test {
	return &Test{name: "[", bracketed: true}
}

func (c *Test) Name() string {
	return c.name
}

func (c *Test) Run(ctx context.Context, inv *Invocation) error {
	args := append([]string(nil), inv.Args...)
	if c.bracketed && len(args) == 1 {
		switch args[0] {
		case "--help":
			_, _ = fmt.Fprint(inv.Stdout, testBracketHelpText)
			return nil
		case "--version":
			_, _ = fmt.Fprint(inv.Stdout, testBracketVersionText)
			return nil
		}
	}
	if c.bracketed {
		if len(args) == 0 || args[len(args)-1] != "]" {
			return exitf(inv, 2, "[: missing ']'")
		}
		args = args[:len(args)-1]
	}

	stack, err := parseTest(args)
	if err != nil {
		return exitf(inv, 2, "%s: %s", c.name, err.Error())
	}
	ok, err := evalTest(ctx, inv, stack)
	if err != nil {
		return exitf(inv, 2, "%s: %s", c.name, err.Error())
	}
	if ok {
		return nil
	}
	return &ExitError{Code: 1}
}

type testSymbolKind int

const (
	testSymbolNone testSymbolKind = iota
	testSymbolLiteral
	testSymbolLParen
	testSymbolBang
	testSymbolBoolOp
	testSymbolStringOp
	testSymbolIntOp
	testSymbolFileOp
	testSymbolUnaryStr
	testSymbolUnaryFile
)

type testSymbol struct {
	kind  testSymbolKind
	token string
}

func parseTest(args []string) ([]testSymbol, error) {
	p := &testRPNParser{args: append([]string(nil), args...)}
	return p.parseArgs(p.args)
}

type testRPNParser struct {
	args []string
}

func (p *testRPNParser) parseArgs(args []string) ([]testSymbol, error) {
	switch len(args) {
	case 0:
		return nil, nil
	case 1:
		return []testSymbol{testLiteralSymbol(args[0])}, nil
	case 2:
		return p.parseTwoArgs(args)
	case 3:
		return p.parseThreeArgs(args)
	case 4:
		switch {
		case args[0] == "!":
			stack, err := p.parseArgs(args[1:])
			if err != nil {
				return nil, err
			}
			return append(stack, testSymbol{kind: testSymbolBang, token: "!"}), nil
		case args[0] == "(" && args[3] == ")":
			return p.parseArgs(args[1:3])
		}
	}
	stack, pos, err := p.parseOr(args, 0)
	if err != nil {
		return nil, err
	}
	if pos != len(args) {
		return nil, testParseExtraArgument(args[pos])
	}
	return stack, nil
}

func (p *testRPNParser) parseTwoArgs(args []string) ([]testSymbol, error) {
	switch {
	case args[0] == "!":
		return []testSymbol{
			testLiteralSymbol(args[1]),
			{kind: testSymbolBang, token: "!"},
		}, nil
	case isTestUnaryOp(args[0]):
		return []testSymbol{
			testLiteralSymbol(args[1]),
			testUnarySymbol(args[0]),
		}, nil
	case args[1] == "-a" || args[1] == "-o":
		return nil, testParseMissingExpression(args[1])
	case isTestExprBinaryOp(args[1]):
		return nil, testParseMissingArgument(quoteGNUOperand(args[1]))
	default:
		return nil, testParseUnaryOperatorExpected(quoteGNUOperand(args[0]))
	}
}

func (p *testRPNParser) parseThreeArgs(args []string) ([]testSymbol, error) {
	if op, ok := testBinarySymbol(args[1], true); ok {
		return []testSymbol{
			testLiteralSymbol(args[0]),
			testLiteralSymbol(args[2]),
			op,
		}, nil
	}
	switch {
	case args[0] == "!":
		stack, err := p.parseArgs(args[1:])
		if err != nil {
			return nil, err
		}
		return append(stack, testSymbol{kind: testSymbolBang, token: "!"}), nil
	case args[0] == "(" && args[2] == ")":
		return []testSymbol{testLiteralSymbol(args[1])}, nil
	default:
		return nil, testParseBinaryOperatorExpected(quoteGNUOperand(args[1]))
	}
}

func (p *testRPNParser) parseOr(args []string, pos int) ([]testSymbol, int, error) {
	stack, pos, err := p.parseAnd(args, pos)
	if err != nil {
		return nil, pos, err
	}
	for pos < len(args) && args[pos] == "-o" {
		if pos+1 >= len(args) {
			return nil, pos, testParseExpectedValue()
		}
		right, next, err := p.parseAnd(args, pos+1)
		if err != nil {
			return nil, pos, err
		}
		stack = append(stack, right...)
		stack = append(stack, testSymbol{kind: testSymbolBoolOp, token: "-o"})
		pos = next
	}
	return stack, pos, nil
}

func (p *testRPNParser) parseAnd(args []string, pos int) ([]testSymbol, int, error) {
	stack, pos, err := p.parseNot(args, pos)
	if err != nil {
		return nil, pos, err
	}
	for pos < len(args) && args[pos] == "-a" {
		if pos+1 >= len(args) {
			return nil, pos, testParseExpectedValue()
		}
		right, next, err := p.parseNot(args, pos+1)
		if err != nil {
			return nil, pos, err
		}
		stack = append(stack, right...)
		stack = append(stack, testSymbol{kind: testSymbolBoolOp, token: "-a"})
		pos = next
	}
	return stack, pos, nil
}

func (p *testRPNParser) parseNot(args []string, pos int) ([]testSymbol, int, error) {
	if pos >= len(args) {
		return nil, pos, testParseExpectedValue()
	}
	if args[pos] == "!" {
		stack, next, err := p.parseNot(args, pos+1)
		if err != nil {
			return nil, pos, err
		}
		return append(stack, testSymbol{kind: testSymbolBang, token: "!"}), next, nil
	}
	return p.parsePrimary(args, pos)
}

func (p *testRPNParser) parsePrimary(args []string, pos int) ([]testSymbol, int, error) {
	if pos >= len(args) {
		return nil, pos, testParseExpectedValue()
	}
	if args[pos] == "(" {
		depth := 1
		groupStarts := []int{pos}
		end := pos + 1
		for end < len(args) && depth > 0 {
			switch args[end] {
			case "(":
				currentGroupStart := groupStarts[len(groupStarts)-1]
				if p.parenStartsNestedGroup(args, currentGroupStart, end) {
					depth++
					groupStarts = append(groupStarts, end)
				}
			case ")":
				depth--
				groupStarts = groupStarts[:len(groupStarts)-1]
			}
			end++
		}
		if depth != 0 {
			return nil, pos, testParseExpected(")")
		}
		inner := args[pos+1 : end-1]
		if len(inner) == 0 {
			return nil, pos, testParseExpected(")")
		}
		stack, err := p.parseArgs(inner)
		if err != nil {
			return nil, pos, err
		}
		return stack, end, nil
	}
	if pos+2 < len(args) && isTestExprBinaryOp(args[pos+1]) {
		op, _ := testBinarySymbol(args[pos+1], false)
		return []testSymbol{
			testLiteralSymbol(args[pos]),
			testLiteralSymbol(args[pos+2]),
			op,
		}, pos + 3, nil
	}
	if isTestUnaryOp(args[pos]) && pos+1 < len(args) {
		return []testSymbol{
			testLiteralSymbol(args[pos+1]),
			testUnarySymbol(args[pos]),
		}, pos + 2, nil
	}
	return []testSymbol{testLiteralSymbol(args[pos])}, pos + 1, nil
}

func (p *testRPNParser) parenStartsNestedGroup(args []string, groupPos, idx int) bool {
	if idx > groupPos+1 {
		prev := args[idx-1]
		if prev == "-a" || prev == "-o" {
			return p.groupPrefixParsesAsExpr(args[groupPos+1 : idx-1])
		}
		return !isTestExprBinaryOp(prev) && !isTestUnaryOp(prev)
	}
	nextClose := -1
	for i := idx + 1; i < len(args); i++ {
		if args[i] == ")" {
			nextClose = i
			break
		}
	}
	if nextClose < 0 {
		return true
	}
	if nextClose == idx+2 {
		return false
	}
	next := args[idx+1]
	return !isTestExprBinaryOp(next) && next != "-a" && next != "-o"
}

func (p *testRPNParser) groupPrefixParsesAsExpr(args []string) bool {
	if len(args) == 0 {
		return false
	}
	_, next, err := p.parseOr(args, 0)
	return err == nil && next == len(args)
}

func testLiteralSymbol(token string) testSymbol {
	return testSymbol{kind: testSymbolLiteral, token: token}
}

func isTestUnaryOp(token string) bool {
	switch token {
	case "-n", "-z",
		"-a",
		"-b", "-c", "-d", "-e", "-f", "-g", "-G", "-h", "-k", "-L", "-N", "-O", "-p", "-r", "-s", "-S", "-t", "-u", "-w", "-x":
		return true
	default:
		return false
	}
}

func testUnarySymbol(token string) testSymbol {
	switch token {
	case "-n", "-z":
		return testSymbol{kind: testSymbolUnaryStr, token: token}
	default:
		return testSymbol{kind: testSymbolUnaryFile, token: token}
	}
}

func isTestExprBinaryOp(token string) bool {
	switch token {
	case "=", "==", "!=", "<", ">",
		"-eq", "-ge", "-gt", "-le", "-lt", "-ne",
		"-ef", "-nt", "-ot":
		return true
	default:
		return false
	}
}

func testBinarySymbol(token string, allowBool bool) (testSymbol, bool) {
	switch token {
	case "-a", "-o":
		if !allowBool {
			return testSymbol{}, false
		}
		return testSymbol{kind: testSymbolBoolOp, token: token}, true
	case "=", "==", "!=", "<", ">":
		return testSymbol{kind: testSymbolStringOp, token: token}, true
	case "-eq", "-ge", "-gt", "-le", "-lt", "-ne":
		return testSymbol{kind: testSymbolIntOp, token: token}, true
	case "-ef", "-nt", "-ot":
		return testSymbol{kind: testSymbolFileOp, token: token}, true
	default:
		return testSymbol{}, false
	}
}

func evalTest(ctx context.Context, inv *Invocation, stack []testSymbol) (bool, error) {
	work := append([]testSymbol(nil), stack...)
	return evalTestStack(ctx, inv, &work)
}

func evalTestStack(ctx context.Context, inv *Invocation, stack *[]testSymbol) (bool, error) {
	if len(*stack) == 0 {
		return false, nil
	}
	symbol := (*stack)[len(*stack)-1]
	*stack = (*stack)[:len(*stack)-1]

	switch symbol.kind {
	case testSymbolBang:
		result, err := evalTestStack(ctx, inv, stack)
		if err != nil {
			return false, err
		}
		return !result, nil
	case testSymbolStringOp:
		b, err := testPopLiteral(stack)
		if err != nil {
			return false, err
		}
		a, err := testPopLiteral(stack)
		if err != nil {
			return false, err
		}
		switch symbol.token {
		case "!=":
			return a != b, nil
		case "<":
			return a < b, nil
		case ">":
			return a > b, nil
		default:
			return a == b, nil
		}
	case testSymbolIntOp:
		b, err := testPopLiteral(stack)
		if err != nil {
			return false, err
		}
		a, err := testPopLiteral(stack)
		if err != nil {
			return false, err
		}
		return testCompareIntegers(a, b, symbol.token)
	case testSymbolFileOp:
		b, err := testPopLiteral(stack)
		if err != nil {
			return false, err
		}
		a, err := testPopLiteral(stack)
		if err != nil {
			return false, err
		}
		return testCompareFiles(ctx, inv, a, b, symbol.token)
	case testSymbolUnaryStr:
		if len(*stack) == 0 {
			return true, nil
		}
		next := (*stack)[len(*stack)-1]
		*stack = (*stack)[:len(*stack)-1]
		value := ""
		switch next.kind {
		case testSymbolLiteral:
			value = next.token
		case testSymbolNone:
			value = ""
		default:
			return false, testParseMissingArgument(symbol.display())
		}
		if symbol.token == "-z" {
			return value == "", nil
		}
		return value != "", nil
	case testSymbolUnaryFile:
		name, err := testPopLiteral(stack)
		if err != nil {
			return false, err
		}
		return testFilePredicate(ctx, inv, name, symbol.token)
	case testSymbolLiteral:
		return symbol.token != "", nil
	case testSymbolNone:
		return false, nil
	case testSymbolBoolOp:
		if len(*stack) < 2 {
			return false, testParseUnaryOperatorExpected(symbol.display())
		}
		right, err := evalTestStack(ctx, inv, stack)
		if err != nil {
			return false, err
		}
		left, err := evalTestStack(ctx, inv, stack)
		if err != nil {
			return false, err
		}
		if symbol.token == "-a" {
			return left && right, nil
		}
		return left || right, nil
	default:
		return false, testParseExpectedValue()
	}
}

func testPopLiteral(stack *[]testSymbol) (string, error) {
	if len(*stack) == 0 {
		return "", testParseExpectedValue()
	}
	symbol := (*stack)[len(*stack)-1]
	*stack = (*stack)[:len(*stack)-1]
	if symbol.kind != testSymbolLiteral {
		return "", testParseExpectedValue()
	}
	return symbol.token, nil
}

func (s testSymbol) display() string {
	switch s.kind {
	case testSymbolNone:
		return "None"
	case testSymbolLParen:
		return quoteGNUOperand("(")
	case testSymbolBang:
		return quoteGNUOperand("!")
	default:
		return quoteGNUOperand(s.token)
	}
}

func testCompareIntegers(a, b, op string) (bool, error) {
	left, ok := parseDecimalBigInt(strings.TrimSpace(a))
	if !ok {
		return false, testParseInvalidInteger(a)
	}
	right, ok := parseDecimalBigInt(strings.TrimSpace(b))
	if !ok {
		return false, testParseInvalidInteger(b)
	}
	switch op {
	case "-eq":
		return left.Cmp(right) == 0, nil
	case "-ne":
		return left.Cmp(right) != 0, nil
	case "-gt":
		return left.Cmp(right) > 0, nil
	case "-ge":
		return left.Cmp(right) >= 0, nil
	case "-lt":
		return left.Cmp(right) < 0, nil
	case "-le":
		return left.Cmp(right) <= 0, nil
	default:
		return false, testParseUnknownOperator(op)
	}
}

func testCompareFiles(ctx context.Context, inv *Invocation, a, b, op string) (bool, error) {
	infoA, absA, existsA, err := statMaybe(ctx, inv, a)
	if err != nil {
		return false, err
	}
	infoB, absB, existsB, err := statMaybe(ctx, inv, b)
	if err != nil {
		return false, err
	}
	switch op {
	case "-ef":
		if !existsA || !existsB {
			return false, nil
		}
		if absA == absB {
			return true, nil
		}
		if realA, errA := inv.FS.Realpath(ctx, absA); errA == nil {
			if realB, errB := inv.FS.Realpath(ctx, absB); errB == nil && realA == realB {
				return true, nil
			}
		}
		return testSameFile(infoA, infoB), nil
	case "-nt":
		switch {
		case existsA && existsB:
			return infoA.ModTime().After(infoB.ModTime()), nil
		case existsA:
			return true, nil
		default:
			return false, nil
		}
	case "-ot":
		switch {
		case existsA && existsB:
			return infoA.ModTime().Before(infoB.ModTime()), nil
		case existsB:
			return true, nil
		default:
			return false, nil
		}
	default:
		return false, testParseUnknownOperator(op)
	}
}

func testFilePredicate(ctx context.Context, inv *Invocation, name, op string) (bool, error) {
	if op == "-t" {
		fd, ok := parseDecimalBigInt(strings.TrimSpace(name))
		if !ok {
			return false, testParseInvalidInteger(name)
		}
		return testIsTTY(inv, int(fd.Int64())), nil
	}
	if op == "-h" || op == "-L" {
		info, _, exists, err := lstatMaybe(ctx, inv, name)
		if err != nil {
			return false, err
		}
		return exists && info.Mode()&stdfs.ModeSymlink != 0, nil
	}

	info, _, exists, err := statMaybe(ctx, inv, name)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}

	switch op {
	case "-b":
		return info.Mode()&stdfs.ModeDevice != 0 && info.Mode()&stdfs.ModeCharDevice == 0, nil
	case "-c":
		return info.Mode()&stdfs.ModeDevice != 0 && info.Mode()&stdfs.ModeCharDevice != 0, nil
	case "-d":
		return info.IsDir(), nil
	case "-e":
		return true, nil
	case "-f":
		return info.Mode().IsRegular(), nil
	case "-g":
		return info.Mode()&0o2000 != 0, nil
	case "-G":
		return testCurrentGroupOwns(inv, info), nil
	case "-k":
		return info.Mode()&0o1000 != 0, nil
	case "-N":
		return testModifiedAfterAccess(info), nil
	case "-O":
		return testCurrentUserOwns(inv, info), nil
	case "-p":
		return info.Mode()&stdfs.ModeNamedPipe != 0, nil
	case "-r":
		return testHasPermission(inv, info, 0o4), nil
	case "-s":
		return info.Size() > 0, nil
	case "-S":
		return info.Mode()&stdfs.ModeSocket != 0, nil
	case "-u":
		return info.Mode()&0o4000 != 0, nil
	case "-w":
		return testHasPermission(inv, info, 0o2), nil
	case "-x":
		return testHasPermission(inv, info, 0o1), nil
	default:
		return false, testParseUnknownOperator(op)
	}
}

func testSameFile(a, b stdfs.FileInfo) bool {
	devA, inoA, okA := testDeviceAndInode(a)
	devB, inoB, okB := testDeviceAndInode(b)
	if okA && okB {
		return devA == devB && inoA == inoB
	}
	return false
}

func testDeviceAndInode(info stdfs.FileInfo) (dev, ino uint64, ok bool) {
	sys := reflect.ValueOf(info.Sys()) //nolint:nilaway // caller guarantees non-nil info
	if !sys.IsValid() {
		return 0, 0, false
	}
	if sys.Kind() == reflect.Pointer {
		if sys.IsNil() {
			return 0, 0, false
		}
		sys = sys.Elem()
	}
	if sys.Kind() != reflect.Struct {
		return 0, 0, false
	}
	devField := sys.FieldByName("Dev")
	inoField := sys.FieldByName("Ino")
	if !devField.IsValid() || !inoField.IsValid() {
		return 0, 0, false
	}
	return testUintField(devField), testUintField(inoField), true
}

func testCurrentUserOwns(inv *Invocation, info stdfs.FileInfo) bool {
	uid, _, ok := testOwnerIDs(info)
	if !ok {
		return true
	}
	return uid == testCurrentID(inv, "EUID")
}

func testCurrentGroupOwns(inv *Invocation, info stdfs.FileInfo) bool {
	_, gid, ok := testOwnerIDs(info)
	if !ok {
		return true
	}
	return gid == testCurrentID(inv, "EGID")
}

func testHasPermission(inv *Invocation, info stdfs.FileInfo, mask stdfs.FileMode) bool {
	mode := info.Mode().Perm() //nolint:nilaway // caller guarantees non-nil info
	currentUID := testCurrentID(inv, "EUID")
	currentGID := testCurrentID(inv, "EGID")
	ownerUID, ownerGID, ok := testOwnerIDs(info)
	if !ok {
		ownerUID = currentUID
		ownerGID = currentGID
	}
	switch {
	case currentUID == ownerUID:
		return mode&(mask<<6) != 0
	case currentGID == ownerGID:
		return mode&(mask<<3) != 0
	default:
		return mode&mask != 0
	}
}

func testOwnerIDs(info stdfs.FileInfo) (uid, gid int, ok bool) {
	if ownership, ok := gbfs.OwnershipFromFileInfo(info); ok {
		return int(ownership.UID), int(ownership.GID), true
	}
	sys := reflect.ValueOf(info.Sys()) //nolint:nilaway // caller guarantees non-nil info
	if !sys.IsValid() {
		return 0, 0, false
	}
	if sys.Kind() == reflect.Pointer {
		if sys.IsNil() {
			return 0, 0, false
		}
		sys = sys.Elem()
	}
	if sys.Kind() != reflect.Struct {
		return 0, 0, false
	}
	uidField := sys.FieldByName("Uid")
	gidField := sys.FieldByName("Gid")
	if !uidField.IsValid() || !gidField.IsValid() {
		return 0, 0, false
	}
	return int(testUintField(uidField)), int(testUintField(gidField)), true
}

func testModifiedAfterAccess(info stdfs.FileInfo) bool {
	atime, ok := testAccessTime(info)
	if !ok {
		return false
	}
	return atime.Before(info.ModTime()) //nolint:nilaway // caller guarantees non-nil info
}

func testAccessTime(info stdfs.FileInfo) (time.Time, bool) {
	sys := reflect.ValueOf(info.Sys()) //nolint:nilaway // caller guarantees non-nil info
	if !sys.IsValid() {
		return time.Time{}, false
	}
	if sys.Kind() == reflect.Pointer {
		if sys.IsNil() {
			return time.Time{}, false
		}
		sys = sys.Elem()
	}
	if sys.Kind() != reflect.Struct {
		return time.Time{}, false
	}
	if field := sys.FieldByName("Atim"); field.IsValid() {
		return testTimespec(field)
	}
	if field := sys.FieldByName("Atimespec"); field.IsValid() {
		return testTimespec(field)
	}
	if sec := sys.FieldByName("Atime"); sec.IsValid() {
		nsec := sys.FieldByName("AtimeNsec")
		return time.Unix(int64(testUintField(sec)), int64(testUintField(nsec))), true
	}
	return time.Time{}, false
}

func testTimespec(value reflect.Value) (time.Time, bool) {
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return time.Time{}, false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return time.Time{}, false
	}
	sec := value.FieldByName("Sec")
	nsec := value.FieldByName("Nsec")
	if !sec.IsValid() || !nsec.IsValid() {
		return time.Time{}, false
	}
	return time.Unix(int64(testUintField(sec)), int64(testUintField(nsec))), true
}

func testUintField(value reflect.Value) uint64 {
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return uint64(value.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return value.Uint()
	default:
		return 0
	}
}

func testCurrentID(inv *Invocation, envKey string) int {
	env := map[string]string(nil)
	if inv != nil {
		env = inv.Env
	}

	switch envKey {
	case "EUID":
		return int(idUintEnv(env, "EUID", idUintEnv(env, "UID", idDefaultUID)))
	case "EGID":
		return int(idUintEnv(env, "EGID", idUintEnv(env, "GID", idDefaultGID)))
	case "UID":
		return int(idUintEnv(env, "UID", idDefaultUID))
	case "GID":
		return int(idUintEnv(env, "GID", idDefaultGID))
	default:
		if env == nil {
			return 0
		}
		if raw := strings.TrimSpace(env[envKey]); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil {
				return parsed
			}
		}
		return 0
	}
}

func testIsTTY(inv *Invocation, fd int) bool {
	switch fd {
	case 0:
		return testTerminalWriter(inv.Stdin)
	case 1:
		return testTerminalWriter(inv.Stdout)
	case 2:
		return testTerminalWriter(inv.Stderr)
	default:
		return false
	}
}

func testTerminalWriter(v any) bool {
	file, ok := v.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}

type testParseError struct {
	message string
}

func (e testParseError) Error() string {
	return e.message
}

func testParseExpectedValue() error {
	return testParseError{message: "argument expected"}
}

func testParseExpected(value string) error {
	return testParseError{message: fmt.Sprintf("expected %s", quoteGNUOperand(value))}
}

func testParseExtraArgument(argument string) error {
	return testParseError{message: fmt.Sprintf("extra argument %s", quoteGNUOperand(argument))}
}

func testParseMissingArgument(argument string) error {
	return testParseError{message: fmt.Sprintf("missing argument after %s", argument)}
}

func testParseMissingExpression(argument string) error {
	return testParseError{message: fmt.Sprintf("%s must be followed by an expression", quoteGNUOperand(argument))}
}

func testParseUnknownOperator(operator string) error {
	return testParseError{message: fmt.Sprintf("unknown operator %s", quoteGNUOperand(operator))}
}

func testParseInvalidInteger(value string) error {
	return testParseError{message: fmt.Sprintf("invalid integer %s", quoteGNUOperand(value))}
}

func testParseBinaryOperatorExpected(operator string) error {
	return testParseError{message: fmt.Sprintf("%s: binary operator expected", operator)}
}

func testParseUnaryOperatorExpected(operator string) error {
	return testParseError{message: fmt.Sprintf("%s: unary operator expected", operator)}
}

const testBracketHelpText = `Usage: test EXPRESSION
  or:  [ EXPRESSION ]
Evaluate expressions.
`

const testBracketVersionText = "[ (gbash) dev\n"

var _ Command = (*Test)(nil)
