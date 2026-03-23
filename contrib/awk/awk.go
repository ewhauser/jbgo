package awk

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"

	"github.com/ewhauser/gbash/contrib/awk/goawk/interp"
	"github.com/ewhauser/gbash/contrib/awk/goawk/parser"

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
		return exitf(inv, "awk: parse error: %v", err)
	}

	loadedInputs, err := loadAWKInputs(ctx, inv, inputs)
	if err != nil {
		return err
	}
	stdin := newLazyAWKStdin(ctx, inv)

	config := &interp.Config{
		Stdin:        stdin,
		Output:       inv.Stdout,
		Error:        inv.Stderr,
		Argv0:        "awk",
		Args:         inputs,
		Vars:         buildAWKVars(opts),
		Environ:      awkEnviron(inv.Env),
		NoExec:       true,
		NoFileWrites: true,
		NoFileReads:  true,
		FileOpener:   newAWKFileOpener(inv, stdin, loadedInputs),
	}
	status, err := interp.ExecProgram(compiled, config)
	if err != nil {
		return exitf(inv, "awk: %v", err)
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
				return awkOptions{}, "", nil, exitf(inv, "awk: option requires an argument -- 'F'")
			}
			opts.fieldSeparator = args[1]
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-F") && len(arg) > 2:
			opts.fieldSeparator = arg[2:]
		case arg == "-f":
			if len(args) < 2 {
				return awkOptions{}, "", nil, exitf(inv, "awk: option requires an argument -- 'f'")
			}
			opts.programFiles = append(opts.programFiles, args[1])
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-f") && len(arg) > 2:
			opts.programFiles = append(opts.programFiles, arg[2:])
		case arg == "-v":
			if len(args) < 2 {
				return awkOptions{}, "", nil, exitf(inv, "awk: option requires an argument -- 'v'")
			}
			if !strings.Contains(args[1], "=") {
				return awkOptions{}, "", nil, exitf(inv, "awk: expected name=value after -v")
			}
			opts.vars = append(opts.vars, args[1])
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-v") && len(arg) > 2:
			value := arg[2:]
			if !strings.Contains(value, "=") {
				return awkOptions{}, "", nil, exitf(inv, "awk: expected name=value after -v")
			}
			opts.vars = append(opts.vars, value)
		default:
			return awkOptions{}, "", nil, exitf(inv, "awk: unsupported flag %s", arg)
		}
		args = args[1:]
	}

	if len(opts.programFiles) == 0 {
		if len(args) == 0 {
			return awkOptions{}, "", nil, exitf(inv, "awk: missing program")
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

type awkInputs struct {
	Files map[string][]byte
}

var awkArgVarPattern = regexp.MustCompile(`^([_a-zA-Z][_a-zA-Z0-9]*)=(.*)`)

func loadAWKInputs(ctx context.Context, inv *commands.Invocation, names []string) (*awkInputs, error) {
	files := make(map[string][]byte)
	for _, name := range names {
		if name == "" || name == "-" || awkArgVarPattern.MatchString(name) {
			continue
		}
		resolved := inv.FS.Resolve(name)
		if _, ok := files[resolved]; ok {
			continue
		}
		data, err := readAllFile(ctx, inv, name)
		if err != nil {
			return nil, err
		}
		files[resolved] = data
	}
	return &awkInputs{
		Files: files,
	}, nil
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

func newAWKFileOpener(inv *commands.Invocation, stdin io.Reader, inputs *awkInputs) func(string) (io.ReadCloser, error) {
	return func(name string) (io.ReadCloser, error) {
		if name == "-" {
			return io.NopCloser(stdin), nil
		}
		data, ok := inputs.Files[inv.FS.Resolve(name)]
		if !ok {
			return nil, fmt.Errorf("can't read from file due to NoFileReads")
		}
		return io.NopCloser(bytes.NewReader(data)), nil
	}
}

type lazyAWKStdin struct {
	once   sync.Once
	load   func() ([]byte, error)
	data   []byte
	reader *bytes.Reader
	err    error
}

func newLazyAWKStdin(ctx context.Context, inv *commands.Invocation) io.Reader {
	return &lazyAWKStdin{
		load: func() ([]byte, error) {
			return commands.ReadAllStdin(ctx, inv)
		},
	}
}

func (r *lazyAWKStdin) Read(p []byte) (int, error) {
	r.once.Do(func() {
		r.data, r.err = r.load()
		if r.err == nil {
			r.reader = bytes.NewReader(r.data)
		}
		r.load = nil
	})
	if r.err != nil {
		return 0, r.err
	}
	return r.reader.Read(p)
}

func readAllFile(ctx context.Context, inv *commands.Invocation, name string) ([]byte, error) {
	file, err := inv.FS.Open(ctx, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	data, err := commands.ReadAll(ctx, inv, file)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func exitf(inv *commands.Invocation, format string, args ...any) error {
	return commands.Exitf(inv, 2, format, args...)
}

var _ commands.Command = (*AWK)(nil)
