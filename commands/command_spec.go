package commands

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
)

// SpecProvider exposes declarative command metadata for shared parsing, help,
// and version rendering.
type SpecProvider interface {
	Spec() CommandSpec
}

// ParsedRunner executes a command after [ParseCommandSpec] has matched its
// arguments to a [CommandSpec].
type ParsedRunner interface {
	RunParsed(context.Context, *Invocation, *ParsedCommand) error
}

// ParseInvocationNormalizer lets a command adjust the invocation used for
// parsing without affecting the execution invocation.
type ParseInvocationNormalizer interface {
	NormalizeInvocation(*Invocation) *Invocation
}

// ParseErrorNormalizer lets a command rewrite parse failures from the shared
// parser into command-specific errors.
type ParseErrorNormalizer interface {
	NormalizeParseError(*Invocation, error) error
}

// LegacySpecProvider marks commands that intentionally keep legacy parsing
// behavior instead of the shared spec layer.
type LegacySpecProvider interface {
	LegacyReason() string
}

// CommandSpec describes a command's help text, arguments, options, and parser
// behavior.
type CommandSpec struct {
	Name            string
	About           string
	Usage           string
	AfterHelp       string
	Options         []OptionSpec
	Args            []ArgSpec
	Parse           ParseConfig
	HelpRenderer    func(io.Writer, CommandSpec) error
	VersionRenderer func(io.Writer, CommandSpec) error
}

// ParseConfig controls which command-line forms [ParseCommandSpec] accepts.
type ParseConfig struct {
	InferLongOptions         bool
	GroupShortOptions        bool
	ContinueShortGroupValues bool
	ShortOptionValueAttached bool
	LongOptionValueEquals    bool
	StopAtFirstPositional    bool
	NegativeNumberPositional bool
	AutoHelp                 bool
	AutoVersion              bool
}

// OptionArity describes whether an option accepts a value.
type OptionArity int

const (
	// OptionNoValue declares a flag that does not accept a value.
	OptionNoValue OptionArity = iota
	// OptionRequiredValue declares an option that must be followed by a value.
	OptionRequiredValue
	// OptionOptionalValue declares an option that may carry a value.
	OptionOptionalValue
)

// OptionSpec describes one named option in a [CommandSpec].
type OptionSpec struct {
	Name                    string
	Short                   rune
	ShortAliases            []rune
	Long                    string
	Aliases                 []string
	HelpAliases             []string
	Help                    string
	ValueName               string
	Arity                   OptionArity
	Repeatable              bool
	Hidden                  bool
	Overrides               []string
	OptionalValueEqualsOnly bool
}

// ArgSpec describes one positional argument in a [CommandSpec].
type ArgSpec struct {
	Name       string
	ValueName  string
	Help       string
	Required   bool
	Repeatable bool
	Default    []string
}

// ParsedCommand contains the parsed values for one [CommandSpec] invocation.
type ParsedCommand struct {
	Spec         CommandSpec
	optionValues map[string][]string
	optionCount  map[string]int
	optionOrder  []string
	optionEvents []ParsedOptionOccurrence
	argValues    map[string][]string
	positionals  []string
}

// ParsedOptionOccurrence records one parsed option occurrence in order.
type ParsedOptionOccurrence struct {
	Name     string
	Raw      string
	Value    string
	HasValue bool
}

// Has reports whether option name was seen.
func (m *ParsedCommand) Has(name string) bool {
	return m != nil && m.optionCount[name] > 0
}

// Count reports how many times option name was seen.
func (m *ParsedCommand) Count(name string) int {
	if m == nil {
		return 0
	}
	return m.optionCount[name]
}

// Value returns the last parsed value for option name.
func (m *ParsedCommand) Value(name string) string {
	if m == nil {
		return ""
	}
	values := m.optionValues[name]
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}

// Values returns all parsed values for option name.
func (m *ParsedCommand) Values(name string) []string {
	if m == nil {
		return nil
	}
	return append([]string(nil), m.optionValues[name]...)
}

// Arg returns the first parsed value for positional argument name.
func (m *ParsedCommand) Arg(name string) string {
	if m == nil {
		return ""
	}
	values := m.argValues[name]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// Args returns all parsed values for positional argument name.
func (m *ParsedCommand) Args(name string) []string {
	if m == nil {
		return nil
	}
	return append([]string(nil), m.argValues[name]...)
}

// Positionals returns the raw positional arguments in parse order.
func (m *ParsedCommand) Positionals() []string {
	if m == nil {
		return nil
	}
	return append([]string(nil), m.positionals...)
}

// RunCommand executes cmd through the shared spec layer when it implements
// [SpecProvider] and [ParsedRunner], otherwise it falls back to [Command.Run].
func RunCommand(ctx context.Context, cmd Command, inv *Invocation) error {
	if cmd == nil {
		return nil
	}
	specCmd, ok := cmd.(SpecProvider)
	if ok {
		if runner, ok := cmd.(ParsedRunner); ok {
			spec := specCmd.Spec()
			parseInv := inv
			if normalizer, ok := cmd.(ParseInvocationNormalizer); ok {
				parseInv = normalizer.NormalizeInvocation(inv)
			}
			return runCommandWithSpec(ctx, cmd, inv, parseInv, &spec, runner.RunParsed)
		}
	}
	return cmd.Run(ctx, inv)
}

func runCommandWithSpec(ctx context.Context, cmd Command, inv, parseInv *Invocation, spec *CommandSpec, run func(context.Context, *Invocation, *ParsedCommand) error) error {
	if parseInv == nil {
		parseInv = inv
	}
	if normalizer, ok := cmd.(ParseErrorNormalizer); ok && parseInv != nil {
		clone := *parseInv
		var stderr bytes.Buffer
		clone.Stderr = &stderr
		parseInv = &clone
		matches, action, err := ParseCommandSpec(parseInv, spec)
		if err != nil {
			return normalizer.NormalizeParseError(inv, err)
		}
		switch action {
		case "help":
			return RenderCommandHelp(inv.Stdout, spec)
		case "version":
			return RenderCommandVersion(inv.Stdout, spec)
		default:
			return run(ctx, inv, matches)
		}
	}
	matches, action, err := ParseCommandSpec(parseInv, spec)
	if err != nil {
		return err
	}
	switch action {
	case "help":
		return RenderCommandHelp(inv.Stdout, spec)
	case "version":
		return RenderCommandVersion(inv.Stdout, spec)
	default:
		return run(ctx, inv, matches)
	}
}

// ParseCommandSpec parses inv.Args against spec and returns the parsed matches.
//
// The returned action is "help" or "version" when automatic help/version
// handling should take over.
func ParseCommandSpec(inv *Invocation, spec *CommandSpec) (*ParsedCommand, string, error) {
	spec = normalizeCommandSpec(spec)
	parsed := &ParsedCommand{
		Spec:         *spec,
		optionValues: make(map[string][]string),
		optionCount:  make(map[string]int),
		optionOrder:  nil,
		argValues:    make(map[string][]string),
	}

	args := append([]string(nil), inv.Args...)
	parsingOptions := true
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]

		if parsingOptions && arg == "--" {
			parsingOptions = false
			continue
		}
		if parsingOptions && arg != "-" && strings.HasPrefix(arg, "--") {
			action, handled, err := parseLongOption(inv, spec, parsed, arg, &args)
			if err != nil || handled {
				return parsed, action, err
			}
			continue
		}
		if parsingOptions && arg != "-" && strings.HasPrefix(arg, "-") && (!spec.Parse.NegativeNumberPositional || !looksNegativeNumeric(arg)) {
			action, handled, err := parseShortOptions(inv, spec, parsed, arg, &args)
			if err != nil || handled {
				return parsed, action, err
			}
			continue
		}

		parsed.positionals = append(parsed.positionals, arg)
		if spec.Parse.StopAtFirstPositional {
			parsed.positionals = append(parsed.positionals, args...)
			args = nil
			break
		}
	}

	if err := assignArgSpecs(inv, parsed); err != nil {
		return nil, "", err
	}
	return parsed, "", nil
}

func normalizeCommandSpec(spec *CommandSpec) *CommandSpec {
	normalized := *spec
	if normalized.Parse.AutoHelp {
		normalized.Options = append(normalized.Options, OptionSpec{
			Name:  "help",
			Short: 'h',
			Long:  "help",
			Help:  "display this help and exit",
		})
	}
	if normalized.Parse.AutoVersion {
		normalized.Options = append(normalized.Options, OptionSpec{
			Name: "version",
			Long: "version",
			Help: "output version information and exit",
		})
	}
	return &normalized
}

func parseLongOption(inv *Invocation, spec *CommandSpec, parsed *ParsedCommand, raw string, rest *[]string) (action string, handled bool, err error) {
	name := strings.TrimPrefix(raw, "--")
	value := ""
	hasValue := false
	if spec.Parse.LongOptionValueEquals {
		name, value, hasValue = strings.Cut(name, "=")
	}

	opt, err := matchLongOption(spec, name)
	if err != nil {
		return "", false, commandUsageError(inv, spec.Name, "unrecognized option '%s'", raw)
	}
	if opt.Name == "help" && spec.Parse.AutoHelp {
		if hasValue {
			return "", false, commandUsageError(inv, spec.Name, "option '--help' doesn't allow an argument")
		}
		return "help", true, nil
	}
	if opt.Name == "version" && spec.Parse.AutoVersion {
		if hasValue {
			return "", false, commandUsageError(inv, spec.Name, "option '--version' doesn't allow an argument")
		}
		return "version", true, nil
	}
	if err := applyOptionValue(inv, spec, parsed, opt, raw, value, hasValue, rest, true); err != nil {
		return "", false, err
	}
	return "", false, nil
}

func parseShortOptions(inv *Invocation, spec *CommandSpec, parsed *ParsedCommand, raw string, rest *[]string) (action string, handled bool, err error) {
	shorts := strings.TrimPrefix(raw, "-")
	if !spec.Parse.GroupShortOptions && len(shorts) > 1 {
		return "", false, commandUsageError(inv, spec.Name, "invalid option -- '%c'", rune(shorts[0]))
	}

	type pendingShortOption struct {
		opt       *OptionSpec
		shownName string
	}

	var pending []pendingShortOption
	for i, ch := range shorts {
		opt, ok := matchShortOption(spec, ch)
		if !ok {
			return "", false, commandUsageError(inv, spec.Name, "invalid option -- '%c'", ch)
		}
		if opt.Name == "help" && spec.Parse.AutoHelp {
			if i != len(shorts)-1 {
				return "", false, commandUsageError(inv, spec.Name, "invalid option -- '%c'", rune(shorts[i+1]))
			}
			return "help", true, nil
		}
		if opt.Name == "version" && spec.Parse.AutoVersion {
			if i != len(shorts)-1 {
				return "", false, commandUsageError(inv, spec.Name, "invalid option -- '%c'", rune(shorts[i+1]))
			}
			return "version", true, nil
		}
		if opt.Arity == OptionNoValue {
			recordOption(parsed, opt.Name, "-"+string(ch), "", false)
			continue
		}

		remaining := shorts[i+1:]
		value := ""
		hasValue := false
		if spec.Parse.ShortOptionValueAttached && remaining != "" {
			if opt.Arity == OptionOptionalValue && opt.OptionalValueEqualsOnly {
				if strings.HasPrefix(remaining, "=") {
					value = remaining[1:]
					hasValue = true
				}
			} else {
				value = remaining
				hasValue = true
			}
		}
		if opt.Arity == OptionRequiredValue && spec.Parse.ContinueShortGroupValues && !hasValue {
			pending = append(pending, pendingShortOption{
				opt:       opt,
				shownName: "-" + string(ch),
			})
			continue
		}
		if err := applyOptionValue(inv, spec, parsed, opt, "-"+string(ch), value, hasValue, rest, false); err != nil {
			return "", false, err
		}
		if opt.Arity == OptionOptionalValue && !hasValue {
			continue
		}
		for _, item := range pending {
			if err := applyOptionValue(inv, spec, parsed, item.opt, item.shownName, "", false, rest, false); err != nil {
				return "", false, err
			}
		}
		return "", false, nil
	}
	for _, item := range pending {
		if err := applyOptionValue(inv, spec, parsed, item.opt, item.shownName, "", false, rest, false); err != nil {
			return "", false, err
		}
	}

	return "", false, nil
}

func applyOptionValue(inv *Invocation, spec *CommandSpec, parsed *ParsedCommand, opt *OptionSpec, shownName, value string, hasValue bool, rest *[]string, long bool) error {
	switch opt.Arity {
	case OptionNoValue:
		if hasValue {
			return commandUsageError(inv, spec.Name, "option '%s' doesn't allow an argument", shownName)
		}
		recordOption(parsed, opt.Name, shownName, "", false)
		return nil
	case OptionRequiredValue:
		if !hasValue {
			if len(*rest) == 0 {
				if long {
					return commandUsageError(inv, spec.Name, "option '--%s' requires an argument", opt.Long)
				}
				return commandUsageError(inv, spec.Name, "option requires an argument -- '%c'", opt.Short)
			}
			value = (*rest)[0]
			*rest = (*rest)[1:]
			hasValue = true
		}
		recordOption(parsed, opt.Name, shownName, value, hasValue)
		return nil
	case OptionOptionalValue:
		if !hasValue && long && !opt.OptionalValueEqualsOnly && len(*rest) > 0 && !strings.HasPrefix((*rest)[0], "-") {
			value = (*rest)[0]
			*rest = (*rest)[1:]
			hasValue = true
		}
		recordOption(parsed, opt.Name, shownName, value, hasValue)
		return nil
	default:
		return nil
	}
}

func recordOption(parsed *ParsedCommand, name, raw, value string, hasValue bool) {
	parsed.optionCount[name]++
	parsed.optionOrder = append(parsed.optionOrder, name)
	parsed.optionEvents = append(parsed.optionEvents, ParsedOptionOccurrence{
		Name:     name,
		Raw:      raw,
		Value:    value,
		HasValue: hasValue,
	})
	if value != "" || slices.Contains([]string{"output-error"}, name) {
		parsed.optionValues[name] = append(parsed.optionValues[name], value)
	}
}

func (m *ParsedCommand) OptionOrder() []string {
	if m == nil {
		return nil
	}
	return append([]string(nil), m.optionOrder...)
}

// OptionOccurrences returns parsed option occurrences in parse order, including
// whether each occurrence carried an explicit value.
func (m *ParsedCommand) OptionOccurrences() []ParsedOptionOccurrence {
	if m == nil {
		return nil
	}
	return append([]ParsedOptionOccurrence(nil), m.optionEvents...)
}

func assignArgSpecs(inv *Invocation, parsed *ParsedCommand) error {
	args := parsed.Spec.Args
	positionals := append([]string(nil), parsed.positionals...)
	for i, arg := range args {
		switch {
		case arg.Repeatable:
			values := positionals
			if len(values) == 0 && len(arg.Default) > 0 {
				values = append([]string(nil), arg.Default...)
			}
			if arg.Required && len(values) == 0 {
				return commandUsageError(inv, parsed.Spec.Name, "missing operand")
			}
			parsed.argValues[arg.Name] = values
			positionals = nil
		case len(positionals) == 0:
			if arg.Required {
				return commandUsageError(inv, parsed.Spec.Name, "missing operand")
			}
			if len(arg.Default) > 0 {
				parsed.argValues[arg.Name] = append([]string(nil), arg.Default...)
			}
		default:
			parsed.argValues[arg.Name] = []string{positionals[0]}
			positionals = positionals[1:]
		}
		if i == len(args)-1 && len(positionals) > 0 {
			return commandUsageError(inv, parsed.Spec.Name, "extra operand %s", quoteGNUOperand(positionals[0]))
		}
	}
	if len(args) == 0 && len(positionals) > 0 {
		parsed.argValues["args"] = positionals
	}
	return nil
}

func matchLongOption(spec *CommandSpec, name string) (*OptionSpec, error) {
	candidates := make([]*OptionSpec, 0, len(spec.Options))
	for i := range spec.Options {
		opt := &spec.Options[i]
		names := append([]string{opt.Long}, opt.Aliases...)
		for _, candidate := range names {
			if candidate == "" {
				continue
			}
			if candidate == name {
				return opt, nil
			}
			if spec.Parse.InferLongOptions && strings.HasPrefix(candidate, name) {
				candidates = append(candidates, opt)
				break
			}
		}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	return nil, fmt.Errorf("no long option match")
}

func matchShortOption(spec *CommandSpec, short rune) (*OptionSpec, bool) {
	for i := range spec.Options {
		opt := &spec.Options[i]
		if opt.Short == short {
			return opt, true
		}
		if slices.Contains(opt.ShortAliases, short) {
			return opt, true
		}
	}
	return nil, false
}

// RenderCommandHelp writes help text for spec using either the command-specific
// renderer or the shared default renderer.
func RenderCommandHelp(w io.Writer, spec *CommandSpec) error {
	spec = normalizeCommandSpec(spec)
	displaySpec := *spec
	displaySpec.Options = append([]OptionSpec(nil), spec.Options...)
	if displaySpec.Parse.AutoHelp && !hasOptionSpec(displaySpec.Options, "help") {
		displaySpec.Options = append(displaySpec.Options, OptionSpec{
			Name: "help",
			Long: "help",
			Help: "display this help and exit",
		})
	}
	if displaySpec.Parse.AutoVersion && !hasOptionSpec(displaySpec.Options, "version") {
		displaySpec.Options = append(displaySpec.Options, OptionSpec{
			Name: "version",
			Long: "version",
			Help: "output version information and exit",
		})
	}
	if spec.HelpRenderer != nil {
		return spec.HelpRenderer(w, displaySpec)
	}

	var b strings.Builder
	if displaySpec.About != "" {
		b.WriteString(displaySpec.About)
		b.WriteString("\n\n")
	}
	b.WriteString("Usage: ")
	if displaySpec.Usage != "" {
		b.WriteString(displaySpec.Usage)
	} else {
		b.WriteString(defaultUsage(&displaySpec))
	}
	b.WriteByte('\n')

	argLines := renderArgHelp(displaySpec.Args)
	if len(argLines) > 0 {
		b.WriteString("\nArguments:\n")
		for _, line := range argLines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}

	optionLines := renderOptionHelp(displaySpec.Options)
	if len(optionLines) > 0 {
		b.WriteString("\nOptions:\n")
		for _, line := range optionLines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	if displaySpec.AfterHelp != "" {
		b.WriteString("\n")
		b.WriteString(displaySpec.AfterHelp)
		if !strings.HasSuffix(displaySpec.AfterHelp, "\n") {
			b.WriteByte('\n')
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// RenderCommandVersion writes version output for spec using either the
// command-specific renderer or the shared default renderer.
func RenderCommandVersion(w io.Writer, spec *CommandSpec) error {
	if spec.VersionRenderer != nil {
		return spec.VersionRenderer(w, *spec)
	}
	return RenderSimpleVersion(w, spec.Name)
}

func renderArgHelp(args []ArgSpec) []string {
	lines := make([]string, 0, len(args))
	for i := range args {
		arg := &args[i]
		if arg.Help == "" {
			continue
		}
		label := argLabel(arg)
		line := fmt.Sprintf("  %-18s %s", label, arg.Help)
		if len(arg.Default) > 0 {
			line += fmt.Sprintf(" [default: %s]", strings.Join(arg.Default, ", "))
		}
		lines = append(lines, strings.TrimRight(line, " "))
	}
	return lines
}

func renderOptionHelp(opts []OptionSpec) []string {
	lines := make([]string, 0, len(opts))
	seen := map[string]bool{}
	for i := range opts {
		opt := &opts[i]
		if opt.Hidden || seen[opt.Name] {
			continue
		}
		seen[opt.Name] = true
		label := optionLabel(opt)
		lines = append(lines, fmt.Sprintf("  %-24s %s", label, opt.Help))
		for _, alias := range opt.HelpAliases {
			lines = append(lines, fmt.Sprintf("  %-24s %s", "--"+alias, opt.Help))
		}
	}
	return lines
}

func optionLabel(opt *OptionSpec) string {
	parts := make([]string, 0, 2)
	if opt.Short != 0 {
		short := "-" + string(opt.Short)
		if opt.Arity == OptionRequiredValue && opt.ValueName != "" {
			short += " " + opt.ValueName
		}
		parts = append(parts, short)
	}
	if opt.Long != "" {
		long := "--" + opt.Long
		if opt.Arity == OptionRequiredValue && opt.ValueName != "" {
			long += "=" + opt.ValueName
		} else if opt.Arity == OptionOptionalValue && opt.ValueName != "" {
			long += "[=" + opt.ValueName + "]"
		}
		parts = append(parts, long)
	}
	return strings.Join(parts, ", ")
}

func argLabel(arg *ArgSpec) string {
	label := arg.ValueName
	if label == "" {
		label = strings.ToUpper(arg.Name)
	}
	if arg.Repeatable {
		label += "..."
	}
	if !arg.Required {
		label = "[" + label + "]"
	}
	return label
}

func hasOptionSpec(opts []OptionSpec, name string) bool {
	for i := range opts {
		if opts[i].Name == name {
			return true
		}
	}
	return false
}

func defaultUsage(spec *CommandSpec) string {
	var parts []string
	parts = append(parts, spec.Name)
	if len(spec.Options) > 0 {
		parts = append(parts, "[OPTION]...")
	}
	for i := range spec.Args {
		arg := &spec.Args[i]
		part := argLabel(arg)
		part = strings.TrimPrefix(strings.TrimSuffix(part, "]"), "[")
		if !arg.Required {
			part = "[" + part + "]"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " ")
}

func commandUsageError(inv *Invocation, name, format string, args ...any) error {
	return exitf(inv, 1, "%s: %s\nTry '%s --help' for more information.", name, fmt.Sprintf(format, args...), name)
}

func looksNegativeNumeric(value string) bool {
	if len(value) < 2 || value[0] != '-' {
		return false
	}
	switch value[1] {
	case '.', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return true
	case 'i', 'I', 'n', 'N':
		return true
	default:
		return false
	}
}
