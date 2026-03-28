package syntax_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/shell/syntax"
	"github.com/ewhauser/gbash/shell/syntax/typedjson"
)

func TestPublicSyntaxParseAndTypedJSONRoundTrip(t *testing.T) {
	t.Parallel()

	src := "echo hi\n"
	file, err := syntax.NewParser().Parse(strings.NewReader(src), "public.sh")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got, want := len(file.Stmts), 1; got != want {
		t.Fatalf("len(Stmts) = %d, want %d", got, want)
	}

	var encoded bytes.Buffer
	if err := typedjson.Encode(&encoded, file); err != nil {
		t.Fatalf("typedjson.Encode() error = %v", err)
	}
	node, err := typedjson.Decode(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatalf("typedjson.Decode() error = %v", err)
	}
	decoded, ok := node.(*syntax.File)
	if !ok {
		t.Fatalf("Decode() returned %T, want *syntax.File", node)
	}

	var printed bytes.Buffer
	if err := syntax.NewPrinter().Print(&printed, decoded); err != nil {
		t.Fatalf("Print() error = %v", err)
	}
	if got, want := printed.String(), src; got != want {
		t.Fatalf("printed source = %q, want %q", got, want)
	}
}
