package interp

import (
	"testing"

	"github.com/ewhauser/gbash/shell/syntax"
)

func TestChunkParseIncompleteUsesParseErrorMetadata(t *testing.T) {
	t.Parallel()

	err := syntax.ParseError{
		Text:      "ignore legacy text",
		Kind:      syntax.ParseErrorKindUnclosed,
		Construct: syntax.ParseErrorSymbolHereDocument,
	}
	if !chunkParseIncomplete(err, nil) {
		t.Fatal("chunkParseIncomplete() = false, want true")
	}
}

func TestNestedBackquoteParseErrorMessageUsesParseErrorMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  syntax.ParseError
		want string
	}{
		{
			name: "double quote",
			err: syntax.ParseError{
				Text:       "ignore legacy text",
				Kind:       syntax.ParseErrorKindUnclosed,
				Unexpected: syntax.ParseErrorSymbolBackquote,
				Expected:   []syntax.ParseErrorSymbol{syntax.ParseErrorSymbolDoubleQuote},
			},
			want: "unexpected EOF while looking for matching `\"'",
		},
		{
			name: "single quote",
			err: syntax.ParseError{
				Text:       "ignore legacy text",
				Kind:       syntax.ParseErrorKindUnclosed,
				Unexpected: syntax.ParseErrorSymbolEOF,
				Expected:   []syntax.ParseErrorSymbol{syntax.ParseErrorSymbolSingleQuote},
			},
			want: "unexpected EOF while looking for matching `''",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := nestedBackquoteParseErrorMessage(tc.err); got != tc.want {
				t.Fatalf("nestedBackquoteParseErrorMessage() = %q, want %q", got, tc.want)
			}
		})
	}
}
