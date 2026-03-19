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
