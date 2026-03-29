package expand

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/shell/syntax"
)

func strEnviron(pairs ...string) func(string) string {
	return func(name string) string {
		prefix := name + "="
		for _, pair := range pairs {
			if val, ok := strings.CutPrefix(pair, prefix); ok {
				return val
			}
		}
		return ""
	}
}

func testFuncEnviron(env func(string) string) Environ {
	if env == nil {
		return FuncEnviron(func(string) string { return "" })
	}
	return FuncEnviron(env)
}

func TestDocumentParsesLikeRemovedShellHelper(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		env  func(string) string
		want string
	}{
		{name: "Literal", in: "foo", want: "foo"},
		{name: "Parameter", in: "a-$b-c", env: strEnviron("b=b_val"), want: "a-b_val-c"},
		{name: "Replace", in: "${x//o/a}", env: strEnviron("x=foo"), want: "faa"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			word := parseWord(t, tc.in)
			got, err := Document(&Config{Env: testFuncEnviron(tc.env)}, word)
			if err != nil {
				t.Fatalf("Document() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("Document() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFieldsParsesLikeRemovedShellHelper(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		env  func(string) string
		want []string
	}{
		{name: "Quoted", in: `"many quoted"`, want: []string{"many quoted"}},
		{name: "Split", in: "unquoted $foo", env: strEnviron("foo=bar baz"), want: []string{"unquoted", "bar", "baz"}},
		{name: "QuotedExpansion", in: `quoted "$foo"`, env: strEnviron("foo=bar baz"), want: []string{"quoted", "bar baz"}},
		{name: "Tilde", in: "~", env: strEnviron("HOME=/my/home"), want: []string{"/my/home"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := syntax.NewParser()
			var words []*syntax.Word
			for word, err := range p.WordsSeq(strings.NewReader(tc.in)) {
				if err != nil {
					t.Fatalf("WordsSeq() error = %v", err)
				}
				words = append(words, word)
			}
			got, err := Fields(&Config{Env: testFuncEnviron(tc.env)}, words...)
			if err != nil {
				t.Fatalf("Fields() error = %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Fields() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestFieldsPreferStartupHomeForLeadingTilde(t *testing.T) {
	t.Parallel()

	word := parseCommandWord(t, `~/src`)
	got, err := Fields(&Config{
		StartupHome:                  "/startup",
		PreferStartupHomeForArgTilde: true,
		Env:                          testFuncEnviron(strEnviron("HOME=/live")),
	}, word)
	if err != nil {
		t.Fatalf("Fields() error = %v", err)
	}
	want := []string{"/startup/src"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Fields() = %#v, want %#v", got, want)
	}
}

func TestRedirectFieldsKeepLiveHomeForLeadingTilde(t *testing.T) {
	t.Parallel()

	word := parseCommandWord(t, `~/redirect`)
	got, err := RedirectFields(&Config{
		StartupHome: "/startup",
		Env:         testFuncEnviron(strEnviron("HOME=/live")),
	}, word)
	if err != nil {
		t.Fatalf("RedirectFields() error = %v", err)
	}
	if want := []string{"/live/redirect"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RedirectFields() = %#v, want %#v", got, want)
	}
}

func TestRemovedShellHelperUnexpectedCmdSubstStillErrors(t *testing.T) {
	t.Parallel()

	want := "unexpected command substitution"
	for _, fn := range []func() error{
		func() error {
			word := parseWord(t, "echo $(uname -a)")
			_, err := Document(&Config{Env: testFuncEnviron(nil)}, word)
			return err
		},
		func() error {
			word := parseCommandWord(t, "$(uname -a)")
			_, err := Fields(&Config{Env: testFuncEnviron(nil)}, word)
			return err
		},
	} {
		got := fmt.Sprint(fn())
		if !strings.Contains(got, want) {
			t.Fatalf("wanted error %q, got %s", want, got)
		}
	}
}
