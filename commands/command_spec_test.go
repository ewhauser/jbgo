package commands

import "testing"

func TestParseCommandSpecGroupedShortAndAttachedValues(t *testing.T) {
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

	matches, action, err := ParseCommandSpec(&Invocation{Args: []string{"-as,", "out.txt"}}, spec)
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

	helpMatches, action, err := ParseCommandSpec(&Invocation{Args: []string{"--ver"}}, CommandSpec{
		Name:  spec.Name,
		Parse: ParseConfig{InferLongOptions: true, AutoVersion: true},
	})
	if err != nil {
		t.Fatalf("ParseCommandSpec(--ver) error = %v", err)
	}
	if action != "version" {
		t.Fatalf("action = %q, want version", action)
	}
	if helpMatches == nil {
		t.Fatalf("matches = nil, want non-nil")
	}

	matches, action, err := ParseCommandSpec(&Invocation{Args: []string{"--", "--version"}}, spec)
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

	matches, action, err := ParseCommandSpec(&Invocation{Args: []string{"--output-error", "out.txt"}}, spec)
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

func TestParseCommandSpecNegativeNumbersAsPositionals(t *testing.T) {
	spec := CommandSpec{
		Name: "seq",
		Args: []ArgSpec{{Name: "number", Repeatable: true, Required: true}},
		Parse: ParseConfig{
			NegativeNumberPositional: true,
		},
	}

	matches, action, err := ParseCommandSpec(&Invocation{Args: []string{"-0.0", "1"}}, spec)
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
