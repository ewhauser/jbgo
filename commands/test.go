package commands

import (
	"context"
	"fmt"
	stdfs "io/fs"
	"math/big"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/policy"
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

func newTestSymbol(token *string) testSymbol {
	if token == nil {
		return testSymbol{kind: testSymbolNone}
	}
	switch *token {
	case "(":
		return testSymbol{kind: testSymbolLParen, token: *token}
	case "!":
		return testSymbol{kind: testSymbolBang, token: *token}
	case "-a", "-o":
		return testSymbol{kind: testSymbolBoolOp, token: *token}
	case "=", "==", "!=", "<", ">":
		return testSymbol{kind: testSymbolStringOp, token: *token}
	case "-eq", "-ge", "-gt", "-le", "-lt", "-ne":
		return testSymbol{kind: testSymbolIntOp, token: *token}
	case "-ef", "-nt", "-ot":
		return testSymbol{kind: testSymbolFileOp, token: *token}
	case "-n", "-z":
		return testSymbol{kind: testSymbolUnaryStr, token: *token}
	case "-b", "-c", "-d", "-e", "-f", "-g", "-G", "-h", "-k", "-L", "-N", "-O", "-p", "-r", "-s", "-S", "-t", "-u", "-w", "-x":
		return testSymbol{kind: testSymbolUnaryFile, token: *token}
	default:
		return testSymbol{kind: testSymbolLiteral, token: *token}
	}
}

func (s testSymbol) intoLiteral() testSymbol {
	if s.kind == testSymbolNone {
		panic("none cannot be literal")
	}
	switch s.kind {
	case testSymbolLParen:
		return testSymbol{kind: testSymbolLiteral, token: "("}
	case testSymbolBang:
		return testSymbol{kind: testSymbolLiteral, token: "!"}
	default:
		return testSymbol{kind: testSymbolLiteral, token: s.token}
	}
}

type testParser struct {
	tokens []string
	pos    int
	stack  []testSymbol
}

func parseTest(args []string) ([]testSymbol, error) {
	p := &testParser{tokens: append([]string(nil), args...)}
	if err := p.parse(); err != nil {
		return nil, err
	}
	return p.stack, nil
}

func (p *testParser) parse() error {
	if err := p.expr(); err != nil {
		return err
	}
	if p.pos < len(p.tokens) {
		return testParseExtraArgument(p.tokens[p.pos])
	}
	return nil
}

func (p *testParser) nextToken() testSymbol {
	if p.pos >= len(p.tokens) {
		return testSymbol{kind: testSymbolNone}
	}
	token := p.tokens[p.pos]
	p.pos++
	return newTestSymbol(&token)
}

func (p *testParser) peek() testSymbol {
	if p.pos >= len(p.tokens) {
		return testSymbol{kind: testSymbolNone}
	}
	token := p.tokens[p.pos]
	return newTestSymbol(&token)
}

func (p *testParser) peekN(n int) testSymbol {
	idx := p.pos + n
	if idx >= len(p.tokens) {
		return testSymbol{kind: testSymbolNone}
	}
	token := p.tokens[idx]
	return newTestSymbol(&token)
}

func (p *testParser) peekIsBoolOp() bool {
	return p.peek().kind == testSymbolBoolOp
}

func (p *testParser) expect(value string) error {
	symbol := p.nextToken()
	if symbol.kind == testSymbolLiteral && symbol.token == value {
		return nil
	}
	return testParseExpected(value)
}

func (p *testParser) expr() error {
	if !p.peekIsBoolOp() {
		if err := p.term(); err != nil {
			return err
		}
	}
	return p.maybeBoolOp()
}

func (p *testParser) term() error {
	symbol := p.nextToken()
	switch symbol.kind {
	case testSymbolLParen:
		return p.lparen()
	case testSymbolBang:
		return p.bang()
	case testSymbolUnaryStr, testSymbolUnaryFile:
		isStringCmp := p.peek().kind == testSymbolStringOp && p.peekN(1).kind != testSymbolNone
		if isStringCmp {
			return p.literal(symbol.intoLiteral())
		}
		p.uop(symbol)
		return nil
	case testSymbolNone:
		p.stack = append(p.stack, symbol)
		return nil
	default:
		return p.literal(symbol)
	}
}

func (p *testParser) lparen() error {
	peek3 := []testSymbol{p.peekN(0), p.peekN(1), p.peekN(2)}
	switch {
	case peek3[0].kind == testSymbolNone:
		return p.literal(testSymbol{kind: testSymbolLParen}.intoLiteral())
	case peek3[1].kind == testSymbolNone:
		return testParseMissingArgument(peek3[0].display())
	case (peek3[0].kind == testSymbolUnaryStr || peek3[0].kind == testSymbolUnaryFile) && peek3[2].kind == testSymbolLiteral && peek3[2].token == ")":
		symbol := p.nextToken()
		p.uop(symbol)
		return p.expect(")")
	case peek3[0].isBinaryOp() && peek3[1].kind == testSymbolLiteral && peek3[1].token == ")":
		return p.literal(testSymbol{kind: testSymbolLParen}.intoLiteral())
	case peek3[1].kind == testSymbolLiteral && peek3[1].token == ")":
		symbol := p.nextToken()
		if err := p.literal(symbol); err != nil {
			return err
		}
		return p.expect(")")
	case peek3[0].isBinaryOp() && peek3[1].isBinaryOp():
		symbol := p.nextToken()
		if err := p.literal(symbol); err != nil {
			return err
		}
		return p.expect(")")
	case peek3[0].isBinaryOp():
		return p.literal(testSymbol{kind: testSymbolLParen}.intoLiteral())
	default:
		if err := p.expr(); err != nil {
			return err
		}
		return p.expect(")")
	}
}

func (p *testParser) bang() error {
	switch p.peek().kind {
	case testSymbolStringOp, testSymbolIntOp, testSymbolFileOp, testSymbolBoolOp:
		peek2 := p.peekN(1)
		if peek2.isBinaryOp() || peek2.kind == testSymbolNone {
			op := p.nextToken().intoLiteral()
			if err := p.literal(op); err != nil {
				return err
			}
			p.stack = append(p.stack, testSymbol{kind: testSymbolBang, token: "!"})
			return nil
		}
		if err := p.literal(testSymbol{kind: testSymbolBang}.intoLiteral()); err != nil {
			return err
		}
		return p.maybeBoolOp()
	case testSymbolNone:
		p.stack = append(p.stack, testSymbol{kind: testSymbolLiteral, token: "!"})
		return nil
	default:
		peek4 := []testSymbol{p.peekN(0), p.peekN(1), p.peekN(2), p.peekN(3)}
		if peek4[0].kind == testSymbolLiteral && peek4[1].kind == testSymbolBoolOp && peek4[2].kind == testSymbolLiteral && peek4[3].kind == testSymbolNone {
			if err := p.expr(); err != nil {
				return err
			}
			p.stack = append(p.stack, testSymbol{kind: testSymbolBang, token: "!"})
			return nil
		}
		if err := p.term(); err != nil {
			return err
		}
		p.stack = append(p.stack, testSymbol{kind: testSymbolBang, token: "!"})
		return nil
	}
}

func (p *testParser) maybeBoolOp() error {
	if !p.peekIsBoolOp() {
		return nil
	}
	symbol := p.nextToken()
	if p.peek().kind == testSymbolNone {
		if err := p.literal(symbol.intoLiteral()); err != nil {
			return err
		}
		return nil
	}
	if err := p.boolOp(symbol); err != nil {
		return err
	}
	return p.maybeBoolOp()
}

func (p *testParser) boolOp(op testSymbol) error {
	if op.token == "-a" {
		if err := p.term(); err != nil {
			return err
		}
	} else {
		if err := p.expr(); err != nil {
			return err
		}
	}
	p.stack = append(p.stack, op)
	return nil
}

func (p *testParser) uop(op testSymbol) {
	symbol := p.nextToken()
	if symbol.kind == testSymbolNone {
		p.stack = append(p.stack, op.intoLiteral())
		return
	}
	p.stack = append(p.stack, symbol.intoLiteral(), op)
}

func (p *testParser) literal(token testSymbol) error {
	p.stack = append(p.stack, token.intoLiteral())
	if !p.peek().isBinaryOp() {
		return nil
	}
	op := p.nextToken()
	value := p.nextToken()
	if value.kind == testSymbolNone {
		return testParseMissingArgument(op.display())
	}
	p.stack = append(p.stack, value.intoLiteral(), op)
	return nil
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

func (s testSymbol) isBinaryOp() bool {
	return s.kind == testSymbolStringOp || s.kind == testSymbolIntOp || s.kind == testSymbolFileOp
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
	left, ok := new(big.Int).SetString(strings.TrimSpace(a), 10)
	if !ok {
		return false, testParseInvalidInteger(a)
	}
	right, ok := new(big.Int).SetString(strings.TrimSpace(b), 10)
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
	infoA, absA, existsA, err := statMaybe(ctx, inv, policy.FileActionStat, a)
	if err != nil {
		return false, err
	}
	infoB, absB, existsB, err := statMaybe(ctx, inv, policy.FileActionStat, b)
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
		fd, ok := new(big.Int).SetString(strings.TrimSpace(name), 10)
		if !ok {
			return false, testParseInvalidInteger(name)
		}
		return testIsTTY(inv, int(fd.Int64())), nil
	}
	if op == "-h" || op == "-L" {
		info, _, exists, err := lstatMaybe(ctx, inv, policy.FileActionLstat, name)
		if err != nil {
			return false, err
		}
		return exists && info.Mode()&stdfs.ModeSymlink != 0, nil
	}

	info, _, exists, err := statMaybe(ctx, inv, policy.FileActionStat, name)
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
	sys := reflect.ValueOf(info.Sys())
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
	return uid == testCurrentID(inv, "EUID", os.Geteuid)
}

func testCurrentGroupOwns(inv *Invocation, info stdfs.FileInfo) bool {
	_, gid, ok := testOwnerIDs(info)
	if !ok {
		return true
	}
	return gid == testCurrentID(inv, "EGID", os.Getegid)
}

func testHasPermission(inv *Invocation, info stdfs.FileInfo, mask stdfs.FileMode) bool {
	mode := info.Mode().Perm()
	currentUID := testCurrentID(inv, "EUID", os.Geteuid)
	currentGID := testCurrentID(inv, "EGID", os.Getegid)
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
	sys := reflect.ValueOf(info.Sys())
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
	return atime.Before(info.ModTime())
}

func testAccessTime(info stdfs.FileInfo) (time.Time, bool) {
	sys := reflect.ValueOf(info.Sys())
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

func testCurrentID(inv *Invocation, envKey string, fallback func() int) int {
	if inv != nil && inv.Env != nil {
		if raw := strings.TrimSpace(inv.Env[envKey]); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil {
				return parsed
			}
		}
	}
	return fallback()
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

func testParseUnknownOperator(operator string) error {
	return testParseError{message: fmt.Sprintf("unknown operator %s", quoteGNUOperand(operator))}
}

func testParseInvalidInteger(value string) error {
	return testParseError{message: fmt.Sprintf("invalid integer %s", quoteGNUOperand(value))}
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
