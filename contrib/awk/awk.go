package awk

import (
	"bytes"
	"context"
	"io"
	"strings"

	"github.com/benhoyt/goawk/interp"
	"github.com/benhoyt/goawk/parser"

	"github.com/ewhauser/gbash/commands"
)

type AWK struct{}

type awkOptions struct {
	fieldSeparator string
	programFiles   []string
	vars           []string
}

func NewAWK() *AWK {
	return &AWK{}
}

func Register(registry commands.CommandRegistry) error {
	if registry == nil {
		return nil
	}
	return registry.Register(NewAWK())
}

func (c *AWK) Name() string {
	return "awk"
}

func (c *AWK) Run(ctx context.Context, inv *commands.Invocation) error {
	opts, programText, inputs, err := parseAWKArgs(inv)
	if err != nil {
		return err
	}
	programSource, err := loadAWKProgram(ctx, inv, opts, programText)
	if err != nil {
		return err
	}

	compiled, err := parser.ParseProgram([]byte(programSource), nil)
	if err != nil {
		return exitf(inv, 2, "awk: parse error: %v", err)
	}

	stdinData, err := loadAWKInputs(ctx, inv, inputs)
	if err != nil {
		return err
	}

	config := &interp.Config{
		Stdin:        bytes.NewReader(stdinData),
		Output:       inv.Stdout,
		Error:        inv.Stderr,
		Argv0:        "awk",
		Vars:         buildAWKVars(opts),
		Environ:      awkEnviron(inv.Env),
		NoExec:       true,
		NoFileWrites: true,
		NoFileReads:  true,
	}
	status, err := interp.ExecProgram(compiled, config)
	if err != nil {
		return exitf(inv, 2, "awk: %v", err)
	}
	if status != 0 {
		return &commands.ExitError{Code: status}
	}
	return nil
}

func parseAWKArgs(inv *commands.Invocation) (opts awkOptions, programText string, inputs []string, err error) {
	args := inv.Args

	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			args = args[1:]
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			break
		}
		switch {
		case arg == "-F":
			if len(args) < 2 {
				return awkOptions{}, "", nil, exitf(inv, 2, "awk: option requires an argument -- 'F'")
			}
			opts.fieldSeparator = args[1]
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-F") && len(arg) > 2:
			opts.fieldSeparator = arg[2:]
		case arg == "-f":
			if len(args) < 2 {
				return awkOptions{}, "", nil, exitf(inv, 2, "awk: option requires an argument -- 'f'")
			}
			opts.programFiles = append(opts.programFiles, args[1])
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-f") && len(arg) > 2:
			opts.programFiles = append(opts.programFiles, arg[2:])
		case arg == "-v":
			if len(args) < 2 {
				return awkOptions{}, "", nil, exitf(inv, 2, "awk: option requires an argument -- 'v'")
			}
			if !strings.Contains(args[1], "=") {
				return awkOptions{}, "", nil, exitf(inv, 2, "awk: expected name=value after -v")
			}
			opts.vars = append(opts.vars, args[1])
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-v") && len(arg) > 2:
			value := arg[2:]
			if !strings.Contains(value, "=") {
				return awkOptions{}, "", nil, exitf(inv, 2, "awk: expected name=value after -v")
			}
			opts.vars = append(opts.vars, value)
		default:
			return awkOptions{}, "", nil, exitf(inv, 2, "awk: unsupported flag %s", arg)
		}
		args = args[1:]
	}

	if len(opts.programFiles) == 0 {
		if len(args) == 0 {
			return awkOptions{}, "", nil, exitf(inv, 2, "awk: missing program")
		}
		programText = args[0]
		args = args[1:]
	}
	return opts, programText, args, nil
}

func loadAWKProgram(ctx context.Context, inv *commands.Invocation, opts awkOptions, programText string) (string, error) {
	if len(opts.programFiles) == 0 {
		return programText, nil
	}
	var parts []string
	for _, name := range opts.programFiles {
		data, err := readAllFile(ctx, inv, name)
		if err != nil {
			return "", err
		}
		parts = append(parts, string(data))
	}
	return strings.Join(parts, "\n"), nil
}

func loadAWKInputs(ctx context.Context, inv *commands.Invocation, names []string) ([]byte, error) {
	inputs, err := readNamedInputs(ctx, inv, names, true)
	if err != nil {
		return nil, err
	}
	if len(inputs) == 0 {
		return nil, nil
	}
	var out bytes.Buffer
	for i, input := range inputs {
		if i > 0 && out.Len() > 0 && !bytes.HasSuffix(out.Bytes(), []byte{'\n'}) {
			out.WriteByte('\n')
		}
		out.Write(input.Data)
	}
	return out.Bytes(), nil
}

func buildAWKVars(opts awkOptions) []string {
	var vars []string
	if opts.fieldSeparator != "" {
		vars = append(vars, "FS", opts.fieldSeparator)
	}
	for _, value := range opts.vars {
		name, current, _ := strings.Cut(value, "=")
		vars = append(vars, name, current)
	}
	return vars
}

func awkEnviron(env map[string]string) []string {
	pairs := make([]string, 0, len(env)*2)
	for key, value := range env {
		pairs = append(pairs, key, value)
	}
	return pairs
}

type namedInput struct {
	Data []byte
}

func readNamedInputs(ctx context.Context, inv *commands.Invocation, names []string, defaultStdin bool) ([]namedInput, error) {
	if len(names) == 0 {
		if !defaultStdin {
			return nil, nil
		}
		data, err := io.ReadAll(inv.Stdin)
		if err != nil {
			return nil, &commands.ExitError{Code: 1, Err: err}
		}
		return []namedInput{{Data: data}}, nil
	}

	var (
		inputs    []namedInput
		stdinData []byte
		stdinRead bool
	)
	for _, name := range names {
		if name == "-" {
			if !stdinRead {
				data, err := io.ReadAll(inv.Stdin)
				if err != nil {
					return nil, &commands.ExitError{Code: 1, Err: err}
				}
				stdinData = data
				stdinRead = true
			}
			inputs = append(inputs, namedInput{Data: append([]byte(nil), stdinData...)})
			continue
		}

		data, err := readAllFile(ctx, inv, name)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, namedInput{Data: data})
	}
	return inputs, nil
}

func readAllFile(ctx context.Context, inv *commands.Invocation, name string) ([]byte, error) {
	file, err := inv.FS.Open(ctx, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, &commands.ExitError{Code: 1, Err: err}
	}
	return data, nil
}

func exitf(inv *commands.Invocation, code int, format string, args ...any) error {
	return commands.Exitf(inv, code, format, args...)
}

var _ commands.Command = (*AWK)(nil)
