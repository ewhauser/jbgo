// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package pattern

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
)

var regexpTests = []struct {
	pat     string
	mode    Mode
	want    string
	wantErr string

	mustMatch    []string
	mustNotMatch []string
}{
	{pat: ``, want: ``},
	{pat: `foo`, want: `foo`},
	{
		pat: `foo`, mode: NoGlobCase, want: `(?si)foo`,
		mustMatch:    []string{"foo", "FOO", "Foo"},
		mustNotMatch: []string{"bar"},
	},
	{pat: `foóà中`, mode: Filenames, want: `foóà中`},
	{pat: `.`, want: `(?s)\.`},
	{pat: `foo*`, want: `(?s)foo.*`},
	{pat: `foo*`, mode: Shortest, want: `(?sU)foo.*`},
	{pat: `foo*`, mode: Shortest | Filenames, want: `(?sU)foo[^/]*`},
	{
		pat: `*foo*`, mode: EntireString, want: `(?s)^.*foo.*$`,
		mustMatch:    []string{"foo", "prefix-foo", "foo-suffix", "foo.suffix", ".foo.", "a\nbfooc\nd"},
		mustNotMatch: []string{"bar"},
	},
	{
		pat: `foo*`, mode: Filenames | EntireString, want: `(?s)^foo[^/]*$`,
		mustMatch:    []string{"foo", "foo-suffix", "foo.suffix", "foo\nsuffix"},
		mustNotMatch: []string{"prefix-foo", "foo/suffix"},
	},
	{
		pat: `foo/*`, mode: Filenames | EntireString, want: `(?s)^foo/([^/.][^/]*)?$`,
		mustMatch:    []string{"foo/", "foo/suffix"},
		mustNotMatch: []string{"foo/.suffix", "foo/bar/baz"},
	},
	{
		pat: `foo/*`, mode: Filenames | EntireString | GlobLeadingDot, want: `(?s)^foo/[^/]*$`,
		mustMatch:    []string{"foo/", "foo/suffix", "foo/.suffix"},
		mustNotMatch: []string{"foo/bar/baz"},
	},
	{pat: `*foo`, mode: Filenames, want: `(?s)([^/.][^/]*)?foo`},
	{
		pat: `*foo`, mode: Filenames | EntireString, want: `(?s)^([^/.][^/]*)?foo$`,
		mustMatch:    []string{"foo", "prefix-foo", "prefix.foo"},
		mustNotMatch: []string{"foo-suffix", "/prefix/foo", ".foo", ".prefix-foo"},
	},
	{pat: `**`, want: `(?s).*.*`},
	{
		pat: `**`, mode: Filenames | EntireString, want: `(?s)^(/|[^/.][^/]*)*$`,
		mustMatch:    []string{"/foo", "/prefix/foo", "/a.b.c/foo", "/a/b/c/foo", "/foo/suffix.ext", "/a\n/\nb"},
		mustNotMatch: []string{"/.prefix/foo", "/prefix/.foo"},
	},
	{
		pat: `**`, mode: Filenames | NoGlobStar | EntireString, want: `(?s)^([^/.][^/]*)?$`,
		mustMatch:    []string{"foo.bar"},
		mustNotMatch: []string{"foo/bar", ".foo"},
	},
	{
		pat: `**`, mode: Filenames | EntireString | GlobLeadingDot, want: `(?s)^.*$`,
		mustMatch: []string{"/foo", "/prefix/foo", "/a.b.c/foo", "/a/b/c/foo", "/foo/suffix.ext", "/a\n/\nb", "/.prefix/foo", "/prefix/.foo"},
	},
	{pat: `/**/foo`, want: `(?s)/.*.*/foo`},
	{
		pat: `/**/foo`, mode: Filenames | EntireString, want: `(?s)^/((/|[^/.][^/]*)*/)?foo$`,
		mustMatch:    []string{"/foo", "/prefix/foo", "/a.b.c/foo", "/a/b/c/foo"},
		mustNotMatch: []string{"/foo/suffix", "prefix/foo", "/.prefix/foo", "/prefix/.foo"},
	},
	{
		pat: `/**/foo`, mode: Filenames | EntireString | GlobLeadingDot, want: `(?s)^/(.*/)?foo$`,
		mustMatch:    []string{"/foo", "/prefix/foo", "/a.b.c/foo", "/a/b/c/foo", "/.prefix/foo"},
		mustNotMatch: []string{"/foo/suffix", "prefix/foo", "/prefix/.foo"},
	},
	{pat: `/**/foo`, mode: Filenames | NoGlobStar, want: `(?s)/([^/.][^/]*)?/foo`},
	{pat: `/**/à`, mode: Filenames, want: `(?s)/((/|[^/.][^/]*)*/)?à`},
	{
		pat: `/**foo`, mode: Filenames, want: `(?s)/([^/.][^/]*)?foo`,
		// These all match because without EntireString, we match substrings.
		mustMatch: []string{"/foo", "/prefix-foo", "/foo-suffix", "/sub/foo"},
	},
	{
		pat: `/**foo`, mode: Filenames | EntireString, want: `(?s)^/([^/.][^/]*)?foo$`,
		mustMatch:    []string{"/foo", "/prefix-foo"},
		mustNotMatch: []string{"/foo-suffix", "/sub/foo", "/.foo", "/.prefix-foo"},
	},
	{
		pat: `/foo**`, mode: Filenames | EntireString, want: `(?s)^/foo[^/]*$`,
		mustMatch:    []string{"/foo", "/foo-suffix", "/foo.suffix"},
		mustNotMatch: []string{"/prefix-foo", "/foo/sub"},
	},
	{pat: `\*`, want: `(?s)\*`},
	{pat: `\`, wantErr: `^\\ at end of pattern$`},
	{pat: `?`, want: `(?s).`},
	{
		pat: `?`, mode: EntireString, want: `(?s)^.$`,
		mustMatch:    []string{"a", "\n", " "},
		mustNotMatch: []string{"abc", ""},
	},
	{pat: `?`, mode: Filenames, want: `(?s)[^/]`},
	{pat: `?à`, want: `(?s).à`},
	{pat: `\a`, want: `(?s)a`},
	{pat: `(`, want: `(?s)\(`},
	{pat: `a|b`, want: `(?s)a\|b`},
	{pat: `x{3}`, want: `(?s)x\{3\}`},
	{pat: `{3,4}`, want: `(?s)\{3,4\}`},
	{pat: `[a]`, want: `(?s)[a]`},
	{pat: `[abc]`, want: `(?s)[abc]`},
	{pat: `[^bc]`, want: `(?s)[^bc]`},
	{pat: `[!bc]`, want: `(?s)[^bc]`},
	{pat: `[[]`, want: `(?s)[[]`},
	{pat: `[\]]`, want: `(?s)[\]]`},
	{pat: `[\]]`, mode: Filenames, want: `(?s)[\]]`},
	{pat: `[]]`, want: `(?s)[]]`},
	{pat: `[!]]`, want: `(?s)[^]]`},
	{pat: `[^]]`, want: `(?s)[^]]`},
	{pat: `[a/b]`, want: `(?s)[a/b]`},
	{
		pat: `[a/b]`, mode: EntireString | Filenames, want: `(?s)^\[a/b\]$`,
		mustMatch:    []string{"[a/b]"},
		mustNotMatch: []string{"a", "/", "b"},
	},
	{
		pat: `[]/a]`, mode: EntireString | Filenames, want: `(?s)^\[\]/a\]$`,
		mustMatch:    []string{"[]/a]"},
		mustNotMatch: []string{"]", "/", "a", "/a]", "/a"},
	},
	{pat: `[`, wantErr: `^\[ was not matched with a closing \]$`},
	{pat: `[\`, wantErr: `^\[ was not matched with a closing \]$`},
	{pat: `[^`, wantErr: `^\[ was not matched with a closing \]$`},
	{pat: `[!`, wantErr: `^\[ was not matched with a closing \]$`},
	{pat: `[!bc]`, want: `(?s)[^bc]`},
	{pat: `[]`, wantErr: `^\[ was not matched with a closing \]$`},
	{pat: `[^]`, wantErr: `^\[ was not matched with a closing \]$`},
	{pat: `[!]`, wantErr: `^\[ was not matched with a closing \]$`},
	{pat: `[ab`, wantErr: `^\[ was not matched with a closing \]$`},
	{pat: `[a-]`, want: `(?s)[a-]`},
	{pat: `[z-a]`, wantErr: `^invalid range: z-a$`},
	{pat: `[a-a]`, want: `(?s)[a-a]`},
	{pat: `[aa]`, want: `(?s)[aa]`},
	{pat: `[0-4A-Z]`, want: `(?s)[0-4A-Z]`},
	{pat: `[-a]`, want: `(?s)[-a]`},
	{pat: `[^-a]`, want: `(?s)[^-a]`},
	{pat: `[a-]`, want: `(?s)[a-]`},
	{pat: `[[:digit:]]`, want: `(?s)[[:digit:]]`},
	{pat: `[[:`, wantErr: `^charClass invalid$`},
	{pat: `[[:digit`, wantErr: `^charClass invalid$`},
	{pat: `[[:wrong:]]`, wantErr: `^charClass invalid$`},
	{pat: `[[=x=]]`, wantErr: `^charClass invalid$`},
	{pat: `[[.x.]]`, wantErr: `^charClass invalid$`},
}

func TestRegexp(t *testing.T) {
	t.Parallel()
	for i, tc := range regexpTests {
		t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
			t.Logf("input: pattern=%q mode=%#b\n", tc.pat, tc.mode)
			got, gotErr := Regexp(tc.pat, tc.mode)
			if tc.wantErr != "" {
				qt.Assert(t, qt.ErrorMatches(gotErr, tc.wantErr))
			} else {
				qt.Assert(t, qt.IsNil(gotErr))
			}
			if got != tc.want {
				t.Errorf("(%q, %#b) got %q, wanted %q", tc.pat, tc.mode, got, tc.want)
			}
			_, rxErr := syntax.Parse(got, syntax.Perl)
			if gotErr == nil && rxErr != nil {
				t.Fatalf("regexp/syntax.Parse(%q) failed with %q", got, rxErr)
			}
			rx := regexp.MustCompile(got)
			for _, s := range tc.mustMatch {
				qt.Check(t, qt.IsTrue(rx.MatchString(s)), qt.Commentf("must match: %q", s))
			}
			for _, s := range tc.mustNotMatch {
				qt.Check(t, qt.IsFalse(rx.MatchString(s)), qt.Commentf("must not match: %q", s))
			}
		})
	}
}

var metaTests = []struct {
	pat       string
	wantHas   bool
	wantQuote string
}{
	{``, false, ``},
	{`foo`, false, `foo`},
	{`.`, false, `.`},
	{`*`, true, `\*`},
	{`foo?`, true, `foo\?`},
	{`\[`, false, `\\\[`},
	{`{`, false, `{`},
}

func TestMeta(t *testing.T) {
	t.Parallel()
	for _, tc := range metaTests {
		if got := HasMeta(tc.pat, 0); got != tc.wantHas {
			t.Errorf("HasMeta(%q, 0) got %t, wanted %t",
				tc.pat, got, tc.wantHas)
		}
		if got := QuoteMeta(tc.pat, 0); got != tc.wantQuote {
			t.Errorf("QuoteMeta(%q, 0) got %q, wanted %q",
				tc.pat, got, tc.wantQuote)
		}
	}
}

func TestExtendedPatternMatcherEscapesAndCharClasses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		pat  string
		mode Mode
		hit  string
		miss string
	}{
		{
			name: "escaped brackets",
			pat:  `\[???\]`,
			mode: Filenames | EntireString,
			hit:  `[abc]`,
			miss: `?`,
		},
		{
			name: "escaped dash in class",
			pat:  `[C\-D]`,
			mode: EntireString,
			hit:  `-`,
			miss: `Z`,
		},
		{
			name: "posix class prefix",
			pat:  `[[:alnum:]]*`,
			mode: Filenames | EntireString | GlobLeadingDot,
			hit:  `20231114.log`,
			miss: `.env`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher, err := ExtendedPatternMatcher(tt.pat, tt.mode)
			if err != nil {
				t.Fatalf("ExtendedPatternMatcher(%q) error = %v", tt.pat, err)
			}
			if !matcher(tt.hit) {
				t.Fatalf("matcher(%q) = false, want true", tt.hit)
			}
			if matcher(tt.miss) {
				t.Fatalf("matcher(%q) = true, want false", tt.miss)
			}
		})
	}
}

// TestExtendedPatternMatcherPathological verifies that patterns which previously
// caused exponential blowup in groupRepeat now complete in bounded time.
func TestExtendedPatternMatcherPathological(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		pat   string
		input string
		want  bool
	}{
		{
			// +(a|aa) against a long string of 'a's was exponential
			// before groupRepeat memoization.
			name:  "repeat with overlapping alts",
			pat:   `+(a|aa)`,
			input: strings.Repeat("a", 40),
			want:  true,
		},
		{
			name:  "repeat with overlapping alts mismatch",
			pat:   `+(a|aa)`,
			input: strings.Repeat("a", 39) + "b",
			want:  false,
		},
		{
			// !(+(a|aa)) against a long string exercised both the
			// negation fresh-matcher overhead and the repeat blowup.
			name:  "negated repeat with overlapping alts",
			pat:   `!(+(a|aa))`,
			input: strings.Repeat("a", 40),
			want:  false,
		},
		{
			name:  "negated repeat non-match",
			pat:   `!(+(a|aa))`,
			input: "b",
			want:  true,
		},
		{
			name:  "star group with overlapping alts",
			pat:   `*(a|aa)`,
			input: strings.Repeat("a", 50),
			want:  true,
		},
		{
			name:  "deeply nested repeat",
			pat:   `+(+(a))`,
			input: strings.Repeat("a", 30),
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			matcher, err := ExtendedPatternMatcher(tt.pat, EntireString|ExtendedOperators)
			if err != nil {
				t.Fatalf("ExtendedPatternMatcher(%q) error = %v", tt.pat, err)
			}
			got := matcher(tt.input)
			if got != tt.want {
				t.Fatalf("matcher(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func BenchmarkExtglobRepeatOverlappingAlts(b *testing.B) {
	matcher, err := ExtendedPatternMatcher(`+(a|aa)`, EntireString|ExtendedOperators)
	if err != nil {
		b.Fatal(err)
	}
	input := strings.Repeat("a", 40)
	b.ResetTimer()
	for b.Loop() {
		matcher(input)
	}
}

func TestExtendedPatternMatcherNegatedExtglobs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		pat  string
		mode Mode
		hits []string
		miss []string
	}{
		{
			name: "adjacent negated extglob",
			pat:  `!(b)@(b|c)`,
			mode: EntireString | ExtendedOperators,
			hits: []string{`ab`, `ac`, `cb`, `cc`},
			miss: []string{`bb`, `bc`},
		},
		{
			name: "nested negated extglob",
			pat:  `a@(!(c|d))`,
			mode: EntireString | ExtendedOperators,
			hits: []string{`ab`, `az`},
			miss: []string{`ac`, `ad`},
		},
		{
			name: "negated extglob does not match leading dot at root",
			pat:  `!(foo)`,
			mode: Filenames | EntireString | ExtendedOperators,
			hits: []string{`bar`},
			miss: []string{`.hidden`, `foo`},
		},
		{
			name: "negated extglob does not match leading dot after slash",
			pat:  `dir/!(foo)`,
			mode: Filenames | EntireString | ExtendedOperators,
			hits: []string{`dir/bar`},
			miss: []string{`dir/.hidden`, `dir/foo`},
		},
		{
			name: "negated extglob preserves literal slash path prefix",
			pat:  `a/!(x)`,
			mode: Filenames | EntireString | ExtendedOperators,
			hits: []string{`a/y`},
			miss: []string{`a/x`, `b/y`},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			matcher, err := ExtendedPatternMatcher(tt.pat, tt.mode)
			if err != nil {
				t.Fatalf("ExtendedPatternMatcher(%q) error = %v", tt.pat, err)
			}
			for _, hit := range tt.hits {
				if !matcher(hit) {
					t.Fatalf("matcher(%q) = false, want true", hit)
				}
			}
			for _, miss := range tt.miss {
				if matcher(miss) {
					t.Fatalf("matcher(%q) = true, want false", miss)
				}
			}
		})
	}
}
