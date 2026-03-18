package shell

import (
	"strings"
	"testing"

	"github.com/ewhauser/gbash/internal/shell/syntax"
)

func TestInteractiveParserSeqRendersCompleteInput(t *testing.T) {
	t.Parallel()

	parser := NewInteractiveParser("repl")
	var got []string
	var incomplete []bool

	for script, err := range parser.Seq(strings.NewReader("echo hi\n")) {
		if err != nil {
			t.Fatalf("Seq() error = %v", err)
		}
		got = append(got, script)
		incomplete = append(incomplete, parser.Incomplete())
	}

	if len(got) != 1 {
		t.Fatalf("Seq() yielded %d scripts, want 1", len(got))
	}
	if got[0] != "echo hi\n" {
		t.Fatalf("script = %q, want %q", got[0], "echo hi\n")
	}
	if incomplete[0] {
		t.Fatalf("Incomplete() = true, want false")
	}
}

func TestInteractiveParserSeqTracksIncompleteInput(t *testing.T) {
	t.Parallel()

	parser := NewInteractiveParser("repl")
	calls := 0

	for script, err := range parser.Seq(strings.NewReader("if true; then\n")) {
		calls++
		if calls == 1 {
			if err != nil {
				t.Fatalf("first Seq() error = %v, want nil for incomplete callback", err)
			}
			if script != "" {
				t.Fatalf("script = %q, want empty script for incomplete input", script)
			}
			if !parser.Incomplete() {
				t.Fatalf("Incomplete() = false, want true")
			}
		}
	}

	if calls < 1 {
		t.Fatalf("Seq() yielded %d callbacks, want at least 1", calls)
	}
}

func TestIsUserSyntaxError(t *testing.T) {
	t.Parallel()

	_, err := syntax.NewParser().Parse(strings.NewReader("echo 'unterminated\n"), "fuzz.sh")
	if err == nil {
		t.Fatal("Parse() error = nil, want syntax error")
	}
	if !IsUserSyntaxError(err) {
		t.Fatalf("IsUserSyntaxError(%T) = false, want true", err)
	}
	if IsUserSyntaxError(nil) {
		t.Fatal("IsUserSyntaxError(nil) = true, want false")
	}
}
