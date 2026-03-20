package expand

import (
	"reflect"
	"strings"
	"testing"
)

func literalExpand(t *testing.T, env testEnv, src string) (string, error) {
	t.Helper()
	return Literal(&Config{Env: env}, parseWord(t, src))
}

func fieldsExpand(t *testing.T, env testEnv, src string) ([]string, error) {
	t.Helper()
	return Fields(&Config{Env: env}, parseCommandWord(t, src))
}

func TestParamReplacementAnchorsAndSlashForms(t *testing.T) {
	t.Parallel()

	env := testEnv{
		"s":         {Set: true, Kind: String, Str: "xx_xx"},
		"lit":       {Set: true, Kind: String, Str: "#%#"},
		"special":   {Set: true, Kind: String, Str: "%abc#def#"},
		"x":         {Set: true, Kind: String, Str: "/_/"},
		"HOST_PATH": {Set: true, Kind: String, Str: "/foo/bar/baz"},
	}

	tests := []struct {
		src  string
		want string
	}{
		{`${s/#xx/yy}`, "yy_xx"},
		{`${s/%xx/yy}`, "xx_yy"},
		{`${lit//#/X}`, "X%X"},
		{`${lit//%/Y}`, "#Y#"},
		{`${special//#/H}`, "%abcHdefH"},
		{`${special//%/P}`, "Pabc#def#"},
		{`${x////c}`, "c_c"},
		{`${x///}`, "_"},
		{`${HOST_PATH////\\/}`, `\/foo\/bar\/baz`},
	}

	for _, tt := range tests {
		got, err := literalExpand(t, env, tt.src)
		if err != nil {
			t.Fatalf("Literal(%q) error = %v", tt.src, err)
		}
		if got != tt.want {
			t.Fatalf("Literal(%q) = %q, want %q", tt.src, got, tt.want)
		}
	}
}

func TestParamReplacementBackslashesRespectSource(t *testing.T) {
	t.Parallel()

	literalEnv := testEnv{
		"x": {Set: true, Kind: String, Str: "a"},
	}
	literalTests := []struct {
		src  string
		want string
	}{
		{`${x/a/\x}`, "x"},
		{`${x/a/\\x}`, `\x`},
		{`${x/a/\/}`, `/`},
		{`${x/a/\\/}`, `\/`},
	}
	for _, tt := range literalTests {
		got, err := literalExpand(t, literalEnv, tt.src)
		if err != nil {
			t.Fatalf("Literal(%q) error = %v", tt.src, err)
		}
		if got != tt.want {
			t.Fatalf("Literal(%q) = %q, want %q", tt.src, got, tt.want)
		}
	}

	expandedTests := []struct {
		name string
		repl string
		want string
	}{
		{name: "SingleBackslashBeforeRune", repl: `\x`, want: `\x`},
		{name: "DoubleBackslashBeforeRune", repl: `\\x`, want: `\x`},
		{name: "SingleBackslashBeforeSlash", repl: `\/`, want: `\/`},
		{name: "DoubleBackslashBeforeSlash", repl: `\\/`, want: `\/`},
	}
	for _, tt := range expandedTests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := literalExpand(t, testEnv{
				"x":    {Set: true, Kind: String, Str: "a"},
				"repl": {Set: true, Kind: String, Str: tt.repl},
			}, `${x/a/$repl}`)
			if err != nil {
				t.Fatalf("Literal(${x/a/$repl}) error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("Literal(${x/a/$repl}) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParamReplacementVectorizesAnchorsOnArrays(t *testing.T) {
	t.Parallel()

	env := testEnv{
		"array": {Set: true, Kind: Indexed, List: []string{"aa", "bb", ""}},
	}

	tests := []struct {
		src  string
		want []string
	}{
		{`${array[@]/#/prefix-}`, []string{"prefix-aa", "prefix-bb", "prefix-"}},
		{`${array[@]/%/-suffix}`, []string{"aa-suffix", "bb-suffix", "-suffix"}},
	}

	for _, tt := range tests {
		got, err := fieldsExpand(t, env, tt.src)
		if err != nil {
			t.Fatalf("Fields(%q) error = %v", tt.src, err)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("Fields(%q) = %#v, want %#v", tt.src, got, tt.want)
		}
	}
}

func TestParamPatternReplacementRespectsLocale(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  testEnv
		src  string
		want string
	}{
		{
			name: "UTF8Global",
			env: testEnv{
				"LANG": {Set: true, Kind: String, Str: "en_US.UTF-8"},
				"s":    {Set: true, Kind: String, Str: "_μ_ and _μ_"},
			},
			src:  `${s//_?_/foo}`,
			want: "foo and foo",
		},
		{
			name: "UTF8Prefix",
			env: testEnv{
				"LANG": {Set: true, Kind: String, Str: "en_US.UTF-8"},
				"s":    {Set: true, Kind: String, Str: "_μ_ and _μ_"},
			},
			src:  `${s/#_?_/foo}`,
			want: "foo and _μ_",
		},
		{
			name: "UTF8Suffix",
			env: testEnv{
				"LANG": {Set: true, Kind: String, Str: "en_US.UTF-8"},
				"s":    {Set: true, Kind: String, Str: "_μ_ and _μ_"},
			},
			src:  `${s/%_?_/foo}`,
			want: "_μ_ and foo",
		},
		{
			name: "CGlobalLeavesMultibyteUnchanged",
			env: testEnv{
				"LC_ALL": {Set: true, Kind: String, Str: "C"},
				"s":      {Set: true, Kind: String, Str: "_μ_ and _μ_"},
			},
			src:  `${s//_?_/foo}`,
			want: "_μ_ and _μ_",
		},
		{
			name: "CPrefixLeavesMultibyteUnchanged",
			env: testEnv{
				"LC_ALL": {Set: true, Kind: String, Str: "C"},
				"s":      {Set: true, Kind: String, Str: "_μ_ and _μ_"},
			},
			src:  `${s/#_?_/foo}`,
			want: "_μ_ and _μ_",
		},
		{
			name: "CSuffixLeavesMultibyteUnchanged",
			env: testEnv{
				"LC_ALL": {Set: true, Kind: String, Str: "C"},
				"s":      {Set: true, Kind: String, Str: "_μ_ and _μ_"},
			},
			src:  `${s/%_?_/foo}`,
			want: "_μ_ and _μ_",
		},
		{
			name: "CGlobalStillMatchesSingleBytes",
			env: testEnv{
				"LC_ALL": {Set: true, Kind: String, Str: "C"},
				"a":      {Set: true, Kind: String, Str: "_x_ and _y_"},
			},
			src:  `${a//_?_/foo}`,
			want: "foo and foo",
		},
		{
			name: "CPrefixStillMatchesSingleBytes",
			env: testEnv{
				"LC_ALL": {Set: true, Kind: String, Str: "C"},
				"a":      {Set: true, Kind: String, Str: "_x_ and _y_"},
			},
			src:  `${a/#_?_/foo}`,
			want: "foo and _y_",
		},
		{
			name: "CSuffixStillMatchesSingleBytes",
			env: testEnv{
				"LC_ALL": {Set: true, Kind: String, Str: "C"},
				"a":      {Set: true, Kind: String, Str: "_x_ and _y_"},
			},
			src:  `${a/%_?_/foo}`,
			want: "_x_ and foo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := literalExpand(t, tt.env, tt.src)
			if err != nil {
				t.Fatalf("Literal(%q) error = %v", tt.src, err)
			}
			if got != tt.want {
				t.Fatalf("Literal(%q) = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}

func TestParamPatternBracketEdgeCases(t *testing.T) {
	t.Parallel()

	stripEnv := testEnv{
		"left":  {Set: true, Kind: String, Str: "[foo]"},
		"right": {Set: true, Kind: String, Str: "[]foo[]"},
	}

	stripTests := []struct {
		src  string
		want string
	}{
		{`${left#[}`, "foo]"},
		{`${left#"["}`, "foo]"},
		{`${right#[]}`, "foo[]"},
		{`${right#"[]"}`, "foo[]"},
	}

	for _, tt := range stripTests {
		got, err := literalExpand(t, stripEnv, tt.src)
		if err != nil {
			t.Fatalf("Literal(%q) error = %v", tt.src, err)
		}
		if got != tt.want {
			t.Fatalf("Literal(%q) = %q, want %q", tt.src, got, tt.want)
		}
	}

	got, err := literalExpand(t, testEnv{
		"pat": {Set: true, Kind: String, Str: `[^]]`},
		"s":   {Set: true, Kind: String, Str: "ab^cd^"},
	}, `${s//$pat/z}`)
	if err != nil {
		t.Fatalf("Literal([^]]) error = %v", err)
	}
	if got != "ab^cd^" {
		t.Fatalf("Literal([^]]) = %q, want %q", got, "ab^cd^")
	}

	got, err = literalExpand(t, testEnv{
		"pat": {Set: true, Kind: String, Str: `[z-a]`},
		"s":   {Set: true, Kind: String, Str: "fooz"},
	}, `${s//$pat}`)
	if err != nil {
		t.Fatalf("Literal([z-a]) error = %v", err)
	}
	if got != "fooz" {
		t.Fatalf("Literal([z-a]) = %q, want %q", got, "fooz")
	}
}

func TestInvalidParamExpansionsFailAtExpansionTime(t *testing.T) {
	t.Parallel()

	errorTests := []struct {
		name string
		env  testEnv
		src  string
	}{
		{
			name: "LengthThenSlice",
			env:  testEnv{"v": {Set: true, Kind: String, Str: "abcde"}},
			src:  `${#v:1:3}`,
		},
		{
			name: "LengthThenDefault",
			env:  testEnv{"x": {Set: true, Kind: String, Str: "foo"}},
			src:  `${#x-default}`,
		},
		{
			name: "MissingScalarSliceOffset",
			env:  testEnv{"s": {Set: true, Kind: String, Str: "abc"}},
			src:  `${s:}`,
		},
	}

	for _, tt := range errorTests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := literalExpand(t, tt.env, tt.src)
			if err == nil {
				t.Fatalf("Literal(%q) unexpectedly succeeded", tt.src)
			}
			if !strings.Contains(err.Error(), "bad substitution") {
				t.Fatalf("Literal(%q) error = %v, want bad substitution", tt.src, err)
			}
			if strings.Contains(err.Error(), "cannot combine") {
				t.Fatalf("Literal(%q) error = %v, still looks like a parse-time error", tt.src, err)
			}
		})
	}

	_, err := fieldsExpand(t, testEnv{
		"array": {Set: true, Kind: Indexed, List: []string{"aa", "bb"}},
	}, `${array[@]:}`)
	if err == nil {
		t.Fatalf("Fields(${array[@]:}) unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "bad substitution") {
		t.Fatalf("Fields(${array[@]:}) error = %v, want bad substitution", err)
	}

	got, err := literalExpand(t, testEnv{
		"s": {Set: true, Kind: String, Str: "abc"},
	}, `${s: }`)
	if err != nil {
		t.Fatalf("Literal(${s: }) error = %v", err)
	}
	if got != "abc" {
		t.Fatalf("Literal(${s: }) = %q, want %q", got, "abc")
	}

	fields, err := fieldsExpand(t, testEnv{
		"array": {Set: true, Kind: Indexed, List: []string{"aa", "bb"}},
	}, `${array[@]: }`)
	if err != nil {
		t.Fatalf("Fields(${array[@]: }) error = %v", err)
	}
	if !reflect.DeepEqual(fields, []string{"aa", "bb"}) {
		t.Fatalf("Fields(${array[@]: }) = %#v, want %#v", fields, []string{"aa", "bb"})
	}
}
