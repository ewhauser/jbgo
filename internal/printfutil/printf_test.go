package printfutil

import (
	"runtime"
	"testing"
	"time"
)

func TestFormatShellQuoteAndZeroPadStrings(t *testing.T) {
	t.Parallel()

	result := Format("(%06s)\n[%q]\n", []string{"42", "a b"}, Options{})
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; diagnostics=%v", result.ExitCode, result.Diagnostics)
	}
	want := "(000042)\n[a\\ b]\n"
	if runtime.GOOS == "linux" {
		want = "(    42)\n[a\\ b]\n"
	}
	if got := result.Output; got != want {
		t.Fatalf("Output = %q, want %q", got, want)
	}
}

func TestFormatSupportsUppercaseEAndAlternateHexZero(t *testing.T) {
	t.Parallel()

	result := Format("[%E][%#x][%#x][%#X][%#X]\n", []string{"3.14", "0", "42", "0", "42"}, Options{})
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; diagnostics=%v", result.ExitCode, result.Diagnostics)
	}
	if got, want := result.Output, "[3.140000E+00][0][0x2a][0][0X2A]\n"; got != want {
		t.Fatalf("Output = %q, want %q", got, want)
	}
}

func TestFormatQuotedCharUsesFirstByteForInvalidUnicode(t *testing.T) {
	t.Parallel()

	tooLarge := "'" + string([]byte{0xf4, 0x91, 0x84, 0x91})
	surrogate := "'" + string([]byte{0xed, 0xb0, 0x80})
	valid := "'μ"

	result := Format("%x\n%x\n%x\n", []string{tooLarge, surrogate, valid}, Options{})
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; diagnostics=%v", result.ExitCode, result.Diagnostics)
	}
	want := "f4\ned\n3bc\n"
	if runtime.GOOS == "linux" {
		want = "111111\ned\n3bc\n"
	}
	if got := result.Output; got != want {
		t.Fatalf("Output = %q, want %q", got, want)
	}
}

func TestFormatOverflowDiagnosticsMatchPlatformOracle(t *testing.T) {
	t.Parallel()

	result := Format("%d\n", []string{"18446744073709551616"}, Options{})
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; diagnostics=%v", result.ExitCode, result.Diagnostics)
	}
	want := "18446744073709551616: Result too large"
	if runtime.GOOS == "linux" {
		want = "18446744073709551616: Numerical result out of range"
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0] != want {
		t.Fatalf("Diagnostics = %v, want [%q]", result.Diagnostics, want)
	}
}

func TestFormatTimeSentinelsAndExtendedDirectives(t *testing.T) {
	t.Parallel()

	now := time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC)
	start := time.Date(2019, time.May, 15, 17, 3, 19, 0, time.UTC)
	result := Format("%(%F %T %z %s)T\n%(%F)T\n", []string{"-1", "-2"}, Options{
		LookupEnv: func(name string) (string, bool) {
			if name == "TZ" {
				return "UTC", true
			}
			return "", false
		},
		Now:       func() time.Time { return now },
		StartTime: start,
	})
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; diagnostics=%v", result.ExitCode, result.Diagnostics)
	}
	if got, want := result.Output, "2020-01-02 03:04:05 +0000 1577934245\n2019-05-15\n"; got != want {
		t.Fatalf("Output = %q, want %q", got, want)
	}
}

func TestFormatRejectsInvalidModifierBeforeVerb(t *testing.T) {
	t.Parallel()

	result := Format("%Zs\n", []string{"x"}, Options{})
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; diagnostics=%v", result.ExitCode, result.Diagnostics)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0] != "`Z': invalid format character" {
		t.Fatalf("Diagnostics = %v, want invalid-format diagnostic", result.Diagnostics)
	}
}

func TestFormatGNUSupportsEscapeE(t *testing.T) {
	t.Parallel()

	result := Format("\\e", nil, Options{Dialect: DialectGNU})
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; diagnostics=%v", result.ExitCode, result.Diagnostics)
	}
	if got, want := result.Output, "\x1b"; got != want {
		t.Fatalf("Output = %q, want %q", got, want)
	}
}

func TestFormatGNURejectsMalformedOuterEscapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		format string
		want   string
	}{
		{name: "hex", format: "A\\xZ", want: "A"},
		{name: "short-unicode", format: "A\\uabc", want: "A"},
		{name: "short-wide-unicode", format: "A\\U1234", want: "A"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := Format(tt.format, nil, Options{Dialect: DialectGNU})
			if result.ExitCode != 1 {
				t.Fatalf("ExitCode = %d, want 1; diagnostics=%v", result.ExitCode, result.Diagnostics)
			}
			if got := result.Output; got != tt.want {
				t.Fatalf("Output = %q, want %q", got, tt.want)
			}
			if len(result.Diagnostics) != 1 || result.Diagnostics[0] != "missing hexadecimal number in escape" {
				t.Fatalf("Diagnostics = %v, want missing-hex diagnostic", result.Diagnostics)
			}
		})
	}
}

func TestFormatGNUPercentBStopsOnMalformedEscape(t *testing.T) {
	t.Parallel()

	result := Format("%b|%s", []string{"A\\xZ", "B"}, Options{Dialect: DialectGNU})
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; diagnostics=%v", result.ExitCode, result.Diagnostics)
	}
	if got, want := result.Output, "A"; got != want {
		t.Fatalf("Output = %q, want %q", got, want)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0] != "missing hexadecimal number in escape" {
		t.Fatalf("Diagnostics = %v, want missing-hex diagnostic", result.Diagnostics)
	}
}

func TestFormatGNUQuoteMatchesCoreutils(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		arg  string
		want string
	}{
		{name: "empty", arg: "", want: "''"},
		{name: "raw", arg: "abc", want: "abc"},
		{name: "space", arg: "a b", want: "'a b'"},
		{name: "single-quote", arg: "'", want: "\"'\""},
		{name: "double-quote", arg: "\"", want: "'\"'"},
		{name: "newline", arg: "a\n", want: "'a'$'\\n'"},
		{name: "tab", arg: "a\tb", want: "'a'$'\\t''b'"},
		{name: "control", arg: string([]byte{0x01}), want: "''$'\\001'"},
		{name: "control-quote-control", arg: string([]byte{0x01, '\'', 0x01}), want: "''$'\\001'\\'''$'\\001'"},
		{name: "leading-tilde", arg: "~foo", want: "'~foo'"},
		{name: "leading-hash", arg: "#foo", want: "'#foo'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := Format("%q", []string{tt.arg}, Options{Dialect: DialectGNU})
			if result.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want 0; diagnostics=%v", result.ExitCode, result.Diagnostics)
			}
			if got := result.Output; got != tt.want {
				t.Fatalf("Output = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatGNURejectsShellOnlyConversionsAndFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		format string
		want   string
	}{
		{name: "time", format: "%(%F)T", want: "%(: invalid conversion specification"},
		{name: "quote-flag-s", format: "%'s", want: "%'s: invalid conversion specification"},
		{name: "quote-flag-c", format: "%'c", want: "%'c: invalid conversion specification"},
		{name: "quote-flag-x", format: "%'x", want: "%'x: invalid conversion specification"},
		{name: "quote-flag-o", format: "%'o", want: "%'o: invalid conversion specification"},
		{name: "width-q", format: "%7q", want: "%7q: invalid conversion specification"},
		{name: "width-b", format: "%7b", want: "%7b: invalid conversion specification"},
		{name: "length-q", format: "%lq", want: "%lq: invalid conversion specification"},
		{name: "length-llq", format: "%llq", want: "%llq: invalid conversion specification"},
		{name: "length-b", format: "%lb", want: "%lb: invalid conversion specification"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := Format(tt.format, []string{"0"}, Options{Dialect: DialectGNU})
			if result.ExitCode != 1 {
				t.Fatalf("ExitCode = %d, want 1; diagnostics=%v", result.ExitCode, result.Diagnostics)
			}
			if got := result.Output; got != "" {
				t.Fatalf("Output = %q, want empty", got)
			}
			if len(result.Diagnostics) != 1 || result.Diagnostics[0] != tt.want {
				t.Fatalf("Diagnostics = %v, want [%q]", result.Diagnostics, tt.want)
			}
		})
	}
}

func TestFormatGNUAllowsQuoteFlagAndLengthModifiersWhereCoreutilsDoes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		format string
		arg    string
		want   string
	}{
		{name: "quote-flag-d", format: "%'d", arg: "1000", want: "1000"},
		{name: "quote-flag-u", format: "%'u", arg: "1000", want: "1000"},
		{name: "quote-flag-f", format: "%'f", arg: "1000", want: "1000.000000"},
		{name: "length-s", format: "%ls", arg: "x", want: "x"},
		{name: "length-d", format: "%ld", arg: "10", want: "10"},
		{name: "length-f", format: "%Lf", arg: "10", want: "10.000000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := Format(tt.format, []string{tt.arg}, Options{Dialect: DialectGNU})
			if result.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want 0; diagnostics=%v", result.ExitCode, result.Diagnostics)
			}
			if got := result.Output; got != tt.want {
				t.Fatalf("Output = %q, want %q", got, tt.want)
			}
			if len(result.Diagnostics) != 0 {
				t.Fatalf("Diagnostics = %v, want empty", result.Diagnostics)
			}
		})
	}
}

func TestFormatGNUWarnsOnExcessArgsWithoutConversions(t *testing.T) {
	t.Parallel()

	result := Format("plain", []string{"extra", "more"}, Options{Dialect: DialectGNU})
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; diagnostics=%v", result.ExitCode, result.Diagnostics)
	}
	if got, want := result.Output, "plain"; got != want {
		t.Fatalf("Output = %q, want %q", got, want)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("Diagnostics = %v, want empty", result.Diagnostics)
	}
	if len(result.Warnings) != 1 || result.Warnings[0] != "warning: ignoring excess arguments, starting with 'extra'" {
		t.Fatalf("Warnings = %v, want excess-args warning", result.Warnings)
	}
}

func TestFormatGNUIndexedArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		format string
		args   []string
		want   string
	}{
		{name: "reorder", format: "%2$s%1$s\n", args: []string{"1", "2"}, want: "21\n"},
		{name: "repeat-format", format: "%1$s%1$s\n", args: []string{"1", "2"}, want: "11\n22\n"},
		{name: "mixed-sequential-and-indexed", format: "%s %s %1$s\n", args: []string{"A", "B"}, want: "A B A\n"},
		{name: "indexed-width-and-precision", format: "%1$*2$.*3$d\n", args: []string{"1", "3", "2"}, want: " 01\n"},
		{name: "indexed-main-with-sequential-width", format: "%2$*d\n", args: []string{"4", "1"}, want: "   1\n"},
		{name: "large-index-clamps-and-repeats-once", format: "empty%2147483648$s\n", args: []string{"foo"}, want: "empty\n"},
		{name: "indexed-and-sequential-share-cycle", format: "%100$*d %s %s %s\n", args: []string{"4", "1"}, want: "   0 1  \n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := Format(tt.format, tt.args, Options{Dialect: DialectGNU})
			if result.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want 0; diagnostics=%v warnings=%v", result.ExitCode, result.Diagnostics, result.Warnings)
			}
			if got := result.Output; got != tt.want {
				t.Fatalf("Output = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatGNURejectsInvalidFieldCombinationsBeforeOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		format string
		want   string
	}{
		{name: "alternate-decimal", format: "%#d", want: "%#d: invalid conversion specification"},
		{name: "zero-pad-string", format: "%0s", want: "%0s: invalid conversion specification"},
		{name: "precision-char", format: "%.9c", want: "%.9c: invalid conversion specification"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := Format(tt.format, []string{"0"}, Options{Dialect: DialectGNU})
			if result.ExitCode != 1 {
				t.Fatalf("ExitCode = %d, want 1; diagnostics=%v", result.ExitCode, result.Diagnostics)
			}
			if got := result.Output; got != "" {
				t.Fatalf("Output = %q, want empty", got)
			}
			if len(result.Diagnostics) != 1 || result.Diagnostics[0] != tt.want {
				t.Fatalf("Diagnostics = %v, want [%q]", result.Diagnostics, tt.want)
			}
		})
	}
}

func TestFormatGNUMatchesNumericDiagnosticsAndCharacterConstantWarnings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		format      string
		args        []string
		wantOutput  string
		wantDiag    string
		wantWarning string
	}{
		{name: "expected-numeric-value", format: "%d", args: []string{"a"}, wantOutput: "0", wantDiag: "'a': expected a numeric value"},
		{name: "value-not-completely-converted", format: "%d", args: []string{"9z"}, wantOutput: "9", wantDiag: "'9z': value not completely converted"},
		{name: "quoted-char-warning", format: "%d", args: []string{`"a"`}, wantOutput: "97", wantWarning: `warning: ": character(s) following character constant have been ignored`},
		{name: "lone-quote", format: "%d", args: []string{`"`}, wantOutput: "0", wantDiag: `'"': expected a numeric value`},
		{name: "invalid-precision-arg", format: "%.*dx", args: []string{"2147483648", "0"}, wantOutput: "", wantDiag: "invalid precision: '2147483648'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := Format(tt.format, tt.args, Options{Dialect: DialectGNU})
			if got := result.Output; got != tt.wantOutput {
				t.Fatalf("Output = %q, want %q", got, tt.wantOutput)
			}
			if tt.wantDiag == "" {
				if len(result.Diagnostics) != 0 {
					t.Fatalf("Diagnostics = %v, want empty", result.Diagnostics)
				}
			} else if len(result.Diagnostics) != 1 || result.Diagnostics[0] != tt.wantDiag {
				t.Fatalf("Diagnostics = %v, want [%q]", result.Diagnostics, tt.wantDiag)
			}
			if tt.wantWarning == "" {
				if len(result.Warnings) != 0 {
					t.Fatalf("Warnings = %v, want empty", result.Warnings)
				}
			} else if len(result.Warnings) != 1 || result.Warnings[0] != tt.wantWarning {
				t.Fatalf("Warnings = %v, want [%q]", result.Warnings, tt.wantWarning)
			}
		})
	}
}

func TestFormatGNUQuotesOverflowOperands(t *testing.T) {
	t.Parallel()

	result := Format("%d", []string{"999999999999999999999999999999"}, Options{Dialect: DialectGNU})
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; diagnostics=%v", result.ExitCode, result.Diagnostics)
	}
	if got, want := result.Output, "9223372036854775807"; got != want {
		t.Fatalf("Output = %q, want %q", got, want)
	}
	wantDiag := "'999999999999999999999999999999': Result too large"
	if runtime.GOOS == "linux" {
		wantDiag = "'999999999999999999999999999999': Numerical result out of range"
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0] != wantDiag {
		t.Fatalf("Diagnostics = %v, want [%q]", result.Diagnostics, wantDiag)
	}
}

func TestFormatGNUQuoteRespectsLocaleForNonASCII(t *testing.T) {
	t.Parallel()

	lookup := func(values map[string]string) func(string) (string, bool) {
		return func(name string) (string, bool) {
			value, ok := values[name]
			return value, ok
		}
	}

	cLocale := Format("%q\n%q\n", []string{"áḃç", string([]byte{0xc2, 0x81})}, Options{
		Dialect:   DialectGNU,
		LookupEnv: lookup(map[string]string{"LC_ALL": "C"}),
	})
	if cLocale.ExitCode != 0 {
		t.Fatalf("C locale ExitCode = %d, want 0; diagnostics=%v", cLocale.ExitCode, cLocale.Diagnostics)
	}
	if got, want := cLocale.Output, "''$'\\303\\241\\341\\270\\203\\303\\247'\n''$'\\302\\201'\n"; got != want {
		t.Fatalf("C locale Output = %q, want %q", got, want)
	}

	utf8Locale := Format("%q\n%q\n", []string{"áḃç", string([]byte{0xc2, 0x81})}, Options{
		Dialect:   DialectGNU,
		LookupEnv: lookup(map[string]string{"LC_ALL": "fr_FR.UTF-8"}),
	})
	if utf8Locale.ExitCode != 0 {
		t.Fatalf("UTF-8 locale ExitCode = %d, want 0; diagnostics=%v", utf8Locale.ExitCode, utf8Locale.Diagnostics)
	}
	if got, want := utf8Locale.Output, "áḃç\n''$'\\302\\201'\n"; got != want {
		t.Fatalf("UTF-8 locale Output = %q, want %q", got, want)
	}
}
