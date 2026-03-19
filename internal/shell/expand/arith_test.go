// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package expand

import (
	"errors"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/internal/shell/syntax"
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
			errMessage: "$'1': arithmetic syntax error: operand expected (error token is \"$'1'\")",
		},
		{
			name:       "ansi-c quoted with escape",
			src:        "$'\\n'",
			wantErr:    true,
			errExpr:    "$'\\n'",
			errTok:     "$'\\n'",
			errMessage: "$'\\n': arithmetic syntax error: operand expected (error token is \"$'\\\\n'\")",
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
			wantErr: false, // double quotes are allowed in arithmetic
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
			} else {
				if err != nil {
					t.Errorf("Arithm(%q) unexpected error: %v", tt.src, err)
				}
			}
		})
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
