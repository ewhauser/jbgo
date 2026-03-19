// Copyright (c) 2018, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package expand

import (
	"io/fs"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/internal/shell/syntax"
)

type testEnv map[string]Variable

func (e testEnv) Get(name string) Variable { return e[name] }

func (e testEnv) Each(fn func(name string, vr Variable) bool) {
	for name, vr := range e {
		if !fn(name, vr) {
			return
		}
	}
}

type testEnvEntry struct {
	name string
	vr   Variable
}

type layeredTestEnv struct {
	values  map[string]Variable
	entries []testEnvEntry
}

func (e layeredTestEnv) Get(name string) Variable { return e.values[name] }

func (e layeredTestEnv) Each(fn func(name string, vr Variable) bool) {
	for _, entry := range e.entries {
		if !fn(entry.name, entry.vr) {
			return
		}
	}
}

func parseWord(t *testing.T, src string) *syntax.Word {
	t.Helper()
	p := syntax.NewParser()
	word, err := p.Document(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	return word
}

func parseCommandWord(t *testing.T, src string) *syntax.Word {
	t.Helper()
	p := syntax.NewParser()
	file, err := p.Parse(strings.NewReader("x "+src+"\n"), "")
	if err != nil {
		t.Fatal(err)
	}
	call, ok := file.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) != 2 {
		t.Fatalf("unexpected parse shape for %q", src)
	}
	return call.Args[1]
}

func parseCondPattern(t *testing.T, src string) *syntax.Pattern {
	t.Helper()
	p := syntax.NewParser()
	file, err := p.Parse(strings.NewReader("[[ foo == "+src+" ]]\n"), "")
	if err != nil {
		t.Fatal(err)
	}
	tc, ok := file.Stmts[0].Cmd.(*syntax.TestClause)
	if !ok {
		t.Fatalf("unexpected parse shape for %q", src)
	}
	bin, ok := tc.X.(*syntax.CondBinary)
	if !ok {
		t.Fatalf("unexpected conditional shape for %q", src)
	}
	pat, ok := bin.Y.(*syntax.CondPattern)
	if !ok {
		t.Fatalf("unexpected pattern operand for %q", src)
	}
	return pat.Pattern
}

func TestConfigNils(t *testing.T) {
	os.Setenv("EXPAND_GLOBAL", "value")
	tests := []struct {
		name string
		cfg  *Config
		src  string
		want string
	}{
		{
			"NilConfig",
			nil,
			"$EXPAND_GLOBAL",
			"",
		},
		{
			"ZeroConfig",
			&Config{},
			"$EXPAND_GLOBAL",
			"",
		},
		{
			"EnvConfig",
			&Config{Env: ListEnviron(os.Environ()...)},
			"$EXPAND_GLOBAL",
			"value",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			word := parseWord(t, tc.src)
			got, err := Literal(tc.cfg, word)
			if err != nil {
				t.Fatalf("did not want error, got %v", err)
			}
			if got != tc.want {
				t.Fatalf("wanted %q, got %q", tc.want, got)
			}
		})
	}
}

func TestFieldsIdempotency(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		{
			"{1..4}",
			[]string{"1", "2", "3", "4"},
		},
		{
			"a{1..4}",
			[]string{"a1", "a2", "a3", "a4"},
		},
	}
	for _, tc := range tests {
		word := parseWord(t, tc.src)
		for range 2 {
			got, err := Fields(nil, word)
			if err != nil {
				t.Fatalf("did not want error, got %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("wanted %q, got %q", tc.want, got)
			}
		}
	}
}

func TestPatternASTExpansionPreservesPatternOperators(t *testing.T) {
	t.Parallel()

	pat := parseCondPattern(t, `foo@(b*(c|d))"$quoted"[0-9]?`)
	got, err := Pattern(&Config{
		Env: testEnv{
			"quoted": {Set: true, Kind: String, Str: "*"},
		},
	}, pat)
	if err != nil {
		t.Fatalf("did not want error, got %v", err)
	}
	const want = `foo@(b*(c|d))\*[0-9]?`
	if got != want {
		t.Fatalf("wanted %q, got %q", want, got)
	}
}

func TestPatternASTExpansionEscapesQuotedParamExpFastPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: `double quoted star`,
			src:  `${x-"*"}`,
			want: `\*`,
		},
		{
			name: `single quoted question`,
			src:  `${x-'?'}`,
			want: `\?`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pat := parseCondPattern(t, tc.src)
			got, err := Pattern(&Config{Env: testEnv{}}, pat)
			if err != nil {
				t.Fatalf("did not want error, got %v", err)
			}
			if got != tc.want {
				t.Fatalf("wanted %q, got %q", tc.want, got)
			}
		})
	}
}

func TestPatternASTExpansionDoesNotTildeExpandExtglobArms(t *testing.T) {
	t.Parallel()

	pat := parseCondPattern(t, `@(~/src|~)`)
	got, err := Pattern(&Config{
		Env: testEnv{
			"HOME": {Set: true, Kind: String, Str: "/tmp/home"},
		},
	}, pat)
	if err != nil {
		t.Fatalf("did not want error, got %v", err)
	}
	const want = `@(~/src|~)`
	if got != want {
		t.Fatalf("wanted %q, got %q", want, got)
	}
}

func TestLiteralParameterPatternOperatorsUsePatternAST(t *testing.T) {
	t.Parallel()

	word := parseCommandWord(t, `${value##?(foo)bar}`)
	got, err := Literal(&Config{
		Env: testEnv{
			"value": {Set: true, Kind: String, Str: "foobar"},
		},
	}, word)
	if err != nil {
		t.Fatalf("did not want error, got %v", err)
	}
	if got != "" {
		t.Fatalf("wanted empty string, got %q", got)
	}
}

func Test_glob(t *testing.T) {
	cfg := &Config{
		ReadDir: func(string) ([]fs.DirEntry, error) {
			return []fs.DirEntry{
				// The filenames here are sorted, just like [io/fs.ReadDirFS].
				&mockFileInfo{name: "A"},
				&mockFileInfo{name: "AB"},
				&mockFileInfo{name: "a"},
				&mockFileInfo{name: "ab"},
			}, nil
		},
	}

	tests := []struct {
		noCaseGlob bool
		pat        string
		want       []string
	}{
		{false, "a*", []string{"a", "ab"}},
		{false, "A*", []string{"A", "AB"}},
		{false, "*b", []string{"ab"}},
		{false, "b*", nil},
		{true, "a*", []string{"A", "AB", "a", "ab"}},
		{true, "A*", []string{"A", "AB", "a", "ab"}},
		{true, "*b", []string{"AB", "ab"}},
		{true, "b*", nil},
	}
	for _, tc := range tests {
		t.Run(tc.pat, func(t *testing.T) {
			cfg.NoCaseGlob = tc.noCaseGlob
			got, err := cfg.glob("/", tc.pat)
			if err != nil {
				t.Fatalf("did not want error, got %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("wanted %q, got %q", tc.want, got)
			}
		})
	}
}

func TestFieldsQuotedAtSingleEmptyAtMatchesBash(t *testing.T) {
	cfg := &Config{
		Env: testEnv{
			"@": {
				Set:  true,
				Kind: Indexed,
				List: []string{""},
			},
		},
	}
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "DefaultUnsetOrNull",
			src:  "\"${@:-fallback}\"",
			want: []string{"fallback"},
		},
		{
			name: "AlternateUnsetOrNull",
			src:  "\"${@:+x}\"",
			want: []string{""},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			word := parseCommandWord(t, tc.src)
			got, err := Fields(cfg, word)
			if err != nil {
				t.Fatalf("did not want error, got %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("wanted %q, got %q", tc.want, got)
			}
		})
	}
}

func TestFieldsQuotedStarSingleEmptyMatchesBash(t *testing.T) {
	tests := []struct {
		name string
		env  testEnv
		src  string
		want []string
	}{
		{
			name: "ArrayAlternateUnsetOrNull",
			env: testEnv{
				"a": {Set: true, Kind: Indexed, List: []string{""}},
			},
			src:  "\"${a[*]:+x}\"",
			want: []string{""},
		},
		{
			name: "ArrayDefaultUnsetOrNull",
			env: testEnv{
				"a": {Set: true, Kind: Indexed, List: []string{""}},
			},
			src:  "\"${a[*]:-fb}\"",
			want: []string{"fb"},
		},
		{
			name: "PositionalAlternateUnsetOrNull",
			env: testEnv{
				"*": {Set: true, Kind: Indexed, List: []string{""}},
			},
			src:  "\"${*:+x}\"",
			want: []string{""},
		},
		{
			name: "PositionalDefaultUnsetOrNull",
			env: testEnv{
				"*": {Set: true, Kind: Indexed, List: []string{""}},
			},
			src:  "\"${*:-fb}\"",
			want: []string{"fb"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			word := parseCommandWord(t, tc.src)
			got, err := Fields(&Config{Env: tc.env}, word)
			if err != nil {
				t.Fatalf("did not want error, got %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("wanted %q, got %q", tc.want, got)
			}
		})
	}
}

func TestLiteralIndirectExpansionAllowsPositionalTarget(t *testing.T) {
	t.Parallel()

	word := parseCommandWord(t, "${!name}")
	got, err := Literal(&Config{
		Env: testEnv{
			"name": {Set: true, Kind: String, Str: "1"},
			"1":    {Set: true, Kind: String, Str: "one"},
		},
	}, word)
	if err != nil {
		t.Fatalf("did not want error, got %v", err)
	}
	if got != "one" {
		t.Fatalf("wanted %q, got %q", "one", got)
	}
}

func TestFieldsQuotedIndirectAllElementsTargets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  testEnv
		want []string
	}{
		{
			name: "SpecialAt",
			env: testEnv{
				"name": {Set: true, Kind: String, Str: "@"},
				"@":    {Set: true, Kind: Indexed, List: []string{"a b", "c"}},
			},
			want: []string{"a b", "c"},
		},
		{
			name: "SpecialStar",
			env: testEnv{
				"name": {Set: true, Kind: String, Str: "*"},
				"*":    {Set: true, Kind: Indexed, List: []string{"a b", "c"}},
			},
			want: []string{"a b c"},
		},
		{
			name: "ArrayAt",
			env: testEnv{
				"name": {Set: true, Kind: String, Str: "arr[@]"},
				"arr":  {Set: true, Kind: Indexed, List: []string{"a b", "c"}},
			},
			want: []string{"a b", "c"},
		},
		{
			name: "ArrayStar",
			env: testEnv{
				"name": {Set: true, Kind: String, Str: "arr[*]"},
				"arr":  {Set: true, Kind: Indexed, List: []string{"a b", "c"}},
			},
			want: []string{"a b c"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			word := parseCommandWord(t, "\"${!name}\"")
			got, err := Fields(&Config{Env: tc.env}, word)
			if err != nil {
				t.Fatalf("did not want error, got %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("wanted %q, got %q", tc.want, got)
			}
		})
	}
}

func TestFieldsQuotedIndirectNamerefTargetStaysSingleField(t *testing.T) {
	t.Parallel()

	word := parseCommandWord(t, "\"${!name}\"")
	got, err := Fields(&Config{
		Env: testEnv{
			"name": {Set: true, Kind: String, Str: "ref"},
			"ref":  {Set: true, Kind: NameRef, Str: "arr[@]"},
			"arr":  {Set: true, Kind: Indexed, List: []string{"a b", "c"}},
		},
	}, word)
	if err != nil {
		t.Fatalf("did not want error, got %v", err)
	}
	want := []string{"a b c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wanted %q, got %q", want, got)
	}
}

func TestNamesByPrefixSkipsShadowedUnsetVariables(t *testing.T) {
	cfg := &Config{
		Env: layeredTestEnv{
			values: map[string]Variable{
				"foo":    {},
				"foobar": {Set: true, Kind: String, Str: "visible"},
				"fooz":   {Set: true, Kind: String, Str: "newer"},
			},
			entries: []testEnvEntry{
				{name: "foo", vr: Variable{Set: true, Kind: String, Str: "old"}},
				{name: "foobar", vr: Variable{Set: true, Kind: String, Str: "visible"}},
				{name: "fooz", vr: Variable{Set: true, Kind: String, Str: "old"}},
				{name: "foo", vr: Variable{}},
				{name: "fooz", vr: Variable{Set: true, Kind: String, Str: "newer"}},
			},
		},
	}

	got := cfg.namesByPrefix("foo")
	want := []string{"foobar", "fooz"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("namesByPrefix(%q) = %q, want %q", "foo", got, want)
	}
}

func TestFieldsQuotedArrayOperatorWordPreservesFieldBoundaries(t *testing.T) {
	tests := []struct {
		name string
		env  testEnv
		src  string
		want []string
	}{
		{
			name: "DefaultUnset",
			env: testEnv{
				"@": {Set: true, Kind: Indexed, List: []string{"a", "b"}},
			},
			src:  "\"${arr[@]-$@}\"",
			want: []string{"a", "b"},
		},
		{
			name: "DefaultUnsetOrNull",
			env: testEnv{
				"@": {Set: true, Kind: Indexed, List: []string{"a", "b"}},
			},
			src:  "\"${arr[@]:-$@}\"",
			want: []string{"a", "b"},
		},
		{
			name: "AlternateUnset",
			env: testEnv{
				"@":   {Set: true, Kind: Indexed, List: []string{"a", "b"}},
				"arr": {Set: true, Kind: Indexed, List: []string{"x"}},
			},
			src:  "\"${arr[@]+$@}\"",
			want: []string{"a", "b"},
		},
		{
			name: "AlternateUnsetOrNull",
			env: testEnv{
				"@":   {Set: true, Kind: Indexed, List: []string{"a", "b"}},
				"arr": {Set: true, Kind: Indexed, List: []string{"x"}},
			},
			src:  "\"${arr[@]:+$@}\"",
			want: []string{"a", "b"},
		},
		{
			name: "QuotedScalarStaysSingleField",
			env: testEnv{
				"x": {Set: true, Kind: String, Str: "a b"},
			},
			src:  "\"${arr[@]-$x}\"",
			want: []string{"a b"},
		},
		{
			name: "EmptyOperatorWordYieldsEmptyField",
			env:  testEnv{},
			src:  "\"${arr[@]-}\"",
			want: []string{""},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			word := parseCommandWord(t, tc.src)
			got, err := Fields(&Config{Env: tc.env}, word)
			if err != nil {
				t.Fatalf("did not want error, got %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("wanted %q, got %q", tc.want, got)
			}
		})
	}
}

func TestLiteralDecodesDollarSingleQuotedANSICEscapes(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "NewlinesAndQuotes",
			src:  `$'line1\nline2\'\"\\'`,
			want: "line1\nline2'\"\\",
		},
		{
			name: "OctalEscapes",
			src:  `$'\1 \11 \111'`,
			want: "\x01 \t I",
		},
		{
			name: "ControlEscapes",
			src:  `$'\c0\c9-\ca\cz\cA\cZ\c-\c+\c"'`,
			want: "\x10\x19-\x01\x1a\x01\x1a\r\v\x02",
		},
		{
			name: "InvalidEscapesRemainLiteral",
			src:  `$'\uZ \u{03bc \z \x'`,
			want: `\uZ \u{03bc \z \x`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			word := parseCommandWord(t, tc.src)
			got, err := Literal(nil, word)
			if err != nil {
				t.Fatalf("did not want error, got %v", err)
			}
			if got != tc.want {
				t.Fatalf("wanted %q, got %q", tc.want, got)
			}
		})
	}
}

type mockFileInfo struct {
	name        string
	typ         fs.FileMode
	fs.DirEntry // Stub out everything but Name() & Type()
}

var _ fs.DirEntry = (*mockFileInfo)(nil)

func (fi *mockFileInfo) Name() string      { return fi.name }
func (fi *mockFileInfo) Type() fs.FileMode { return fi.typ }
