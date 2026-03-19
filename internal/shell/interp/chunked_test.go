package interp

import (
	"strings"
	"testing"

	"github.com/ewhauser/gbash/internal/shell/syntax"
)

func TestLineContinuesHonorsShellContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		line string
		want bool
	}{
		{
			name: "active trailing backslash",
			line: "echo foo \\\n",
			want: true,
		},
		{
			name: "comment backslash is literal",
			line: "alias hi='echo hi' # \\\n",
			want: false,
		},
		{
			name: "even trailing backslashes do not continue",
			line: "echo foo \\\\\n",
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := lineContinues(tt.line); got != tt.want {
				t.Fatalf("lineContinues(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestRunShellReaderExecutesAliasBeforeCommentBackslashLine(t *testing.T) {
	t.Parallel()

	stdout, stderr, err := runInterpScript(t, `
shopt -s expand_aliases
alias hi='echo hi' # \
hi
`)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if got, want := stdout, "hi\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestShiftChunkPositionsDoesNotShiftSharedAliasExpansionTwice(t *testing.T) {
	t.Parallel()

	parser := syntax.NewParser(syntax.ExpandAliases(func(name string) (syntax.AliasSpec, bool) {
		switch name {
		case "hi":
			return syntax.AliasSpec{Value: "echo hello "}, true
		case "punct":
			return syntax.AliasSpec{Value: "world"}, true
		default:
			return syntax.AliasSpec{}, false
		}
	}))

	file, err := parser.Parse(strings.NewReader("hi punct\n"), "")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	call := file.Stmts[0].Cmd.(*syntax.CallExpr)
	shared := file.AliasExpansions[0]
	sharedRefs := 0
	for _, arg := range call.Args {
		for _, expansion := range arg.AliasExpansions {
			if expansion == shared {
				sharedRefs++
			}
		}
	}
	if sharedRefs < 2 {
		t.Fatalf("shared alias expansion referenced %d times, want at least 2", sharedRefs)
	}

	orig := shared.Pos
	shiftChunkPositions(file, 10, 3)

	want := syntax.NewPos(orig.Offset()+10, orig.Line()+2, orig.Col())
	if got := shared.Pos; got != want {
		t.Fatalf("shared alias position = %v, want %v", got, want)
	}
}
