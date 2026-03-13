package commands

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

type Sed struct{}

type sedOptions struct {
	quiet   bool
	inPlace bool
	scripts []string
}

type sedCommand struct {
	first       *sedAddress
	second      *sedAddress
	kind        sedCommandKind
	pattern     *regexp.Regexp
	replacement string
	global      bool
}

type sedCommandKind string

const (
	sedCommandDelete     sedCommandKind = "delete"
	sedCommandPrint      sedCommandKind = "print"
	sedCommandQuit       sedCommandKind = "quit"
	sedCommandSubstitute sedCommandKind = "substitute"
)

type sedAddress struct {
	kind    sedAddressKind
	line    int
	pattern *regexp.Regexp
}

type sedAddressKind string

const (
	sedAddressLine    sedAddressKind = "line"
	sedAddressLast    sedAddressKind = "last"
	sedAddressPattern sedAddressKind = "pattern"
)

func NewSed() *Sed {
	return &Sed{}
}

func (c *Sed) Name() string {
	return "sed"
}

func (c *Sed) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Sed) Spec() CommandSpec {
	return CommandSpec{
		Name:  "sed",
		About: "Stream editor",
		Usage: "sed [OPTION]... {script-only-if-no-other-script} [input-file]...\n\n  -n, --quiet, --silent\n                 suppress automatic printing of pattern space\n      --debug\n                 annotate program execution\n  -e script, --expression=script\n                 add the script to the commands to be executed\n  -f script-file, --file=script-file\n                 add the contents of script-file to the commands to be executed\n  -i[SUFFIX], --in-place[=SUFFIX]\n                 edit files in place (makes backup if SUFFIX supplied)\n  -E, -r, --regexp-extended\n                 use extended regular expressions in the script",
		Options: []OptionSpec{
			{Name: "quiet", Short: 'n', Long: "quiet", Aliases: []string{"silent"}, Help: "suppress automatic printing of pattern space"},
			{Name: "expression", Short: 'e', Long: "expression", Arity: OptionRequiredValue, ValueName: "script", Repeatable: true, Help: "add the script to the commands to be executed"},
			{Name: "file", Short: 'f', Long: "file", Arity: OptionRequiredValue, ValueName: "script-file", Repeatable: true, Help: "add the contents of script-file to the commands to be executed"},
			{Name: "in-place", Short: 'i', Long: "in-place", Help: "edit files in place"},
			{Name: "regexp-extended", Short: 'E', ShortAliases: []rune{'r'}, Long: "regexp-extended", Help: "use extended regular expressions in the script"},
		},
		Args: []ArgSpec{
			{Name: "arg", ValueName: "ARG", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			StopAtFirstPositional:    true,
		},
	}
}

func (c *Sed) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, files, err := parseSedMatches(ctx, inv, matches)
	if err != nil {
		return err
	}
	program, err := parseSedProgram(opts.scripts)
	if err != nil {
		return exitf(inv, 1, "sed: %v", err)
	}

	if opts.inPlace && len(files) == 0 {
		return exitf(inv, 1, "sed: no input files for in-place edit")
	}

	exitCode := 0
	if len(files) == 0 {
		data, err := readAllStdin(inv)
		if err != nil {
			return err
		}
		output := runSedProgram(program, textLines(data), opts.quiet)
		if _, err := inv.Stdout.Write(output); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		return nil
	}

	for _, file := range files {
		data, abs, err := readAllFile(ctx, inv, file)
		if err != nil {
			_, _ = fmt.Fprintf(inv.Stderr, "sed: %s: No such file or directory\n", file)
			exitCode = 1
			continue
		}
		output := runSedProgram(program, textLines(data), opts.quiet)
		if !opts.inPlace {
			if _, err := inv.Stdout.Write(output); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			continue
		}

		info, _, err := statPath(ctx, inv, abs)
		if err != nil {
			return err
		}
		if err := writeFileContents(ctx, inv, abs, output, info.Mode().Perm()); err != nil {
			return err
		}
	}

	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func parseSedMatches(ctx context.Context, inv *Invocation, matches *ParsedCommand) (sedOptions, []string, error) {
	opts := sedOptions{
		quiet:   matches.Has("quiet"),
		inPlace: matches.Has("in-place"),
	}
	for _, source := range matches.Values("expression") {
		appendSedScriptSource(&opts.scripts, source)
	}
	for _, name := range matches.Values("file") {
		if err := appendSedScriptFile(ctx, inv, &opts.scripts, name); err != nil {
			return sedOptions{}, nil, err
		}
	}

	args := matches.Args("arg")
	if len(opts.scripts) == 0 {
		if len(args) == 0 {
			return sedOptions{}, nil, exitf(inv, 1, "sed: missing script")
		}
		appendSedScriptSource(&opts.scripts, args[0])
		args = args[1:]
	}

	return opts, args, nil
}

func appendSedScriptFile(ctx context.Context, inv *Invocation, scripts *[]string, name string) error {
	data, _, err := readAllFile(ctx, inv, name)
	if err != nil {
		return err
	}
	appendSedScriptSource(scripts, string(data))
	return nil
}

func appendSedScriptSource(scripts *[]string, source string) {
	for line := range strings.SplitSeq(source, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		*scripts = append(*scripts, line)
	}
}

func parseSedProgram(scripts []string) ([]sedCommand, error) {
	program := make([]sedCommand, 0, len(scripts))
	for _, script := range scripts {
		command, err := parseSedCommand(strings.TrimSpace(script))
		if err != nil {
			return nil, err
		}
		program = append(program, command)
	}
	return program, nil
}

func parseSedCommand(script string) (sedCommand, error) {
	command := sedCommand{}

	first, rest, ok, err := parseSedAddress(script)
	if err != nil {
		return sedCommand{}, err
	}
	if ok {
		command.first = first
		script = strings.TrimSpace(rest)
		if strings.HasPrefix(script, ",") {
			second, rest, ok, err := parseSedAddress(strings.TrimSpace(script[1:]))
			if err != nil {
				return sedCommand{}, err
			}
			if !ok {
				return sedCommand{}, fmt.Errorf("missing second address")
			}
			command.second = second
			script = strings.TrimSpace(rest)
		}
	}

	if script == "" {
		return sedCommand{}, fmt.Errorf("missing sed command")
	}

	switch script[0] {
	case 'd':
		command.kind = sedCommandDelete
	case 'p':
		command.kind = sedCommandPrint
	case 'q':
		command.kind = sedCommandQuit
	case 's':
		pattern, replacement, flags, err := parseSedSubstitute(script)
		if err != nil {
			return sedCommand{}, err
		}
		command.kind = sedCommandSubstitute
		command.replacement = replacement
		command.global = strings.ContainsRune(flags, 'g')
		if strings.ContainsRune(flags, 'i') {
			pattern = "(?i)" + pattern
		}
		command.pattern, err = regexp.Compile(pattern)
		if err != nil {
			return sedCommand{}, err
		}
	default:
		return sedCommand{}, fmt.Errorf("unsupported sed command %q", script)
	}

	return command, nil
}

func parseSedAddress(script string) (address *sedAddress, remainder string, ok bool, err error) {
	script = strings.TrimSpace(script)
	if script == "" {
		return nil, "", false, nil
	}

	switch script[0] {
	case '$':
		return &sedAddress{kind: sedAddressLast}, script[1:], true, nil
	case '/':
		pattern, rest, err := parseSedDelimited(script[1:], '/')
		if err != nil {
			return nil, "", false, err
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, "", false, err
		}
		return &sedAddress{kind: sedAddressPattern, pattern: re}, rest, true, nil
	}

	if script[0] < '0' || script[0] > '9' {
		return nil, script, false, nil
	}

	line, rest, ok := consumeSortKeyNumber(script)
	if !ok {
		return nil, "", false, fmt.Errorf("invalid address")
	}
	return &sedAddress{kind: sedAddressLine, line: line}, rest, true, nil
}

func parseSedSubstitute(script string) (pattern, replacement, flags string, err error) {
	if len(script) < 2 {
		return "", "", "", fmt.Errorf("invalid substitute command")
	}
	delimiter := rune(script[1])
	pattern, rest, err := parseSedDelimited(script[2:], delimiter)
	if err != nil {
		return "", "", "", err
	}
	replacement, rest, err = parseSedDelimited(rest, delimiter)
	if err != nil {
		return "", "", "", err
	}
	flags = strings.TrimSpace(rest)
	for _, flag := range flags {
		switch flag {
		case 'g', 'i':
		default:
			return "", "", "", fmt.Errorf("unsupported substitute flag %q", string(flag))
		}
	}
	return pattern, replacement, flags, nil
}

func parseSedDelimited(input string, delimiter rune) (value, remainder string, err error) {
	var builder strings.Builder
	escape := false
	for index, r := range input {
		switch {
		case escape:
			if r != delimiter {
				builder.WriteRune('\\')
			}
			builder.WriteRune(r)
			escape = false
		case r == '\\':
			escape = true
		case r == delimiter:
			return builder.String(), input[index+len(string(r)):], nil
		default:
			builder.WriteRune(r)
		}
	}
	return "", "", fmt.Errorf("unterminated expression")
}

func runSedProgram(program []sedCommand, lines []string, quiet bool) []byte {
	output := make([]string, 0, len(lines))
	active := make([]bool, len(program))
	totalLines := len(lines)

	for index, original := range lines {
		current := original
		deleted := false
		quit := false
		printed := make([]string, 0, 1)

		for commandIndex, command := range program {
			applies, nextActive := sedCommandApplies(command, active[commandIndex], current, index+1, totalLines)
			active[commandIndex] = nextActive
			if !applies {
				continue
			}

			switch command.kind {
			case sedCommandDelete:
				deleted = true
			case sedCommandPrint:
				printed = append(printed, current)
			case sedCommandQuit:
				quit = true
			case sedCommandSubstitute:
				current = sedReplace(command.pattern, current, command.replacement, command.global)
			}

			if deleted || quit {
				break
			}
		}

		output = append(output, printed...)
		if !quiet && !deleted {
			output = append(output, current)
		}
		if quit {
			break
		}
	}

	if len(output) == 0 {
		return nil
	}
	return []byte(strings.Join(output, "\n") + "\n")
}

func sedCommandApplies(command sedCommand, active bool, line string, lineNumber, totalLines int) (applies, nextActive bool) {
	if command.second == nil {
		return sedAddressMatches(command.first, line, lineNumber, totalLines), false
	}
	if active {
		return true, !sedAddressMatches(command.second, line, lineNumber, totalLines)
	}
	if !sedAddressMatches(command.first, line, lineNumber, totalLines) {
		return false, false
	}
	return true, !sedAddressMatches(command.second, line, lineNumber, totalLines)
}

func sedAddressMatches(address *sedAddress, line string, lineNumber, totalLines int) bool {
	if address == nil {
		return true
	}
	switch address.kind {
	case sedAddressLine:
		return lineNumber == address.line
	case sedAddressLast:
		return lineNumber == totalLines
	case sedAddressPattern:
		return address.pattern.MatchString(line)
	default:
		return false
	}
}

func sedReplace(pattern *regexp.Regexp, line, replacement string, global bool) string {
	matches := pattern.FindAllStringSubmatchIndex(line, -1)
	if len(matches) == 0 {
		return line
	}
	if !global {
		matches = matches[:1]
	}

	var out strings.Builder
	cursor := 0
	for _, match := range matches {
		start, end := match[0], match[1]
		if start < cursor {
			continue
		}
		out.WriteString(line[cursor:start])
		out.WriteString(expandSedReplacement(replacement, line, match))
		cursor = end
	}
	out.WriteString(line[cursor:])
	return out.String()
}

func expandSedReplacement(replacement, line string, match []int) string {
	var out strings.Builder
	for index := 0; index < len(replacement); index++ {
		ch := replacement[index]
		switch ch {
		case '&':
			out.WriteString(line[match[0]:match[1]])
		case '\\':
			if index+1 >= len(replacement) {
				out.WriteByte('\\')
				continue
			}
			next := replacement[index+1]
			index++
			if next >= '1' && next <= '9' {
				group := int(next - '0')
				offset := group * 2
				if offset+1 < len(match) && match[offset] >= 0 {
					out.WriteString(line[match[offset]:match[offset+1]])
				}
				continue
			}
			out.WriteByte(next)
		default:
			out.WriteByte(ch)
		}
	}
	return out.String()
}

var _ Command = (*Sed)(nil)
