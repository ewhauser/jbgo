package shell

import (
	"testing"

	"github.com/ewhauser/gbash/shell/syntax"
)

func TestInteractiveParseIncompleteUsesParseErrorMetadata(t *testing.T) {
	t.Parallel()

	err := syntax.ParseError{
		Text:      "ignore legacy text",
		Kind:      syntax.ParseErrorKindUnclosed,
		Construct: syntax.ParseErrorSymbolHereDocument,
	}
	if !interactiveParseIncomplete(err, nil) {
		t.Fatal("interactiveParseIncomplete() = false, want true")
	}
}
