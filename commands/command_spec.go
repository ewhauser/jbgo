package commands

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
)

type SpecProvider interface {
	Spec() CommandSpec
}

type ParsedRunner interface {
	RunParsed(context.Context, *Invocation, *ParsedCommand) error
}

type LegacySpecProvider interface {
	LegacyReason() string
}

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

type ParseConfig struct {
	InferLongOptions         bool
	GroupShortOptions        bool
	ShortOptionValueAttached bool
	LongOptionValueEquals    bool
	StopAtFirstPositional    bool
	NegativeNumberPositional bool
	AutoHelp                 bool
	AutoVersion              bool
}

type OptionArity int

const (
	OptionNoValue OptionArity = iota
	OptionRequiredValue
	OptionOptionalValue
)

type OptionSpec struct {
	Name                    string
	Short                   rune
	Long                    string
	Aliases                 []string
	Help                    string
	ValueName               string
	Arity                   OptionArity
	Repeatable              bool
	Hidden                  bool
	Overrides               []string
	OptionalValueEqualsOnly bool
}

type ArgSpec struct {
	Name       string
	ValueName  string
	Help       string
	Required   bool
	Repeatable bool
	Default    []string
}

type ParsedCommand struct {
	Spec         CommandSpec
	optionValues map[string][]string
	optionCount  map[string]int
	optionOrder  []string
	argValues    map[string][]string
	positionals  []string
}

func (m *ParsedCommand) Has(name string) bool {
	return m != nil && m.optionCount[name] > 0
}

func (m *ParsedCommand) Count(name string) int {
	if m == nil {
		return 0
	}
	return m.optionCount[name]
}

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

func (m *ParsedCommand) Values(name string) []string {
	if m == nil {
		return nil
	}
	return append([]string(nil), m.optionValues[name]...)
}

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

func (m *ParsedCommand) Args(name string) []string {
	if m == nil {
		return nil
	}
	return append([]string(nil), m.argValues[name]...)
}

func (m *ParsedCommand) Positionals() []string {
	if m == nil {
		return nil
	}
	return append([]string(nil), m.positionals...)
}

func RunCommand(ctx context.Context, cmd Command, inv *Invocation) error {
	if cmd == nil {
		return nil
	}
	specCmd, ok := cmd.(SpecProvider)
	if ok {
		if runner, ok := cmd.(ParsedRunner); ok {
			return runCommandWithSpec(ctx, inv, specCmd.Spec(), runner.RunParsed)
		}
	}
	return cmd.Run(ctx, inv)
}

func runCommandWithSpec(ctx context.Context, inv *Invocation, spec CommandSpec, run func(context.Context, *Invocation, *ParsedCommand) error) error {
	matches, action, err := ParseCommandSpec(inv, spec)
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

func ParseCommandSpec(inv *Invocation, spec CommandSpec) (*ParsedCommand, string, error) {
	spec = normalizeCommandSpec(spec)
	parsed := &ParsedCommand{
		Spec:         spec,
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
		if parsingOptions && arg != "-" && strings.HasPrefix(arg, "-") && !(spec.Parse.NegativeNumberPositional && looksNegativeNumeric(arg)) {
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

func normalizeCommandSpec(spec CommandSpec) CommandSpec {
	if spec.Parse.AutoHelp {
		spec.Options = append(spec.Options, OptionSpec{
			Name:  "help",
			Short: 'h',
			Long:  "help",
			Help:  "display this help and exit",
		})
	}
	if spec.Parse.AutoVersion {
		spec.Options = append(spec.Options, OptionSpec{
			Name: "version",
			Long: "version",
			Help: "output version information and exit",
		})
	}
	return spec
}

func parseLongOption(inv *Invocation, spec CommandSpec, parsed *ParsedCommand, raw string, rest *[]string) (string, bool, error) {
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

func parseShortOptions(inv *Invocation, spec CommandSpec, parsed *ParsedCommand, raw string, rest *[]string) (string, bool, error) {
	shorts := strings.TrimPrefix(raw, "-")
	if !spec.Parse.GroupShortOptions && len(shorts) > 1 {
		return "", false, commandUsageError(inv, spec.Name, "invalid option -- '%c'", rune(shorts[0]))
	}

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
			recordOption(parsed, opt.Name, "")
			continue
		}

		remaining := shorts[i+1:]
		value := ""
		hasValue := false
		if spec.Parse.ShortOptionValueAttached && remaining != "" {
			value = remaining
			hasValue = true
		}
		if err := applyOptionValue(inv, spec, parsed, opt, "-"+string(ch), value, hasValue, rest, false); err != nil {
			return "", false, err
		}
		return "", false, nil
	}

	return "", false, nil
}

func applyOptionValue(inv *Invocation, spec CommandSpec, parsed *ParsedCommand, opt OptionSpec, shownName, value string, hasValue bool, rest *[]string, long bool) error {
	switch opt.Arity {
	case OptionNoValue:
		if hasValue {
			return commandUsageError(inv, spec.Name, "option '%s' doesn't allow an argument", shownName)
		}
		recordOption(parsed, opt.Name, "")
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
		}
		recordOption(parsed, opt.Name, value)
		return nil
	case OptionOptionalValue:
		if !hasValue && long && !opt.OptionalValueEqualsOnly && len(*rest) > 0 && !strings.HasPrefix((*rest)[0], "-") {
			value = (*rest)[0]
			*rest = (*rest)[1:]
			hasValue = true
		}
		recordOption(parsed, opt.Name, value)
		return nil
	default:
		return nil
	}
}

func recordOption(parsed *ParsedCommand, name, value string) {
	parsed.optionCount[name]++
	parsed.optionOrder = append(parsed.optionOrder, name)
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

func matchLongOption(spec CommandSpec, name string) (OptionSpec, error) {
	candidates := make([]OptionSpec, 0, len(spec.Options))
	for _, opt := range spec.Options {
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
	return OptionSpec{}, fmt.Errorf("no long option match")
}

func matchShortOption(spec CommandSpec, short rune) (OptionSpec, bool) {
	for _, opt := range spec.Options {
		if opt.Short == short {
			return opt, true
		}
	}
	return OptionSpec{}, false
}

func RenderCommandHelp(w io.Writer, spec CommandSpec) error {
	spec = normalizeCommandSpec(spec)
	if spec.HelpRenderer != nil {
		return spec.HelpRenderer(w, spec)
	}

	var b strings.Builder
	if spec.About != "" {
		b.WriteString(spec.About)
		b.WriteString("\n\n")
	}
	b.WriteString("Usage: ")
	if spec.Usage != "" {
		b.WriteString(spec.Usage)
	} else {
		b.WriteString(defaultUsage(spec))
	}
	b.WriteByte('\n')

	argLines := renderArgHelp(spec.Args)
	if len(argLines) > 0 {
		b.WriteString("\nArguments:\n")
		for _, line := range argLines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}

	optionLines := renderOptionHelp(spec.Options)
	if len(optionLines) > 0 {
		b.WriteString("\nOptions:\n")
		for _, line := range optionLines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	if spec.AfterHelp != "" {
		b.WriteString("\n")
		b.WriteString(spec.AfterHelp)
		if !strings.HasSuffix(spec.AfterHelp, "\n") {
			b.WriteByte('\n')
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func RenderCommandVersion(w io.Writer, spec CommandSpec) error {
	if spec.VersionRenderer != nil {
		return spec.VersionRenderer(w, spec)
	}
	return RenderSimpleVersion(w, spec.Name)
}

func renderArgHelp(args []ArgSpec) []string {
	lines := make([]string, 0, len(args))
	for _, arg := range args {
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
	for _, opt := range opts {
		if opt.Hidden || seen[opt.Name] {
			continue
		}
		seen[opt.Name] = true
		label := optionLabel(opt)
		lines = append(lines, fmt.Sprintf("  %-24s %s", label, opt.Help))
	}
	return lines
}

func optionLabel(opt OptionSpec) string {
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

func argLabel(arg ArgSpec) string {
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

func defaultUsage(spec CommandSpec) string {
	var parts []string
	parts = append(parts, spec.Name)
	if len(spec.Options) > 0 {
		parts = append(parts, "[OPTION]...")
	}
	for _, arg := range spec.Args {
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
