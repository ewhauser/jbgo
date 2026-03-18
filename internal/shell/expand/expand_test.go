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
