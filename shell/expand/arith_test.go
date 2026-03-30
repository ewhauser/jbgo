// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package expand

import (
	"errors"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/shell/syntax"
)

func parseArithmExpr(t *testing.T, src string) syntax.ArithmExpr {
	t.Helper()
	p := syntax.NewParser()
	// Wrap in (( )) to parse as arithmetic command
	file, err := p.Parse(strings.NewReader("(("+src+"))\n"), "")
	if err != nil {
		t.Fatal(err)
	}
	arith := file.Stmts[0].Cmd.(*syntax.ArithmCmd)
	return arith.X
}

func parseArithmExpansion(t *testing.T, src string) *syntax.ArithmExp {
	t.Helper()
	p := syntax.NewParser()
	file, err := p.Parse(strings.NewReader("echo "+src+"\n"), "")
	if err != nil {
		t.Fatal(err)
	}
	call := file.Stmts[0].Cmd.(*syntax.CallExpr)
	part, ok := call.Args[1].Parts[0].(*syntax.ArithmExp)
	if !ok {
		t.Fatalf("word part = %T, want *syntax.ArithmExp", call.Args[1].Parts[0])
	}
	return part
}

func TestArithParseOperandExpectedTokenUsesParseErrorMetadata(t *testing.T) {
	t.Parallel()

	parseErr := syntax.ParseError{
		Text:       "ignore legacy text",
		Pos:        syntax.NewPos(1, 1, 2),
		Kind:       syntax.ParseErrorKindMissing,
		Construct:  syntax.ParseErrorSymbol("+"),
		Unexpected: syntax.ParseErrorSymbolEOF,
		Expected:   []syntax.ParseErrorSymbol{syntax.ParseErrorSymbolExpression},
	}
	if got, ok := arithParseOperandExpectedToken("1+", parseErr); !ok || got != "+" {
		t.Fatalf("arithParseOperandExpectedToken() = (%q, %v), want (%q, true)", got, ok, "+")
	}
}

func parseArithmExpansionScript(t *testing.T, script string) *syntax.ArithmExp {
	t.Helper()
	p := syntax.NewParser()
	file, err := p.Parse(strings.NewReader(script), "")
	if err != nil {
		t.Fatal(err)
	}
	call := file.Stmts[0].Cmd.(*syntax.CallExpr)
	part, ok := call.Args[1].Parts[0].(*syntax.ArithmExp)
	if !ok {
		t.Fatalf("word part = %T, want *syntax.ArithmExp", call.Args[1].Parts[0])
	}
	return part
}

func parseLetArithmExpr(t *testing.T, src string) syntax.ArithmExpr {
	t.Helper()
	p := syntax.NewParser()
	file, err := p.Parse(strings.NewReader("let "+src+"\n"), "")
	if err != nil {
		t.Fatal(err)
	}
	let := file.Stmts[0].Cmd.(*syntax.LetClause)
	if got, want := len(let.Exprs), 1; got != want {
		t.Fatalf("len(let.Exprs) = %d, want %d", got, want)
	}
	return let.Exprs[0]
}

func TestArithmSingleQuoteRejection(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantErr    bool
		errExpr    string
		errTok     string
		errMessage string
	}{
		{
			name:       "single quoted number",
			src:        "'1'",
			wantErr:    true,
			errExpr:    "'1'",
			errTok:     "'1'",
			errMessage: "'1': arithmetic syntax error: operand expected (error token is \"'1'\")",
		},
		{
			name:       "single quoted with space",
			src:        "'1 '",
			wantErr:    true,
			errExpr:    "'1 '",
			errTok:     "'1 '",
			errMessage: "'1 ': arithmetic syntax error: operand expected (error token is \"'1 '\")",
		},
		{
			name:       "ansi-c quoted",
			src:        "$'1'",
			wantErr:    true,
			errExpr:    "$'1'",
			errTok:     "$'1'",
			errMessage: "'1': arithmetic syntax error: operand expected (error token is \"'1'\")",
		},
		{
			name:       "ansi-c quoted with escape",
			src:        "$'\\n'",
			wantErr:    true,
			errExpr:    "$'\\n'",
			errTok:     "$'\\n'",
			errMessage: "'\\n': arithmetic syntax error: operand expected (error token is \"'\\\\n'\")",
		},
		{
			name:       "assignment with single quoted",
			src:        "x='1'",
			wantErr:    true,
			errExpr:    "x='1'",
			errTok:     "'1'",
			errMessage: "x='1': arithmetic syntax error: operand expected (error token is \"'1'\")",
		},
		{
			name:       "add-assign with single quoted",
			src:        "x+='2'",
			wantErr:    true,
			errExpr:    "x+='2'",
			errTok:     "'2'",
			errMessage: "x+='2': arithmetic syntax error: operand expected (error token is \"'2'\")",
		},
		{
			name:       "binary expression with single quoted rhs",
			src:        "1+'2'",
			wantErr:    true,
			errExpr:    "1+'2'",
			errTok:     "'2'",
			errMessage: "1+'2': arithmetic syntax error: operand expected (error token is \"'2'\")",
		},
		{
			name:    "plain number",
			src:     "42",
			wantErr: false,
		},
		{
			name:    "double quoted number",
			src:     `"1"`,
			wantErr: false,
		},
		{
			name:    "variable",
			src:     "x",
			wantErr: false,
		},
		{
			name:    "expression",
			src:     "1+2",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr := parseArithmExpr(t, tt.src)
			cfg := &Config{
				Env: testEnv{},
			}
			_, err := Arithm(cfg, expr)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Arithm(%q) expected error, got nil", tt.src)
					return
				}
				var syntaxErr ArithmSyntaxError
				if !errors.As(err, &syntaxErr) {
					t.Errorf("Arithm(%q) expected ArithmSyntaxError, got %T: %v", tt.src, err, err)
					return
				}
				if got := syntax.ArithmExprString(syntaxErr.Expr); got != tt.errExpr {
					t.Errorf("Arithm(%q) error expr = %q, want %q", tt.src, got, tt.errExpr)
				}
				if got := syntax.ArithmExprString(syntaxErr.Token); got != tt.errTok {
					t.Errorf("Arithm(%q) error token = %q, want %q", tt.src, got, tt.errTok)
				}
				if got := syntaxErr.Error(); got != tt.errMessage {
					t.Errorf("Arithm(%q) error message = %q, want %q", tt.src, got, tt.errMessage)
				}
			} else if err != nil {
				t.Errorf("Arithm(%q) unexpected error: %v", tt.src, err)
			}
		})
	}
}

func TestArithmLetUsesShellDequotedSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		src         string
		env         testEnv
		want        int
		wantVarName string
		wantVar     string
	}{
		{
			name:        "single quoted rhs",
			src:         "z2='y*3'",
			env:         testEnv{"y": {Set: true, Kind: String, Str: "3"}},
			want:        9,
			wantVarName: "z2",
			wantVar:     "9",
		},
		{
			name:        "grouping with spaces",
			src:         "x=( 1 )",
			want:        1,
			wantVarName: "x",
			wantVar:     "1",
		},
		{
			name:        "grouping with spaces around variables",
			src:         "y=( x + 2 )",
			env:         testEnv{"x": {Set: true, Kind: String, Str: "1"}},
			want:        3,
			wantVarName: "y",
			wantVar:     "3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr := parseLetArithmExpr(t, tt.src)
			env := tt.env
			if env == nil {
				env = testEnv{}
			}
			got, err := ArithmLet(&Config{Env: env}, expr)
			if err != nil {
				t.Fatalf("ArithmLet(%q) unexpected error: %v", tt.src, err)
			}
			if got != tt.want {
				t.Fatalf("ArithmLet(%q) = %d, want %d", tt.src, got, tt.want)
			}
			if got := env.Get(tt.wantVarName).String(); got != tt.wantVar {
				t.Fatalf("%s = %q, want %q", tt.wantVarName, got, tt.wantVar)
			}
		})
	}
}

func TestArithmRecursiveExpressionMatchesBash(t *testing.T) {
	t.Parallel()

	env := testEnv{
		"loop": {Set: true, Kind: String, Str: "i<=100&&(s+=i,i++,loop)"},
		"s":    {Set: true, Kind: String, Str: "0"},
		"i":    {Set: true, Kind: String, Str: "0"},
	}
	cfg := &Config{Env: env}

	got, err := Arithm(cfg, parseArithmExpr(t, "a=loop,s"))
	if err != nil {
		t.Fatalf("Arithm(a=loop,s) error = %v", err)
	}
	if got != 5050 {
		t.Fatalf("Arithm(a=loop,s) = %d, want %d", got, 5050)
	}
	if got := env["i"].Str; got != "101" {
		t.Fatalf("i = %q, want %q", got, "101")
	}
	if got := env["s"].Str; got != "5050" {
		t.Fatalf("s = %q, want %q", got, "5050")
	}
	if got := env["a"].Str; got != "0" {
		t.Fatalf("a = %q, want %q", got, "0")
	}
}

func TestArithmArrayElementLValues(t *testing.T) {
	t.Parallel()

	env := testEnv{
		"a": {Set: true, Kind: Indexed, List: []string{"1", "4"}, Indices: []int{1, 4}},
	}
	cfg := &Config{Env: env}

	postInc := parseArithmExpr(t, "a[2]++")
	got, err := Arithm(cfg, postInc)
	if err != nil {
		t.Fatalf("Arithm(postInc) error = %v", err)
	}
	if got != 0 {
		t.Fatalf("Arithm(postInc) = %d, want 0", got)
	}
	if val, ok := env["a"].IndexedGet(2); !ok || val != "1" {
		t.Fatalf("a[2] = (%q, %v), want (\"1\", true)", val, ok)
	}

	preInc := parseArithmExpr(t, "++a[2]")
	got, err = Arithm(cfg, preInc)
	if err != nil {
		t.Fatalf("Arithm(preInc) error = %v", err)
	}
	if got != 2 {
		t.Fatalf("Arithm(preInc) = %d, want 2", got)
	}
	if val, ok := env["a"].IndexedGet(2); !ok || val != "2" {
		t.Fatalf("a[2] after pre-inc = (%q, %v), want (\"2\", true)", val, ok)
	}

	assign := parseArithmExpr(t, "a[-1]=100")
	got, err = Arithm(cfg, assign)
	if err != nil {
		t.Fatalf("Arithm(assign) error = %v", err)
	}
	if got != 100 {
		t.Fatalf("Arithm(assign) = %d, want 100", got)
	}
	if val, ok := env["a"].IndexedGet(4); !ok || val != "100" {
		t.Fatalf("a[4] after assign = (%q, %v), want (\"100\", true)", val, ok)
	}
}

func TestArithmWholeAssociativeWritesUseZeroKey(t *testing.T) {
	t.Parallel()

	env := testEnv{
		"d": {
			Set:  true,
			Kind: Associative,
			Map: map[string]string{
				"0":   "1",
				"foo": "hello",
				"bar": "world",
			},
		},
	}
	cfg := &Config{Env: env}

	postInc := parseArithmExpr(t, "d++")
	got, err := Arithm(cfg, postInc)
	if err != nil {
		t.Fatalf("Arithm(postInc) error = %v", err)
	}
	if got != 1 {
		t.Fatalf("Arithm(postInc) = %d, want 1", got)
	}

	preInc := parseArithmExpr(t, "++d")
	got, err = Arithm(cfg, preInc)
	if err != nil {
		t.Fatalf("Arithm(preInc) error = %v", err)
	}
	if got != 3 {
		t.Fatalf("Arithm(preInc) = %d, want 3", got)
	}

	assign := parseArithmExpr(t, "d+=4")
	got, err = Arithm(cfg, assign)
	if err != nil {
		t.Fatalf("Arithm(assign) error = %v", err)
	}
	if got != 7 {
		t.Fatalf("Arithm(assign) = %d, want 7", got)
	}

	gotMap := env["d"].Map
	if gotMap["0"] != "7" {
		t.Fatalf("d[0] = %q, want 7", gotMap["0"])
	}
	if gotMap["foo"] != "hello" || gotMap["bar"] != "world" {
		t.Fatalf("assoc side keys changed: %#v", gotMap)
	}
}

func TestArithmNounsetPlainAssignmentInitializesUnsetScalar(t *testing.T) {
	t.Parallel()

	env := testEnv{}
	cfg := &Config{Env: env, NoUnset: true}

	got, err := Arithm(cfg, parseArithmExpr(t, "i=2"))
	if err != nil {
		t.Fatalf("Arithm(i=2) error = %v", err)
	}
	if got != 2 {
		t.Fatalf("Arithm(i=2) = %d, want 2", got)
	}
	if got := env["i"].Str; got != "2" {
		t.Fatalf("i = %q, want %q", got, "2")
	}
}

func TestArithmNounsetPlainAssignmentInitializesUnsetIndexedElement(t *testing.T) {
	t.Parallel()

	env := testEnv{}
	cfg := &Config{Env: env, NoUnset: true}

	got, err := Arithm(cfg, parseArithmExpr(t, "a[3]=2"))
	if err != nil {
		t.Fatalf("Arithm(a[3]=2) error = %v", err)
	}
	if got != 2 {
		t.Fatalf("Arithm(a[3]=2) = %d, want 2", got)
	}
	if val, ok := env["a"].IndexedGet(3); !ok || val != "2" {
		t.Fatalf("a[3] = (%q, %v), want (\"2\", true)", val, ok)
	}
}

func TestArithmNounsetDeclaredUnsetUsesArithmeticZero(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		src       string
		env       testEnv
		indexed   bool
		want      int
		wantStr   string
		wantIndex int
	}{
		{
			name:    "scalar post increment",
			src:     "i++",
			env:     testEnv{"i": {Kind: String, UnsetArithmZero: true}},
			want:    0,
			wantStr: "1",
		},
		{
			name:    "scalar add assign",
			src:     "i+=2",
			env:     testEnv{"i": {Kind: String, UnsetArithmZero: true}},
			want:    2,
			wantStr: "2",
		},
		{
			name:      "indexed element post increment",
			src:       "a[0]++",
			env:       testEnv{"a": {Kind: Indexed, UnsetArithmZero: true}},
			indexed:   true,
			want:      0,
			wantStr:   "1",
			wantIndex: 0,
		},
		{
			name:    "plain arithmetic read",
			src:     "i<2",
			env:     testEnv{"i": {Kind: String, UnsetArithmZero: true}},
			want:    1,
			wantStr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Env: tt.env, NoUnset: true}

			got, err := Arithm(cfg, parseArithmExpr(t, tt.src))
			if err != nil {
				t.Fatalf("Arithm(%q) error = %v", tt.src, err)
			}
			if got != tt.want {
				t.Fatalf("Arithm(%q) = %d, want %d", tt.src, got, tt.want)
			}
			if tt.indexed {
				if val, ok := tt.env["a"].IndexedGet(tt.wantIndex); !ok || val != tt.wantStr {
					t.Fatalf("a[%d] = (%q, %v), want (%q, true)", tt.wantIndex, val, ok, tt.wantStr)
				}
				return
			}
			if got := tt.env["i"].Str; got != tt.wantStr {
				t.Fatalf("i = %q, want %q", got, tt.wantStr)
			}
		})
	}
}

func TestArithmNounsetReadModifyWriteStillFails(t *testing.T) {
	t.Parallel()

	tests := []string{"i++", "++i", "i+=2"}
	for _, src := range tests {
		t.Run(src, func(t *testing.T) {
			cfg := &Config{Env: testEnv{}, NoUnset: true}
			_, err := Arithm(cfg, parseArithmExpr(t, src))
			if err == nil {
				t.Fatalf("Arithm(%q) error = nil, want unbound variable", src)
			}
			var unboundErr UnboundVariableError
			if !errors.As(err, &unboundErr) {
				t.Fatalf("Arithm(%q) error = %T, want UnboundVariableError", src, err)
			}
			if got, want := unboundErr.Name, "i"; got != want {
				t.Fatalf("Arithm(%q) unbound variable = %q, want %q", src, got, want)
			}
		})
	}
}

func TestArithmWithSourcePreservesDivisionByZeroSpacing(t *testing.T) {
	t.Parallel()

	exp := parseArithmExpansion(t, "$(( 1 / 0 ))")
	cfg := &Config{Env: testEnv{}}

	got, err := ArithmWithSource(cfg, exp.X, exp.Source, exp.Left.Offset()+3, exp.Right.Offset())
	if err == nil {
		t.Fatal("ArithmWithSource() error = nil, want division-by-zero error")
	}
	if got != 0 {
		t.Fatalf("ArithmWithSource() = %d, want 0", got)
	}
	const want = `1 / 0 : division by 0 (error token is "0 ")`
	if err.Error() != want {
		t.Fatalf("ArithmWithSource() error = %q, want %q", err.Error(), want)
	}
}

func TestArithmWithSourcePreservesExpandedOperands(t *testing.T) {
	t.Parallel()

	exp := parseArithmExpansion(t, "$(( $x / $y ))")
	cfg := &Config{Env: testEnv{
		"x": {Set: true, Kind: String, Str: "1"},
		"y": {Set: true, Kind: String, Str: "0"},
	}}

	got, err := ArithmWithSource(cfg, exp.X, exp.Source, exp.Left.Offset()+3, exp.Right.Offset())
	if err == nil {
		t.Fatal("ArithmWithSource() error = nil, want division-by-zero error")
	}
	if got != 0 {
		t.Fatalf("ArithmWithSource() = %d, want 0", got)
	}
	const want = `1 / 0 : division by 0 (error token is "0 ")`
	if err.Error() != want {
		t.Fatalf("ArithmWithSource() error = %q, want %q", err.Error(), want)
	}
}
func TestArithmWithSourcePreservesInvalidConstantExpression(t *testing.T) {
	t.Parallel()

	exp := parseArithmExpansion(t, "$((a + 42x))")
	cfg := &Config{Env: testEnv{}}

	got, err := ArithmWithSource(cfg, exp.X, exp.Source, exp.Left.Offset()+3, exp.Right.Offset())
	if err == nil {
		t.Fatal("ArithmWithSource() error = nil, want invalid-constant error")
	}
	if got != 0 {
		t.Fatalf("ArithmWithSource() = %d, want 0", got)
	}
	const want = `a + 42x: value too great for base (error token is "42x")`
	if err.Error() != want {
		t.Fatalf("ArithmWithSource() error = %q, want %q", err.Error(), want)
	}
}

func TestArithmWithSourcePreservesInvalidConstantExpressionWithoutTrailingNewline(t *testing.T) {
	t.Parallel()

	exp := parseArithmExpansionScript(t, "echo $((a + 42x))")
	cfg := &Config{Env: testEnv{}}

	got, err := ArithmWithSource(cfg, exp.X, exp.Source, exp.Left.Offset()+3, exp.Right.Offset())
	if err == nil {
		t.Fatal("ArithmWithSource() error = nil, want invalid-constant error")
	}
	if got != 0 {
		t.Fatalf("ArithmWithSource() = %d, want 0", got)
	}
	const want = `a + 42x: value too great for base (error token is "42x")`
	if err.Error() != want {
		t.Fatalf("ArithmWithSource() error = %q, want %q", err.Error(), want)
	}
}

func TestArithmWithSourcePreservesLeadingNewlineInMultilineDiagnostics(t *testing.T) {
	t.Parallel()

	exp := parseArithmExpansionScript(t, "echo $((\n1 + 2  # not a comment\n))\n")
	cfg := &Config{Env: testEnv{}}

	got, err := ArithmWithSource(cfg, exp.X, exp.Source, exp.Left.Offset()+3, exp.Right.Offset())
	if err == nil {
		t.Fatal("ArithmWithSource() error = nil, want multiline diagnostic error")
	}
	if got != 0 {
		t.Fatalf("ArithmWithSource() = %d, want 0", got)
	}
	const want = "\n1 + 2  # not a comment\n: arithmetic syntax error: invalid arithmetic operator (error token is \"# not a comment\n\")"
	if err.Error() != want {
		t.Fatalf("ArithmWithSource() error = %q, want %q", err.Error(), want)
	}
}

func TestArithmWithSourceUsesExpandedStringForIndexedStringDiagnostics(t *testing.T) {
	t.Parallel()

	exp := parseArithmExpansion(t, "$(( s[0] ))")
	cfg := &Config{Env: testEnv{
		"s": {Set: true, Kind: String, Str: "12 34"},
	}}

	got, err := ArithmWithSource(cfg, exp.X, exp.Source, exp.Left.Offset()+3, exp.Right.Offset())
	if err == nil {
		t.Fatal("ArithmWithSource() error = nil, want indexed-string diagnostic")
	}
	if got != 0 {
		t.Fatalf("ArithmWithSource() = %d, want 0", got)
	}
	const want = `12 34: arithmetic syntax error in expression (error token is "34")`
	if err.Error() != want {
		t.Fatalf("ArithmWithSource() error = %q, want %q", err.Error(), want)
	}
}

func TestArithmWithSourceUsesExpressionErrorForExpandedBareWord(t *testing.T) {
	t.Parallel()

	exp := parseArithmExpansion(t, "$((echo hello))")
	cfg := &Config{Env: testEnv{}}

	got, err := ArithmWithSource(cfg, exp.X, exp.Source, exp.Left.Offset()+3, exp.Right.Offset())
	if err == nil {
		t.Fatal("ArithmWithSource() error = nil, want bare-word diagnostic")
	}
	if got != 0 {
		t.Fatalf("ArithmWithSource() = %d, want 0", got)
	}
	const want = `echo hello: arithmetic syntax error in expression (error token is "hello")`
	if err.Error() != want {
		t.Fatalf("ArithmWithSource() error = %q, want %q", err.Error(), want)
	}
}

func TestArithmWithSourcePreservesParenAmbiguityRuntimeParseError(t *testing.T) {
	t.Parallel()

	exp := parseArithmExpansionScript(t, "echo $(( echo 1\necho 2\n(( x ))\n: $(( x ))\necho 3\n))\n")
	cfg := &Config{Env: testEnv{}}

	got, err := ArithmWithSource(cfg, exp.X, exp.Source, exp.Left.Offset()+3, exp.Right.Offset())
	if err == nil {
		t.Fatal("ArithmWithSource() error = nil, want multiline parse error")
	}
	if got != 0 {
		t.Fatalf("ArithmWithSource() = %d, want 0", got)
	}
	const want = "echo 1\necho 2\n(( x ))\n: 0\necho 3\n: arithmetic syntax error in expression (error token is \"1\necho 2\n(( x ))\n: 0\necho 3\n\")"
	if err.Error() != want {
		t.Fatalf("ArithmWithSource() error = %q, want %q", err.Error(), want)
	}
}

func TestArithmWithSourceTrimsLeadingWhitespaceFromCommentToken(t *testing.T) {
	t.Parallel()

	exp := parseArithmExpansionScript(t, "echo $((\n1 + 2  # not a comment\n))\n")
	cfg := &Config{Env: testEnv{}}

	got, err := ArithmWithSource(cfg, exp.X, exp.Source, exp.Left.Offset()+3, exp.Right.Offset())
	if err == nil {
		t.Fatal("ArithmWithSource() error = nil, want multiline diagnostic error")
	}
	if got != 0 {
		t.Fatalf("ArithmWithSource() = %d, want 0", got)
	}
	const want = "\n1 + 2  # not a comment\n: arithmetic syntax error: invalid arithmetic operator (error token is \"# not a comment\n\")"
	if err.Error() != want {
		t.Fatalf("ArithmWithSource() error = %q, want %q", err.Error(), want)
	}
}

func TestWithArithmSourceEnrichesExistingDivisionByZeroErrors(t *testing.T) {
	t.Parallel()

	exp := parseArithmExpansion(t, "$(( 1 / 0 ))")
	cfg := &Config{Env: testEnv{}}

	_, err := Arithm(cfg, exp.X)
	if err == nil {
		t.Fatal("Arithm() error = nil, want division-by-zero error")
	}
	err = WithArithmSource(err, exp.Source, exp.Left.Offset()+3, exp.Right.Offset())

	const want = `1 / 0 : division by 0 (error token is "0 ")`
	if got := err.Error(); got != want {
		t.Fatalf("WithArithmSource() error = %q, want %q", got, want)
	}
}

func TestArithmWithSourceRejectsCarriageReturnStringToInteger(t *testing.T) {
	t.Parallel()

	exp := parseArithmExpansion(t, "$(( $x + 1 ))")
	cfg := &Config{Env: testEnv{
		"x": {Set: true, Kind: String, Str: "\r42\r"},
	}}

	got, err := ArithmWithSource(cfg, exp.X, exp.Source, exp.Left.Offset()+3, exp.Right.Offset())
	if err == nil {
		t.Fatal("ArithmWithSource() error = nil, want parse error")
	}
	if got != 0 {
		t.Fatalf("ArithmWithSource() = %d, want 0", got)
	}
	const want = "\r42\r + 1 : arithmetic syntax error: operand expected (error token is \"\r42\r + 1 \")"
	if err.Error() != want {
		t.Fatalf("ArithmWithSource() error = %q, want %q", err.Error(), want)
	}
}

func TestArithmWithSourceAllowsTabStringToInteger(t *testing.T) {
	t.Parallel()

	exp := parseArithmExpansion(t, "$(( $x + 1 ))")
	cfg := &Config{Env: testEnv{
		"x": {Set: true, Kind: String, Str: "\t42\t"},
	}}

	got, err := ArithmWithSource(cfg, exp.X, exp.Source, exp.Left.Offset()+3, exp.Right.Offset())
	if err != nil {
		t.Fatalf("ArithmWithSource() error = %v", err)
	}
	if got != 43 {
		t.Fatalf("ArithmWithSource() = %d, want 43", got)
	}
}

func TestArithmWithSourceRejectsVerticalTabStringToInteger(t *testing.T) {
	t.Parallel()

	exp := parseArithmExpansion(t, "$(( $x + 1 ))")
	cfg := &Config{Env: testEnv{
		"x": {Set: true, Kind: String, Str: "\v42\v"},
	}}

	got, err := ArithmWithSource(cfg, exp.X, exp.Source, exp.Left.Offset()+3, exp.Right.Offset())
	if err == nil {
		t.Fatal("ArithmWithSource() error = nil, want parse error")
	}
	if got != 0 {
		t.Fatalf("ArithmWithSource() = %d, want 0", got)
	}
	const want = "\v42\v + 1 : arithmetic syntax error: operand expected (error token is \"\v42\v + 1 \")"
	if err.Error() != want {
		t.Fatalf("ArithmWithSource() error = %q, want %q", err.Error(), want)
	}
}

func TestArithmWithSourceRejectsFormFeedStringToInteger(t *testing.T) {
	t.Parallel()

	exp := parseArithmExpansion(t, "$(( $x + 1 ))")
	cfg := &Config{Env: testEnv{
		"x": {Set: true, Kind: String, Str: "\f42\f"},
	}}

	got, err := ArithmWithSource(cfg, exp.X, exp.Source, exp.Left.Offset()+3, exp.Right.Offset())
	if err == nil {
		t.Fatal("ArithmWithSource() error = nil, want parse error")
	}
	if got != 0 {
		t.Fatalf("ArithmWithSource() = %d, want 0", got)
	}
	const want = "\f42\f + 1 : arithmetic syntax error: operand expected (error token is \"\f42\f + 1 \")"
	if err.Error() != want {
		t.Fatalf("ArithmWithSource() error = %q, want %q", err.Error(), want)
	}
}
