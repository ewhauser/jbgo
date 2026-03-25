package commands

import "testing"

func TestParseCommandSpecGroupedShortAndAttachedValues(t *testing.T) {
	t.Parallel()
	spec := CommandSpec{
		Name: "probe",
		Options: []OptionSpec{
			{Name: "append", Short: 'a'},
			{Name: "separator", Short: 's', Arity: OptionRequiredValue},
		},
		Args: []ArgSpec{{Name: "file", Repeatable: true}},
		Parse: ParseConfig{
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
		},
	}

	matches, action, err := ParseCommandSpec(&Invocation{Args: []string{"-as,", "out.txt"}}, &spec)
	if err != nil {
		t.Fatalf("ParseCommandSpec() error = %v", err)
	}
	if action != "" {
		t.Fatalf("action = %q, want empty", action)
	}
	if !matches.Has("append") {
		t.Fatalf("append = false, want true")
	}
	if got, want := matches.Value("separator"), ","; got != want {
		t.Fatalf("separator = %q, want %q", got, want)
	}
	if got, want := matches.Args("file"), []string{"out.txt"}; !equalStrings(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
}

func TestParseCommandSpecSupportsLongInferenceAndDoubleDash(t *testing.T) {
	t.Parallel()
	spec := CommandSpec{
		Name: "probe",
		Options: []OptionSpec{
			{Name: "version", Long: "version"},
		},
		Args: []ArgSpec{{Name: "arg", Repeatable: true}},
		Parse: ParseConfig{
			InferLongOptions: true,
		},
	}

	versionSpec := CommandSpec{
		Name:  spec.Name,
		Parse: ParseConfig{InferLongOptions: true, AutoVersion: true},
	}
	helpMatches, action, err := ParseCommandSpec(&Invocation{Args: []string{"--ver"}}, &versionSpec)
	if err != nil {
		t.Fatalf("ParseCommandSpec(--ver) error = %v", err)
	}
	if action != "version" {
		t.Fatalf("action = %q, want version", action)
	}
	if helpMatches == nil {
		t.Fatalf("matches = nil, want non-nil")
	}

	matches, action, err := ParseCommandSpec(&Invocation{Args: []string{"--", "--version"}}, &spec)
	if err != nil {
		t.Fatalf("ParseCommandSpec(--) error = %v", err)
	}
	if action != "" {
		t.Fatalf("action = %q, want empty", action)
	}
	if got, want := matches.Args("arg"), []string{"--version"}; !equalStrings(got, want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
}

func TestParseCommandSpecOptionalValueEqualsOnly(t *testing.T) {
	t.Parallel()
	spec := CommandSpec{
		Name: "tee",
		Options: []OptionSpec{
			{Name: "output-error", Long: "output-error", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true},
		},
		Args: []ArgSpec{{Name: "file", Repeatable: true}},
		Parse: ParseConfig{
			LongOptionValueEquals: true,
		},
	}

	matches, action, err := ParseCommandSpec(&Invocation{Args: []string{"--output-error", "out.txt"}}, &spec)
	if err != nil {
		t.Fatalf("ParseCommandSpec() error = %v", err)
	}
	if action != "" {
		t.Fatalf("action = %q, want empty", action)
	}
	if !matches.Has("output-error") {
		t.Fatalf("output-error = false, want true")
	}
	if got := matches.Value("output-error"); got != "" {
		t.Fatalf("output-error value = %q, want empty", got)
	}
	if got, want := matches.Args("file"), []string{"out.txt"}; !equalStrings(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
}

func TestParseCommandSpecGroupedOptionalValueEqualsOnlyContinuesParsing(t *testing.T) {
	t.Parallel()
	spec := CommandSpec{
		Name: "ls",
		Options: []OptionSpec{
			{Name: "classify", Short: 'F', Arity: OptionOptionalValue, OptionalValueEqualsOnly: true},
			{Name: "dereference", Short: 'L'},
			{Name: "recursive", Short: 'R'},
		},
		Args: []ArgSpec{{Name: "file", Repeatable: true}},
		Parse: ParseConfig{
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
		},
	}

	matches, action, err := ParseCommandSpec(&Invocation{Args: []string{"-FLR", "sub"}}, &spec)
	if err != nil {
		t.Fatalf("ParseCommandSpec() error = %v", err)
	}
	if action != "" {
		t.Fatalf("action = %q, want empty", action)
	}
	for _, name := range []string{"classify", "dereference", "recursive"} {
		if !matches.Has(name) {
			t.Fatalf("%s = false, want true", name)
		}
	}
	if got, want := matches.Args("file"), []string{"sub"}; !equalStrings(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
}

func TestParseCommandSpecNegativeNumbersAsPositionals(t *testing.T) {
	t.Parallel()
	spec := CommandSpec{
		Name: "seq",
		Args: []ArgSpec{{Name: "number", Repeatable: true, Required: true}},
		Parse: ParseConfig{
			NegativeNumberPositional: true,
		},
	}

	matches, action, err := ParseCommandSpec(&Invocation{Args: []string{"-0.0", "1"}}, &spec)
	if err != nil {
		t.Fatalf("ParseCommandSpec() error = %v", err)
	}
	if action != "" {
		t.Fatalf("action = %q, want empty", action)
	}
	if got, want := matches.Args("number"), []string{"-0.0", "1"}; !equalStrings(got, want) {
		t.Fatalf("numbers = %v, want %v", got, want)
	}
}

func TestParseCommandSpecContinueShortGroupValuesConsumesLaterArgs(t *testing.T) {
	t.Parallel()
	spec := CommandSpec{
		Name: "probe",
		Options: []OptionSpec{
			{Name: "command", Short: 'c', Arity: OptionRequiredValue},
			{Name: "errexit", Short: 'e'},
			{Name: "nounset", Short: 'u'},
		},
		Parse: ParseConfig{
			GroupShortOptions:        true,
			ContinueShortGroupValues: true,
		},
	}

	matches, action, err := ParseCommandSpec(&Invocation{Args: []string{"-ceu", "echo hi"}}, &spec)
	if err != nil {
		t.Fatalf("ParseCommandSpec() error = %v", err)
	}
	if action != "" {
		t.Fatalf("action = %q, want empty", action)
	}
	if !matches.Has("errexit") {
		t.Fatalf("errexit = false, want true")
	}
	if !matches.Has("nounset") {
		t.Fatalf("nounset = false, want true")
	}
	if got, want := matches.Value("command"), "echo hi"; got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

func TestParseCommandSpecContinueShortGroupValuesSupportsMultiplePendingOptions(t *testing.T) {
	t.Parallel()
	spec := CommandSpec{
		Name: "probe",
		Options: []OptionSpec{
			{Name: "option", Short: 'o', Arity: OptionRequiredValue},
			{Name: "command", Short: 'c', Arity: OptionRequiredValue},
			{Name: "nounset", Short: 'u'},
		},
		Parse: ParseConfig{
			GroupShortOptions:        true,
			ContinueShortGroupValues: true,
		},
	}

	matches, action, err := ParseCommandSpec(&Invocation{Args: []string{"-ocu", "pipefail", "echo hi"}}, &spec)
	if err != nil {
		t.Fatalf("ParseCommandSpec() error = %v", err)
	}
	if action != "" {
		t.Fatalf("action = %q, want empty", action)
	}
	if !matches.Has("nounset") {
		t.Fatalf("nounset = false, want true")
	}
	if got, want := matches.Value("option"), "pipefail"; got != want {
		t.Fatalf("option = %q, want %q", got, want)
	}
	if got, want := matches.Value("command"), "echo hi"; got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

func TestParseCommandSpecPreservesExplicitEmptyRequiredOptionValue(t *testing.T) {
	t.Parallel()
	spec := CommandSpec{
		Name: "probe",
		Options: []OptionSpec{
			{Name: "unset", Short: 'u', Arity: OptionRequiredValue},
		},
		Args: []ArgSpec{{Name: "arg", Repeatable: true}},
	}

	matches, action, err := ParseCommandSpec(&Invocation{Args: []string{"-u", "", "tail"}}, &spec)
	if err != nil {
		t.Fatalf("ParseCommandSpec() error = %v", err)
	}
	if action != "" {
		t.Fatalf("action = %q, want empty", action)
	}
	if got, want := matches.Values("unset"), []string{""}; !equalStrings(got, want) {
		t.Fatalf("unset values = %v, want %v", got, want)
	}
	if got, want := matches.Args("arg"), []string{"tail"}; !equalStrings(got, want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
}
