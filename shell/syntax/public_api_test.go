package syntax_test

import (
	"bytes"
	"errors"
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

func TestPublicSyntaxParseErrorMetadata(t *testing.T) {
	t.Parallel()

	_, err := syntax.NewParser().Parse(strings.NewReader("if foo\n"), "public.sh")
	if err == nil {
		t.Fatal("Parse() error = nil, want parse error")
	}
	var parseErr syntax.ParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("Parse() error = %T, want syntax.ParseError", err)
	}
	if got, want := parseErr.Kind, syntax.ParseErrorKindMissing; got != want {
		t.Fatalf("Kind = %q, want %q", got, want)
	}
	if got, want := parseErr.Construct, syntax.ParseErrorSymbol("if <cond>"); got != want {
		t.Fatalf("Construct = %q, want %q", got, want)
	}
	if got, want := parseErr.Unexpected, syntax.ParseErrorSymbolEOF; got != want {
		t.Fatalf("Unexpected = %q, want %q", got, want)
	}
	if got, want := parseErr.Expected, []syntax.ParseErrorSymbol{syntax.ParseErrorSymbolThen}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("Expected = %v, want %v", got, want)
	}
}
