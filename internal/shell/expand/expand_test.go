// Copyright (c) 2018, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package expand

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/internal/shell/pattern"
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

func (e testEnv) Set(name string, vr Variable) error {
	if !vr.IsSet() && !vr.Declared() {
		delete(e, name)
		return nil
	}
	e[name] = vr
	return nil
}

func (e testEnv) SetVarRef(ref *syntax.VarRef, vr Variable, appendValue bool) error {
	if ref == nil {
		return nil
	}
	if ref.Index == nil {
		e[ref.Name.Value] = vr
		return nil
	}
	prev := e[ref.Name.Value]
	switch prev.Kind {
	case Associative:
		key, err := (&Config{Env: e}).associativeSubscriptKey(ref.Index)
		if err != nil {
			return err
		}
		if prev.Map == nil {
			prev.Map = make(map[string]string)
		}
		if appendValue {
			prev.Map[key] += vr.Str
		} else {
			prev.Map[key] = vr.Str
		}
		prev.Set = true
		e[ref.Name.Value] = prev
		return nil
	default:
		index, err := Arithm(&Config{Env: e}, ref.Index.Expr)
		if err != nil {
			return err
		}
		if index < 0 {
			resolved, ok := prev.IndexedResolve(index)
			if !ok {
				return BadArraySubscriptError{Name: ref.Name.Value}
			}
			index = resolved
		}
		e[ref.Name.Value] = prev.IndexedSet(index, vr.Str, appendValue)
		return nil
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

func TestAssignmentLiteralConsumesUnquotedBackslashes(t *testing.T) {
	t.Parallel()

	word := parseCommandWord(t, `foo\ bar\*baz`)

	gotLiteral, err := Literal(nil, word)
	if err != nil {
		t.Fatalf("Literal() error = %v", err)
	}
	if gotLiteral != `foo\ bar\*baz` {
		t.Fatalf("Literal() = %q, want %q", gotLiteral, `foo\ bar\*baz`)
	}

	gotAssign, err := AssignmentLiteral(nil, word)
	if err != nil {
		t.Fatalf("AssignmentLiteral() error = %v", err)
	}
	if gotAssign != "foo bar*baz" {
		t.Fatalf("AssignmentLiteral() = %q, want %q", gotAssign, "foo bar*baz")
	}
}

func TestAssignmentLiteralExpandsColonSeparatedTildes(t *testing.T) {
	t.Parallel()

	word := parseCommandWord(t, `~:~:~/src`)
	got, err := AssignmentLiteral(&Config{
		Env: testEnv{
			"HOME": {Set: true, Kind: String, Str: "/live"},
		},
	}, word)
	if err != nil {
		t.Fatalf("AssignmentLiteral() error = %v", err)
	}
	if got != "/live:/live:/live/src" {
		t.Fatalf("AssignmentLiteral() = %q, want %q", got, "/live:/live:/live/src")
	}
}

func TestAssignmentLiteralUsesLiveHome(t *testing.T) {
	t.Parallel()

	word := parseCommandWord(t, `~:~/src`)
	got, err := AssignmentLiteral(&Config{
		StartupHome: "/startup",
		Env: testEnv{
			"HOME": {Set: true, Kind: String, Str: "/live"},
		},
	}, word)
	if err != nil {
		t.Fatalf("AssignmentLiteral() error = %v", err)
	}
	if got != "/live:/live/src" {
		t.Fatalf("AssignmentLiteral() = %q, want %q", got, "/live:/live/src")
	}
}

func TestAssignmentLiteralHonorsEscapedColonsForTildes(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Env: testEnv{
			"HOME": {Set: true, Kind: String, Str: "/live"},
		},
	}
	tests := []struct {
		word string
		want string
	}{
		{word: `\:~`, want: `:~`},
		{word: `foo\:~:~`, want: `foo:~:/live`},
		{word: `\\:~`, want: `\:/live`},
	}
	for _, test := range tests {
		word := parseCommandWord(t, test.word)
		got, err := AssignmentLiteral(cfg, word)
		if err != nil {
			t.Fatalf("AssignmentLiteral(%q) error = %v", test.word, err)
		}
		if got != test.want {
			t.Fatalf("AssignmentLiteral(%q) = %q, want %q", test.word, got, test.want)
		}
	}
}

func TestAssignmentLiteralExpandsTildesInParamDefaults(t *testing.T) {
	t.Parallel()

	word := parseCommandWord(t, `~:${undef-~:~}`)
	got, err := AssignmentLiteral(&Config{
		StartupHome: "/startup",
		Env: testEnv{
			"HOME": {Set: true, Kind: String, Str: "/home/bar"},
		},
	}, word)
	if err != nil {
		t.Fatalf("AssignmentLiteral() error = %v", err)
	}
	if got != "/home/bar:/home/bar:/home/bar" {
		t.Fatalf("AssignmentLiteral() = %q, want %q", got, "/home/bar:/home/bar:/home/bar")
	}
}

func TestFieldsExpandTildeInAssignmentLikeArgs(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		StartupHome: "/startup",
		Env: testEnv{
			"HOME": {Set: true, Kind: String, Str: "/home/bob"},
		},
	}
	tests := []struct {
		src  string
		want []string
	}{
		{src: `x=~`, want: []string{"x=/home/bob"}},
		{src: `x=~:${undef-~:~}`, want: []string{"x=/home/bob:/home/bob:/home/bob"}},
		{src: `x=${undef}~`, want: []string{"x=~"}},
	}
	for _, tc := range tests {
		word := parseCommandWord(t, tc.src)
		got, err := Fields(cfg, word)
		if err != nil {
			t.Fatalf("Fields(%q) error = %v", tc.src, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("Fields(%q) = %#v, want %#v", tc.src, got, tc.want)
		}
	}
}

func TestFieldsPreserveQuotedIndirectArrayParamWord(t *testing.T) {
	t.Parallel()

	word := parseCommandWord(t, `${!hooksSlice+"${!hooksSlice}"}`)
	got, err := Fields(&Config{
		Env: testEnv{
			"hooksSlice": {Set: true, Kind: String, Str: "preHooks[@]"},
			"preHooks": {
				Set:  true,
				Kind: Indexed,
				List: []string{"foo bar", "baz"},
			},
		},
	}, word)
	if err != nil {
		t.Fatalf("Fields() error = %v", err)
	}
	want := []string{"foo bar", "baz"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Fields() = %#v, want %#v", got, want)
	}
}

func TestRegexpExpandsLeadingTildeAsLiteralRegex(t *testing.T) {
	t.Parallel()

	word := parseCommandWord(t, `~`)
	got, err := Regexp(&Config{
		Env: testEnv{
			"HOME": {Set: true, Kind: String, Str: "^a$"},
		},
	}, word)
	if err != nil {
		t.Fatalf("Regexp() error = %v", err)
	}
	if got != `\^a\$` {
		t.Fatalf("Regexp() = %q, want %q", got, `\^a\$`)
	}
}

func TestRegexpExpandsNestedTildeAsLiteralRegex(t *testing.T) {
	t.Parallel()

	word := parseCommandWord(t, `${v:-~}`)
	got, err := Regexp(&Config{
		Env: testEnv{
			"HOME": {Set: true, Kind: String, Str: "^a$"},
		},
	}, word)
	if err != nil {
		t.Fatalf("Regexp() error = %v", err)
	}
	if got != `\^a\$` {
		t.Fatalf("Regexp() = %q, want %q", got, `\^a\$`)
	}
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
		word := parseCommandWord(t, tc.src)
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

func TestSparseElementExpansionsTreatHolesAsUnset(t *testing.T) {
	t.Parallel()

	env := testEnv{
		"sp": {Set: true, Kind: Indexed, List: []string{"one", "four"}, Indices: []int{1, 4}},
	}

	tests := []struct {
		src  string
		want string
	}{
		{`${sp[2]+set}`, ""},
		{`${sp[2]:-default}`, "default"},
		{`${sp[-3]+set}`, ""},
		{`${sp[-3]:-default}`, "default"},
	}
	for _, tt := range tests {
		word := parseWord(t, tt.src)
		got, err := Literal(&Config{Env: env}, word)
		if err != nil {
			t.Fatalf("Literal(%q) error = %v", tt.src, err)
		}
		if got != tt.want {
			t.Fatalf("Literal(%q) = %q, want %q", tt.src, got, tt.want)
		}
	}
}

func TestBadArraySubscriptCanReportWithoutAbortingExpansion(t *testing.T) {
	t.Parallel()

	env := testEnv{
		"sp": {Set: true, Kind: Indexed, List: []string{"one", "two"}, Indices: []int{1, 2}},
	}
	var reported []string
	cfg := &Config{
		Env: env,
		ReportError: func(err error) {
			reported = append(reported, err.Error())
		},
	}

	word := parseCommandWord(t, `"${sp[-10]}"`)
	got, err := Literal(cfg, word)
	if err != nil {
		t.Fatalf("Literal() error = %v", err)
	}
	if got != "" {
		t.Fatalf("Literal() = %q, want empty string", got)
	}
	if !reflect.DeepEqual(reported, []string{"sp: bad array subscript"}) {
		t.Fatalf("reported = %#v, want bad array subscript", reported)
	}

	reported = nil
	word = parseCommandWord(t, `"$((sp[-10]))"`)
	got, err = Literal(cfg, word)
	if err != nil {
		t.Fatalf("Literal() arithmetic error = %v", err)
	}
	if got != "0" {
		t.Fatalf("Literal() arithmetic = %q, want %q", got, "0")
	}
	if !reflect.DeepEqual(reported, []string{"sp: bad array subscript"}) {
		t.Fatalf("arithmetic reported = %#v, want bad array subscript", reported)
	}

	reported = nil
	word = parseCommandWord(t, `${sp[-10]}`)
	fields, err := Fields(cfg, word)
	if err != nil {
		t.Fatalf("Fields() error = %v", err)
	}
	if len(fields) != 0 {
		t.Fatalf("Fields() = %#v, want zero fields", fields)
	}
	if !reflect.DeepEqual(reported, []string{"sp: bad array subscript"}) {
		t.Fatalf("fields reported = %#v, want bad array subscript once", reported)
	}
}

func TestFieldsQuotedSparseArrayExpansionKeepsPrefixAndSuffix(t *testing.T) {
	t.Parallel()

	env := testEnv{
		"a": {Set: true, Kind: Indexed, List: []string{"v0", "v1", "v5", "v9"}, Indices: []int{0, 1, 5, 9}},
	}
	word := parseCommandWord(t, `"abc${a[@]}xyz"`)
	got, err := Fields(&Config{Env: env}, word)
	if err != nil {
		t.Fatalf("Fields() error = %v", err)
	}
	want := []string{"abcv0", "v1", "v5", "v9xyz"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Fields() = %#v, want %#v", got, want)
	}
}

func TestFieldsQuotedSparseArrayParamOpsAreElementWise(t *testing.T) {
	t.Parallel()

	env := testEnv{
		"a": {Set: true, Kind: Indexed, List: []string{"v0", "v1", "v5", "v9"}, Indices: []int{0, 1, 5, 9}},
	}
	tests := []struct {
		src  string
		want []string
	}{
		{`"${a[@]#v}"`, []string{"0", "1", "5", "9"}},
		{`"${a[@]@Q}"`, []string{"'v0'", "'v1'", "'v5'", "'v9'"}},
	}
	for _, tc := range tests {
		word := parseCommandWord(t, tc.src)
		got, err := Fields(&Config{Env: env}, word)
		if err != nil {
			t.Fatalf("Fields(%q) error = %v", tc.src, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("Fields(%q) = %#v, want %#v", tc.src, got, tc.want)
		}
	}
}

func TestFieldsQuotedAssociativeKeyExpansionStarJoinsOneField(t *testing.T) {
	t.Parallel()

	env := testEnv{
		"A": {Set: true, Kind: Associative, Map: map[string]string{"X X": "xx", "Y Y": "yy"}},
	}
	tests := []struct {
		src  string
		want []string
	}{
		{`"${!A[@]}"`, []string{"X X", "Y Y"}},
		{`"${!A[*]}"`, []string{"X X Y Y"}},
	}
	for _, tc := range tests {
		word := parseCommandWord(t, tc.src)
		got, err := Fields(&Config{Env: env}, word)
		if err != nil {
			t.Fatalf("Fields(%q) error = %v", tc.src, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("Fields(%q) = %#v, want %#v", tc.src, got, tc.want)
		}
	}
}

func TestFieldsDirectKeyExpansionUnsetAndScalar(t *testing.T) {
	t.Parallel()

	env := testEnv{
		"x": {Set: true, Kind: String, Str: ""},
	}
	tests := []struct {
		src  string
		want []string
	}{
		{`[${!u[@]}]`, []string{"[]"}},
		{`[${!u[*]}]`, []string{"[]"}},
		{`[${!x[@]}]`, []string{"[0]"}},
		{`[${!x[*]}]`, []string{"[0]"}},
		{`"${!u[@]}"`, nil},
		{`"${!u[*]}"`, []string{""}},
		{`"${!x[@]}"`, []string{"0"}},
		{`"${!x[*]}"`, []string{"0"}},
	}
	for _, tc := range tests {
		word := parseCommandWord(t, tc.src)
		got, err := Fields(&Config{Env: env}, word)
		if err != nil {
			t.Fatalf("Fields(%q) error = %v", tc.src, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("Fields(%q) = %#v, want %#v", tc.src, got, tc.want)
		}
	}
}

func TestAssociativeSubscriptStringifiesNonWordExpr(t *testing.T) {
	t.Parallel()

	env := testEnv{
		"a": {Set: true, Kind: Associative, Map: map[string]string{"a+1": "value"}},
	}
	word := parseWord(t, `${a[a+1]}`)
	got, err := Literal(&Config{Env: env}, word)
	if err != nil {
		t.Fatalf("Literal() error = %v", err)
	}
	if got != "value" {
		t.Fatalf("Literal() = %q, want %q", got, "value")
	}
}

func TestBashQuoteValueMatchesSingleQuoteEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{"spam", "'spam'"},
		{"''", "''\\'''\\'''"},
		{"a'b", "'a'\\''b'"},
		{"é μ", "'é μ'"},
		{"a\nb\x01c'd", "$'a\\nb\\001c\\'d'"},
	}
	for _, tc := range tests {
		got, err := bashQuoteValue(tc.in)
		if err != nil {
			t.Fatalf("bashQuoteValue(%q) error = %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("bashQuoteValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBashStringHelpersHonorLCCTypeBeforeLang(t *testing.T) {
	t.Parallel()

	cfg := &Config{Env: testEnv{
		"LANG":     {Set: true, Kind: String, Str: "C"},
		"LC_CTYPE": {Set: true, Kind: String, Str: "C.UTF-8"},
	}}

	if got := cfg.bashStringLen("éx"); got != 2 {
		t.Fatalf("bashStringLen() = %d, want %d", got, 2)
	}
	if got := cfg.bashStringSlice("éx", true, 1, false, 0); got != "x" {
		t.Fatalf("bashStringSlice() = %q, want %q", got, "x")
	}
}

func TestDecodePromptEscapesPreservesUnknownSequences(t *testing.T) {
	t.Parallel()

	if got := decodePromptEscapes(`\q`); got != `\q` {
		t.Fatalf("decodePromptEscapes(unknown) = %q, want %q", got, `\q`)
	}
	if got := decodePromptEscapes(`\n`); got != "\n" {
		t.Fatalf("decodePromptEscapes(newline) = %q, want newline", got)
	}
	if got := decodePromptEscapes(`\\`); got != `\` {
		t.Fatalf("decodePromptEscapes(backslash) = %q, want %q", got, `\`)
	}
	if got := decodePromptEscapes(`\$`); got != `$` {
		t.Fatalf("decodePromptEscapes(dollar) = %q, want %q", got, `$`)
	}
}

func TestSparseArrayDefaultExpansionUsesElementZeroSetness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  testEnv
		src  string
		want string
	}{
		{
			name: "empty array scalar default",
			env:  testEnv{"a": {Set: true, Kind: Indexed}},
			src:  `${a-(unset)}`,
			want: "(unset)",
		},
		{
			name: "assoc scalar element zero",
			env:  testEnv{"assoc": {Set: true, Kind: Associative, Map: map[string]string{"0": "zero"}}},
			src:  `${assoc}`,
			want: "zero",
		},
	}
	for _, tc := range tests {
		word := parseWord(t, tc.src)
		got, err := Literal(&Config{Env: tc.env}, word)
		if err != nil {
			t.Fatalf("Literal(%q) error = %v", tc.src, err)
		}
		if got != tc.want {
			t.Fatalf("Literal(%q) = %q, want %q", tc.src, got, tc.want)
		}
	}
}

func TestLiteralPreservesParsedBraceExp(t *testing.T) {
	t.Parallel()

	word := parseCommandWord(t, `{${x},$y}`)
	got, err := Literal(&Config{
		Env: testEnv{
			"x": {Set: true, Kind: String, Str: "A"},
			"y": {Set: true, Kind: String, Str: "B"},
		},
	}, word)
	if err != nil {
		t.Fatalf("did not want error, got %v", err)
	}
	if got != "{A,B}" {
		t.Fatalf("wanted %q, got %q", "{A,B}", got)
	}
}

func TestScalarAllElementsSubscriptsPreserveValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "QuotedAt",
			src:  "\"${s[@]}\"",
			want: []string{"a b"},
		},
		{
			name: "QuotedStar",
			src:  "\"${s[*]}\"",
			want: []string{"a b"},
		},
		{
			name: "UnquotedAt",
			src:  "${s[@]}",
			want: []string{"a", "b"},
		},
		{
			name: "UnquotedStar",
			src:  "${s[*]}",
			want: []string{"a", "b"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			word := parseCommandWord(t, tc.src)
			got, err := Fields(&Config{
				Env: testEnv{
					"s": {Set: true, Kind: String, Str: "a b"},
				},
			}, word)
			if err != nil {
				t.Fatalf("did not want error, got %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("wanted %q, got %q", tc.want, got)
			}
		})
	}
}

func TestFieldsPreserveQuotesWhenStringifyingBraceExpInParamArgs(t *testing.T) {
	t.Parallel()

	word := parseCommandWord(t, `${unset:-{'a b',c}}`)
	got, err := Fields(&Config{
		Env: testEnv{},
	}, word)
	if err != nil {
		t.Fatalf("did not want error, got %v", err)
	}
	want := []string{`{a b,c}`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wanted %q, got %q", want, got)
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

func TestGlobRespectsLocaleCollation(t *testing.T) {
	t.Parallel()

	readDir := func(string) ([]fs.DirEntry, error) {
		return []fs.DirEntry{
			&mockFileInfo{name: "hello"},
			&mockFileInfo{name: "hello-test.sh"},
			&mockFileInfo{name: "hello.py"},
			&mockFileInfo{name: "hello_preamble.sh"},
		}, nil
	}

	tests := []struct {
		name string
		env  testEnv
		want []string
	}{
		{
			name: "c locale keeps byte order",
			env: testEnv{
				"LC_ALL": {Set: true, Kind: String, Str: "C.UTF-8"},
			},
			want: []string{"hello", "hello-test.sh", "hello.py", "hello_preamble.sh"},
		},
		{
			name: "lc_collate uses locale order",
			env: testEnv{
				"LC_COLLATE": {Set: true, Kind: String, Str: "en_US.UTF-8"},
			},
			want: []string{"hello", "hello_preamble.sh", "hello-test.sh", "hello.py"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := (&Config{
				Env:     tt.env,
				ReadDir: readDir,
			}).glob("/", "h*")
			if err != nil {
				t.Fatalf("glob() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("glob() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestFieldsGlobEscapedBracketPattern(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{"[abc]", "?"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
	cfg := &Config{
		Env: testEnv{
			"PWD": {Set: true, Kind: String, Str: dir},
		},
		ReadDir:      os.ReadDir,
		GlobSkipDots: true,
	}
	if matches, err := cfg.glob(dir, `\[???\]`); err != nil {
		t.Fatalf("glob() error = %v", err)
	} else if !reflect.DeepEqual(matches, []string{"[abc]"}) {
		t.Fatalf("glob() = %#v, want %#v", matches, []string{"[abc]"})
	}
	got, err := Fields(cfg, parseCommandWord(t, `\[???\]`), parseCommandWord(t, `\?`))
	if err != nil {
		t.Fatalf("Fields() error = %v", err)
	}
	want := []string{"[abc]", "?"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Fields() = %#v, want %#v", got, want)
	}
}

func TestFieldsGlobEscapedDashInCharClass(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{"foo.-", "c.C"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
	cfg := &Config{
		Env: testEnv{
			"PWD": {Set: true, Kind: String, Str: dir},
		},
		ReadDir:      os.ReadDir,
		GlobSkipDots: true,
	}
	got, err := Fields(cfg, parseCommandWord(t, `*.[C\-D]`))
	if err != nil {
		t.Fatalf("Fields() error = %v", err)
	}
	want := []string{"c.C", "foo.-"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Fields() = %#v, want %#v", got, want)
	}
}

func TestFieldsGlobCharClassExpression(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{"foo.-", "e.E"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
	cfg := &Config{
		Env: testEnv{
			"PWD": {Set: true, Kind: String, Str: dir},
		},
		ReadDir:      os.ReadDir,
		GlobSkipDots: true,
	}
	got, err := Fields(cfg, parseCommandWord(t, `*.[[:punct:]E]`))
	if err != nil {
		t.Fatalf("Fields() error = %v", err)
	}
	want := []string{"e.E", "foo.-"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Fields() = %#v, want %#v", got, want)
	}
}

func TestFieldsGlobIgnoreCharClass(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{".env", "_testing.py", "20231114.log", "pyproject.toml", "has space.docx"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
	cfg := &Config{
		Env: testEnv{
			"PWD":        {Set: true, Kind: String, Str: dir},
			"GLOBIGNORE": {Set: true, Kind: String, Str: "[[:alnum:]]*"},
		},
		ReadDir:      os.ReadDir,
		GlobSkipDots: true,
	}
	got, err := Fields(cfg, parseCommandWord(t, `*.*`))
	if err != nil {
		t.Fatalf("Fields() error = %v", err)
	}
	want := []string{".env", "_testing.py"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Fields() = %#v, want %#v", got, want)
	}
}

func TestFilterGlobIgnoreCharClass(t *testing.T) {
	t.Parallel()

	direct, err := pattern.ExtendedPatternMatcher("[[:alnum:]]*", pattern.Filenames|pattern.EntireString|pattern.GlobLeadingDot)
	if err != nil {
		t.Fatalf("direct matcher error = %v", err)
	}
	if !direct("20231114.log") || direct(".env") {
		t.Fatalf("direct matcher did not respect char class prefix")
	}

	cfg := prepareConfig(&Config{
		Env: testEnv{
			"GLOBIGNORE": {Set: true, Kind: String, Str: "[[:alnum:]]*"},
		},
	})
	if len(cfg.globIgnoreMatchers) != 1 {
		t.Fatalf("len(globIgnoreMatchers) = %d, want 1", len(cfg.globIgnoreMatchers))
	}
	if !cfg.globIgnoreMatchers[0]("20231114.log") || cfg.globIgnoreMatchers[0](".env") {
		t.Fatalf("prepared matcher did not respect char class prefix")
	}
	got := cfg.filterGlobIgnore([]string{".env", "20231114.log", "_testing.py", "has space.docx", "pyproject.toml"})
	want := []string{".env", "_testing.py"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterGlobIgnore() = %#v, want %#v", got, want)
	}
}

func TestLiteralPreservesEscapedGlobChars(t *testing.T) {
	t.Parallel()

	tests := []struct {
		src  string
		want string
	}{
		{`\[???\]`, `\[???\]`},
		{`*.[C\-D]`, `*.[C\-D]`},
	}
	for _, tt := range tests {
		got, err := Literal(nil, parseCommandWord(t, tt.src))
		if err != nil {
			t.Fatalf("Literal(%q) error = %v", tt.src, err)
		}
		if got != tt.want {
			t.Fatalf("Literal(%q) = %q, want %q", tt.src, got, tt.want)
		}
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

func TestFieldsIndirectUnsetArrayNounset(t *testing.T) {
	t.Parallel()

	// Indirect ref to undefined array[@] is treated as empty (no error) even
	// under nounset, matching bash behavior.
	t.Run("AtSubscriptSilentUnderNounset", func(t *testing.T) {
		word := parseCommandWord(t, "\"${!name}\"")
		got, err := Fields(&Config{
			NoUnset: true,
			Env: testEnv{
				"name": {Set: true, Kind: String, Str: "arr[@]"},
			},
		}, word)
		if err != nil {
			t.Fatalf("did not want error for unset arr[@] under nounset, got %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("wanted [], got %q", got)
		}
	})

	// Indirect ref to undefined array[*] must still error under nounset,
	// matching bash: "!name: unbound variable".
	t.Run("StarSubscriptErrorUnderNounset", func(t *testing.T) {
		word := parseCommandWord(t, "\"${!name}\"")
		_, err := Fields(&Config{
			NoUnset: true,
			Env: testEnv{
				"name": {Set: true, Kind: String, Str: "arr[*]"},
			},
		}, word)
		if err == nil {
			t.Fatal("expected unbound variable error for unset arr[*] under nounset, got nil")
		}
		var unsetErr UnsetParameterError
		if !errors.As(err, &unsetErr) {
			t.Fatalf("expected UnsetParameterError, got %T: %v", err, err)
		}
	})

	// Outer default operators must still fire for [*] under nounset; the
	// operator handles the unset case and no error should be produced.
	t.Run("StarSubscriptDefaultOpSilentUnderNounset", func(t *testing.T) {
		word := parseCommandWord(t, "${!name:-fallback}")
		got, err := Fields(&Config{
			NoUnset: true,
			Env: testEnv{
				"name": {Set: true, Kind: String, Str: "arr[*]"},
			},
		}, word)
		if err != nil {
			t.Fatalf("did not want error for ${!name:-fallback} with unset arr[*], got %v", err)
		}
		if !reflect.DeepEqual(got, []string{"fallback"}) {
			t.Fatalf("wanted [\"fallback\"], got %q", got)
		}
	})
}

func TestFieldsUnquotedIndirectAllElementsTargets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  testEnv
		want []string
	}{
		{
			name: "SpecialAtCustomIFS",
			env: testEnv{
				"IFS":  {Set: true, Kind: String, Str: "zx"},
				"name": {Set: true, Kind: String, Str: "@"},
				"@":    {Set: true, Kind: Indexed, List: []string{"a b", "c"}},
			},
			want: []string{"a b", "c"},
		},
		{
			name: "ArrayAtCustomIFS",
			env: testEnv{
				"IFS":  {Set: true, Kind: String, Str: "zx"},
				"name": {Set: true, Kind: String, Str: "arr[@]"},
				"arr":  {Set: true, Kind: Indexed, List: []string{"a b", "c"}},
			},
			want: []string{"a b", "c"},
		},
		{
			name: "SpecialAtEmptyIFS",
			env: testEnv{
				"IFS":  {Set: true, Kind: String, Str: ""},
				"name": {Set: true, Kind: String, Str: "@"},
				"@":    {Set: true, Kind: Indexed, List: []string{"a", "b"}},
			},
			want: []string{"a", "b"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			word := parseCommandWord(t, `${!name}`)
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

func TestFieldsQuotedIndirectNamerefArrayTargetPreservesElements(t *testing.T) {
	t.Parallel()

	word := parseCommandWord(t, "\"${!name}\"")
	got, err := Fields(&Config{
		Env: testEnv{
			"name": {Set: true, Kind: String, Str: "ref[@]"},
			"ref":  {Set: true, Kind: NameRef, Str: "arr"},
			"arr":  {Set: true, Kind: Indexed, List: []string{"a b", "c"}},
		},
	}, word)
	if err != nil {
		t.Fatalf("did not want error, got %v", err)
	}
	want := []string{"a b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wanted %q, got %q", want, got)
	}
}

func TestFieldsIndirectWhitespaceArithmeticSubscript(t *testing.T) {
	t.Parallel()

	word := parseCommandWord(t, "\"${!name}\"")
	got, err := Fields(&Config{
		Env: testEnv{
			"name": {Set: true, Kind: String, Str: "arr[i + 1]"},
			"i":    {Set: true, Kind: String, Str: "0"},
			"arr":  {Set: true, Kind: Indexed, List: []string{"zero", "one", "two"}},
		},
	}, word)
	if err != nil {
		t.Fatalf("did not want error, got %v", err)
	}
	want := []string{"one"}
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

func TestNamesByPrefixSortsResults(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Env: layeredTestEnv{
			values: map[string]Variable{
				"foo2": {Set: true, Kind: String, Str: "two"},
				"foo1": {Set: true, Kind: String, Str: "one"},
			},
			entries: []testEnvEntry{
				{name: "foo2", vr: Variable{Set: true, Kind: String, Str: "two"}},
				{name: "foo1", vr: Variable{Set: true, Kind: String, Str: "one"}},
			},
		},
	}

	got := cfg.namesByPrefix("foo")
	want := []string{"foo1", "foo2"}
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

func TestFieldsWordSplittingPreservesCustomIFSEmpties(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		env  testEnv
		want []string
	}{
		{
			name: "ConsecutiveNonWhitespaceDelimiters",
			src:  `$x`,
			env: testEnv{
				"IFS": {Set: true, Kind: String, Str: "x"},
				"x":   {Set: true, Kind: String, Str: "xxa"},
			},
			want: []string{"", "", "a"},
		},
		{
			name: "MixedWhitespaceAndNonWhitespace",
			src:  `$x`,
			env: testEnv{
				"IFS": {Set: true, Kind: String, Str: " :"},
				"x":   {Set: true, Kind: String, Str: " :a:: b:"},
			},
			want: []string{"", "a", "", "b"},
		},
		{
			name: "GlobMetacharacterDelimiter",
			src:  `$x`,
			env: testEnv{
				"IFS": {Set: true, Kind: String, Str: "*"},
				"x":   {Set: true, Kind: String, Str: "*a**"},
			},
			want: []string{"", "a", ""},
		},
		{
			name: "UnicodeDelimiterUsesIFSRune",
			src:  `$x`,
			env: testEnv{
				"IFS": {Set: true, Kind: String, Str: "ç"},
				"x":   {Set: true, Kind: String, Str: "çx"},
			},
			want: []string{"", "x"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			word := parseCommandWord(t, tc.src)
			got, err := Fields(&Config{Env: tc.env}, word)
			if err != nil {
				t.Fatalf("Fields(%q) error = %v", tc.src, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Fields(%q) = %#v, want %#v", tc.src, got, tc.want)
			}
		})
	}
}

func TestFieldsUnquotedArrayExpansionsRespectSplitContext(t *testing.T) {
	t.Parallel()

	defaultEmptyArgs := testEnv{
		"@": {Set: true, Kind: Indexed, List: []string{"", "", "", "", ""}},
		"*": {Set: true, Kind: Indexed, List: []string{"", "", "", "", ""}},
	}
	customEmptyArgs := testEnv{
		"IFS": {Set: true, Kind: String, Str: "x"},
		"@":   {Set: true, Kind: Indexed, List: []string{"", "", "", "", ""}},
		"*":   {Set: true, Kind: Indexed, List: []string{"", "", "", "", ""}},
	}

	tests := []struct {
		name string
		cfg  *Config
		src  string
		want []string
	}{
		{
			name: "DefaultIFSAtInLargerWord",
			cfg:  &Config{Env: defaultEmptyArgs},
			src:  `=$@=`,
			want: []string{"=", "="},
		},
		{
			name: "EmptyIFSAtInLargerWord",
			cfg: &Config{Env: testEnv{
				"IFS": {Set: true, Kind: String, Str: ""},
				"@":   {Set: true, Kind: Indexed, List: []string{"", "", "", "", ""}},
				"*":   {Set: true, Kind: Indexed, List: []string{"", "", "", "", ""}},
			}},
			src:  `=$@=`,
			want: []string{"=", "="},
		},
		{
			name: "CustomIFSAtInLargerWord",
			cfg:  &Config{Env: customEmptyArgs},
			src:  `=$@=`,
			want: []string{"=", "", "", "", "="},
		},
		{
			name: "CustomIFSStarInLargerWord",
			cfg:  &Config{Env: customEmptyArgs},
			src:  `=$*=`,
			want: []string{"=", "", "", "", "="},
		},
		{
			name: "CustomIFSStarBareWord",
			cfg:  &Config{Env: customEmptyArgs},
			src:  `$*`,
			want: []string{"", "", "", ""},
		},
		{
			name: "ArrayAtCustomIFS",
			cfg: &Config{
				Env: testEnv{
					"IFS": {Set: true, Kind: String, Str: "z"},
					"a":   {Set: true, Kind: Indexed, List: []string{"a b", "c", ""}},
				},
			},
			src:  `${a[@]}`,
			want: []string{"a b", "c"},
		},
		{
			name: "ArrayAtEmptyIFSElidesEmptyElements",
			cfg: &Config{
				Env: testEnv{
					"IFS": {Set: true, Kind: String, Str: ""},
					"a":   {Set: true, Kind: Indexed, List: []string{"", "one", ""}},
				},
			},
			src:  `${a[@]}`,
			want: []string{"one"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			word := parseCommandWord(t, tc.src)
			got, err := Fields(tc.cfg, word)
			if err != nil {
				t.Fatalf("Fields(%q) error = %v", tc.src, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Fields(%q) = %#v, want %#v", tc.src, got, tc.want)
			}
		})
	}
}

func TestLiteralArrayJoinUsesStringContextRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "AtUsesSpaces",
			src:  `$@`,
			want: "x y z",
		},
		{
			name: "StarUsesIFSFirstByte",
			src:  `$*`,
			want: "x:y z",
		},
	}

	cfg := &Config{
		Env: testEnv{
			"IFS": {Set: true, Kind: String, Str: ":"},
			"@":   {Set: true, Kind: Indexed, List: []string{"x", "y z"}},
			"*":   {Set: true, Kind: Indexed, List: []string{"x", "y z"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			word := parseWord(t, tc.src)
			got, err := Literal(cfg, word)
			if err != nil {
				t.Fatalf("Literal(%q) error = %v", tc.src, err)
			}
			if got != tc.want {
				t.Fatalf("Literal(%q) = %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}

func TestFieldsUnquotedParamOperatorWordKeepsOuterLiteralBoundaries(t *testing.T) {
	t.Parallel()

	word := parseCommandWord(t, `1${undef:-"2 3" "4 5"}6`)
	got, err := Fields(&Config{Env: testEnv{}}, word)
	if err != nil {
		t.Fatalf("Fields() error = %v", err)
	}
	want := []string{"12 3", "4 56"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Fields() = %#v, want %#v", got, want)
	}
}

func TestFieldsUnquotedArrayOperatorWordKeepsQuotedBoundaries(t *testing.T) {
	t.Parallel()

	word := parseCommandWord(t, `1${arr[@]:-"2 3" "4 5"}6`)
	got, err := Fields(&Config{Env: testEnv{}}, word)
	if err != nil {
		t.Fatalf("Fields() error = %v", err)
	}
	want := []string{"12 3", "4 56"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Fields() = %#v, want %#v", got, want)
	}
}

func TestFieldsCurrentUserHomeEmptyPreservesEmptyField(t *testing.T) {
	t.Parallel()

	word := parseCommandWord(t, `~`)
	got, err := Fields(&Config{
		Env: testEnv{
			"HOME": {Set: true, Kind: String, Str: ""},
		},
	}, word)
	if err != nil {
		t.Fatalf("Fields() error = %v", err)
	}
	want := []string{""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Fields() = %#v, want %#v", got, want)
	}
}

func TestFieldsDoubleQuotedScalarWord(t *testing.T) {
	t.Parallel()

	word := parseCommandWord(t, `"== $y"`)
	got, err := Fields(&Config{
		Env: testEnv{
			"y": {Set: true, Kind: String, Str: "foo"},
		},
	}, word)
	if err != nil {
		t.Fatalf("Fields() error = %v", err)
	}
	want := []string{"== foo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Fields() = %#v, want %#v", got, want)
	}
}

func TestFieldsReparseBraceExpandedWords(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		StartupHome: "/startup",
		Env: testEnv{
			"a":         {Set: true, Kind: String, Str: "A"},
			"HOME":      {Set: true, Kind: String, Str: "/home/bob"},
			"HOME root": {Set: true, Kind: String, Str: "/root"},
		},
	}
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "SimpleVarAbsorbsBraceSuffix",
			src:  `{$a,b}_{c,d}`,
			want: []string{"b_c", "b_d"},
		},
		{
			name: "LiteralPrefixVarAbsorbsBraceSuffix",
			src:  `{_$a,b}_{c,d}`,
			want: []string{"_", "_", "b_c", "b_d"},
		},
		{
			name: "TildeExpandsAfterBraceSplit",
			src:  `{foo~,~}/bar`,
			want: []string{"foo~/bar", "/home/bob/bar"},
		},
		{
			name: "NamedUserTildeExpandsAfterBraceSplit",
			src:  `~{/src,root}`,
			want: []string{"/home/bob/src", "/root"},
		},
		{
			name: "QuotedBraceElementsStillQuoteAfterReparse",
			src:  `{'a',b}_{c,"d"}`,
			want: []string{"a_c", "a_d", "b_c", "b_d"},
		},
		{
			name: "LeadingCommentByteRemainsLiteral",
			src:  `{#foo,bar}`,
			want: []string{"#foo", "bar"},
		},
		{
			name: "MixedQuotesPreserveLiteralValue",
			src:  `-{\X"b",'cd'}-`,
			want: []string{"-Xb-", "-cd-"},
		},
		{
			name: "EmptyAlternativesRemainWords",
			src:  `{X,,Y,}`,
			want: []string{"X", "Y"},
		},
		{
			name: "EmptyAlternativesPreserveQuotedSuffix",
			src:  `{X,,Y,}''`,
			want: []string{"X", "", "Y", ""},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			word := parseCommandWord(t, tc.src)
			got, err := Fields(cfg, word)
			if err != nil {
				t.Fatalf("Fields() error = %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Fields() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestLiteralCurrentUserHomeUsesSandboxEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		cfg  *Config
		want string
	}{
		{
			name: "LiveHOME",
			src:  `~/src`,
			cfg: &Config{
				Env: testEnv{
					"HOME": {Set: true, Kind: String, Str: "/live"},
				},
			},
			want: "/live/src",
		},
		{
			name: "StartupHomeIgnored",
			src:  `~/src`,
			cfg: &Config{
				StartupHome: "/startup",
				Env: testEnv{
					"HOME": {Set: true, Kind: String, Str: "/live"},
				},
			},
			want: "/live/src",
		},
		{
			name: "RootHomeAvoidsDoubleSlash",
			src:  `~/src`,
			cfg: &Config{
				Env: testEnv{
					"HOME": {Set: true, Kind: String, Str: "/"},
				},
			},
			want: "/src",
		},
		{
			name: "TrailingSlashHomePreserved",
			src:  `~/src`,
			cfg: &Config{
				Env: testEnv{
					"HOME": {Set: true, Kind: String, Str: "/tmp/"},
				},
			},
			want: "/tmp//src",
		},
		{
			name: "EmptyHomeExpandsToEmptyString",
			src:  `~`,
			cfg: &Config{
				Env: testEnv{
					"HOME": {Set: true, Kind: String, Str: ""},
				},
			},
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			word := parseCommandWord(t, tc.src)
			got, err := Literal(tc.cfg, word)
			if err != nil {
				t.Fatalf("Literal() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("Literal() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLiteralNamedUserTildeRequiresSandboxMapping(t *testing.T) {
	t.Parallel()

	word := parseCommandWord(t, `~root/src`)
	got, err := Literal(&Config{Env: testEnv{}}, word)
	if err != nil {
		t.Fatalf("Literal() error = %v", err)
	}
	if got != "~root/src" {
		t.Fatalf("Literal() = %q, want %q", got, "~root/src")
	}
}

func TestReparseBraceWordSpecialLeadingTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		word *syntax.Word
		want string
	}{
		{
			name: "CommentByte",
			word: litWord("#foo"),
			want: "#foo",
		},
		{
			name: "RedirectByte",
			word: litWord(">"),
			want: ">",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := reparseBraceWord(tc.word)
			if err != nil {
				t.Fatalf("reparseBraceWord() error = %v", err)
			}
			if got.Lit() != tc.want {
				t.Fatalf("reparseBraceWord() = %q, want %q", got.Lit(), tc.want)
			}
		})
	}
}

func TestAssociativeAllElementSliceUsesBashOffsets(t *testing.T) {
	t.Parallel()

	env := testEnv{
		"assoc": {
			Set:  true,
			Kind: Associative,
			Map: map[string]string{
				"xx": "1",
				"yy": "2",
				"zz": "3",
				"aa": "4",
				"bb": "5",
			},
		},
	}

	tests := []struct {
		src  string
		want []string
	}{
		{`${assoc[@]:0:3}`, []string{"4", "1", "3"}},
		{`${assoc[@]:1:3}`, []string{"4", "1", "3"}},
		{`${assoc[@]:2:3}`, []string{"1", "3", "5"}},
		{`${assoc[@]: -2:2}`, []string{"5", "2"}},
		{`"${assoc[*]:1:3}"`, []string{"4 1 3"}},
	}

	for _, tt := range tests {
		word := parseCommandWord(t, tt.src)
		got, err := Fields(&Config{Env: env}, word)
		if err != nil {
			t.Fatalf("Fields(%q) error = %v", tt.src, err)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Fatalf("Fields(%q) = %q, want %q", tt.src, got, tt.want)
		}
	}
}

func TestFieldsSupportsMultibyteIFS(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Env: testEnv{
			"IFS": {Set: true, Kind: String, Str: "ç"},
			"x":   {Set: true, Kind: String, Str: "çx"},
		},
	}

	word := parseCommandWord(t, `$x`)
	got, err := Fields(cfg, word)
	if err != nil {
		t.Fatalf("Fields() error = %v", err)
	}
	want := []string{"", "x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Fields() = %#v, want %#v", got, want)
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
