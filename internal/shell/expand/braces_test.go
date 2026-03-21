// Copyright (c) 2018, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package expand

import (
	"bytes"
	"testing"

	"github.com/ewhauser/gbash/internal/shell/syntax"
)

func litWord(s string) *syntax.Word {
	return &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: s}}}
}

func litWords(strs ...string) []*syntax.Word {
	l := make([]*syntax.Word, 0, len(strs))
	for _, s := range strs {
		l = append(l, litWord(s))
	}
	return l
}

var braceTests = []struct {
	src  string
	want []string
}{
	{
		src:  "a{b",
		want: []string{"a{b"},
	},
	{
		src:  "a}b",
		want: []string{"a}b"},
	},
	{
		src:  "{a,b{c,d}",
		want: []string{"{a,bc", "{a,bd"},
	},
	{
		src:  "{a{b",
		want: []string{"{a{b"},
	},
	{
		src:  "a{}",
		want: []string{"a{}"},
	},
	{
		src:  "a{b}",
		want: []string{"a{b}"},
	},
	{
		src:  "a{b,c}",
		want: []string{"ab", "ac"},
	},
	{
		src:  "a{à,世界}",
		want: []string{"aà", "a世界"},
	},
	{
		src:  "a{b,c}d{e,f}g",
		want: []string{"abdeg", "abdfg", "acdeg", "acdfg"},
	},
	{
		src:  "a{b{x,y},c}d",
		want: []string{"abxd", "abyd", "acd"},
	},
	{
		src:  "a{1,2,3,4,5}",
		want: []string{"a1", "a2", "a3", "a4", "a5"},
	},
	{
		src:  "a{1..",
		want: []string{"a{1.."},
	},
	{
		src:  "a{1..4",
		want: []string{"a{1..4"},
	},
	{
		src:  "a{1.4}",
		want: []string{"a{1.4}"},
	},
	{
		src:  "{a,b}{1..4",
		want: []string{"a{1..4", "b{1..4"},
	},
	{
		src:  "a{1..4}",
		want: []string{"a1", "a2", "a3", "a4"},
	},
	{
		src:  "a{1..2}b{4..5}c",
		want: []string{"a1b4c", "a1b5c", "a2b4c", "a2b5c"},
	},
	{
		src:  "a{1..f}",
		want: []string{"a{1..f}"},
	},
	{
		src:  "a{c..f}",
		want: []string{"ac", "ad", "ae", "af"},
	},
	{
		src:  "a{H..K}",
		want: []string{"aH", "aI", "aJ", "aK"},
	},
	{
		src:  "a{-..f}",
		want: []string{"a{-..f}"},
	},
	{
		src:  "a{3..-}",
		want: []string{"a{3..-}"},
	},
	{
		src:  "a{1..10..3}",
		want: []string{"a1", "a4", "a7", "a10"},
	},
	{
		src:  "a{1..8..-3}",
		want: []string{"a1", "a4", "a7"},
	},
	{
		src:  "a{1..4..0}",
		want: []string{"a1", "a2", "a3", "a4"},
	},
	{
		src:  "a{1..4..-1}",
		want: []string{"a1", "a2", "a3", "a4"},
	},
	{
		src:  "a{4..1}",
		want: []string{"a4", "a3", "a2", "a1"},
	},
	{
		src:  "a{8..1..3}",
		want: []string{"a8", "a5", "a2"},
	},
	{
		src:  "a{4..1..-2}",
		want: []string{"a4", "a2"},
	},
	{
		src:  "a{4..1..1}",
		want: []string{"a4", "a3", "a2", "a1"},
	},
	{
		src:  "{1..005}",
		want: []string{"001", "002", "003", "004", "005"},
	},
	{
		src:  "{09..12}",
		want: []string{"09", "10", "11", "12"},
	},
	{
		src:  "{12..07}",
		want: []string{"12", "11", "10", "09", "08", "07"},
	},
	{
		src:  "{-02..4}",
		want: []string{"-02", "-01", "000", "001", "002", "003", "004"},
	},
	{
		src:  "{+02..4}",
		want: []string{"2", "3", "4"},
	},
	{
		src:  "{0001..05..2}",
		want: []string{"0001", "0003", "0005"},
	},
	{
		src:  "{0..1}",
		want: []string{"0", "1"},
	},
	{
		src:  "a{d..k..3}",
		want: []string{"ad", "ag", "aj"},
	},
	{
		src:  "a{a..e..-2}",
		want: []string{"aa", "ac", "ae"},
	},
	{
		src:  "a{d..k..n}",
		want: []string{"a{d..k..n}"},
	},
	{
		src:  "a{k..d..-2}",
		want: []string{"ak", "ai", "ag", "ae"},
	},
	{
		src:  "a{e..a..2}",
		want: []string{"ae", "ac", "aa"},
	},
	{
		src:  "{1..1}",
		want: []string{"1"},
	},
}

func TestBraces(t *testing.T) {
	t.Parallel()
	for _, tc := range braceTests {
		t.Run(tc.src, func(t *testing.T) {
			word := parseCommandWord(t, tc.src)
			inStr := printWords(word)
			wantStr := printWords(litWords(tc.want...)...)
			wantBraceExpParts(t, word, inStr != wantStr)

			got, err := Braces(word)
			if err != nil {
				t.Fatalf("Braces(%q) error = %v", tc.src, err)
			}
			gotStr := printWords(got...)
			if gotStr != wantStr {
				t.Fatalf("mismatch in %q\nwant:\n%s\ngot: %s",
					inStr, wantStr, gotStr)
			}
		})
	}
}

func TestBracesMixedCaseCharRangeErrors(t *testing.T) {
	t.Parallel()

	const wantMixedCaseErr = "bad substitution: no closing \"`\" in `-"

	tests := []struct {
		src  string
		want string
	}{
		{
			src:  `-{z..A}-`,
			want: wantMixedCaseErr,
		},
		{
			src:  `-{z..A..2}-`,
			want: wantMixedCaseErr,
		},
	}

	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			word := parseCommandWord(t, tc.src)
			got, err := Braces(word)
			if err == nil {
				t.Fatalf("Braces(%q) unexpectedly succeeded with %q", tc.src, printWords(got...))
			}
			if err.Error() != tc.want {
				t.Fatalf("Braces(%q) error = %q, want %q", tc.src, err.Error(), tc.want)
			}
		})
	}
}

func TestFieldsMixedCaseCharRangeError(t *testing.T) {
	t.Parallel()

	_, err := Fields(nil, parseCommandWord(t, `-{z..A}-`))
	if err == nil {
		t.Fatal("Fields() unexpectedly succeeded")
	}
	const want = "bad substitution: no closing \"`\" in `-"
	if err.Error() != want {
		t.Fatalf("Fields() error = %q, want %q", err.Error(), want)
	}
}

func TestBracesMixedCaseCharRangeWithoutSuffixExpands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		src    string
		prefix string
		from   rune
		to     rune
		step   int
	}{
		{
			src:  `{z..A}`,
			from: 'z',
			to:   'A',
			step: 1,
		},
		{
			src:    `-{z..A}`,
			prefix: "-",
			from:   'z',
			to:     'A',
			step:   1,
		},
		{
			src:  `{z..A..2}`,
			from: 'z',
			to:   'A',
			step: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			word := parseCommandWord(t, tc.src)
			got, err := Braces(word)
			if err != nil {
				t.Fatalf("Braces(%q) error = %v", tc.src, err)
			}

			var want []string
			for r := tc.from; r >= tc.to; r -= rune(tc.step) {
				want = append(want, tc.prefix+string(r))
			}
			wantStr := printWords(litWords(want...)...)
			if gotStr := printWords(got...); gotStr != wantStr {
				t.Fatalf("Braces(%q) = %q, want %q", tc.src, gotStr, wantStr)
			}
		})
	}
}

func TestBracesInvalidSequenceStepLiteralizes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		src  string
		want []string
	}{
		{
			src:  "a{1..2..-9223372036854775808}b{c,d}",
			want: []string{"a{1..2..-9223372036854775808}bc", "a{1..2..-9223372036854775808}bd"},
		},
		{
			src:  "a{1..2..9223372036854775808}b{c,d}",
			want: []string{"a{1..2..9223372036854775808}bc", "a{1..2..9223372036854775808}bd"},
		},
	}

	for _, src := range tests {
		t.Run(src.src, func(t *testing.T) {
			word := parseCommandWord(t, src.src)
			got, err := Braces(word)
			if err != nil {
				t.Fatalf("Braces(%q) error = %v", src.src, err)
			}
			want := printWords(litWords(src.want...)...)
			if gotStr := printWords(got...); gotStr != want {
				t.Fatalf("Braces(%q) = %q, want %q", src.src, gotStr, want)
			}
		})
	}
}

func wantBraceExpParts(t *testing.T, word *syntax.Word, want bool) {
	t.Helper()
	anyBrace := false
	for _, part := range word.Parts {
		if _, anyBrace = part.(*syntax.BraceExp); anyBrace {
			break
		}
	}
	if anyBrace && !want {
		t.Fatalf("didn't want any BraceExp node, but found one")
	} else if !anyBrace && want {
		t.Fatalf("wanted a BraceExp node, but found none")
	}
}

func printWords(words ...*syntax.Word) string {
	p := syntax.NewPrinter()
	var buf bytes.Buffer
	call := &syntax.CallExpr{Args: words}
	p.Print(&buf, call)
	return buf.String()
}
