package expand_test

import (
	"strings"
	"testing"

	"github.com/ewhauser/gbash/shell/expand"
	"github.com/ewhauser/gbash/shell/syntax"
)

func TestPublicExpandLiteralWithPublicSyntaxAST(t *testing.T) {
	t.Parallel()

	file, err := syntax.NewParser().Parse(strings.NewReader("echo \"$HOME\"/$NAME\n"), "expand.sh")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	call, ok := file.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok {
		t.Fatalf("Cmd = %T, want *syntax.CallExpr", file.Stmts[0].Cmd)
	}
	if got, want := len(call.Args), 2; got != want {
		t.Fatalf("len(Args) = %d, want %d", got, want)
	}

	cfg := expand.Config{
		Env: expand.ListEnviron("HOME=/sandbox/home", "NAME=gbash"),
	}
	value, err := expand.Literal(&cfg, call.Args[1])
	if err != nil {
		t.Fatalf("Literal() error = %v", err)
	}
	if got, want := value, "/sandbox/home/gbash"; got != want {
		t.Fatalf("Literal() = %q, want %q", got, want)
	}
}
