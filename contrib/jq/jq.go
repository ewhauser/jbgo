package jq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	stdfs "io/fs"
	"slices"
	"strconv"
	"strings"

	"github.com/ewhauser/gbash/commands"
	"github.com/itchyny/gojq"
)

type JQ struct{}

type jqOptions struct {
	asciiOutput        bool
	buildConfiguration bool
	colorOutput        bool
	compact            bool
	exitStatus         bool
	fromFile           bool
	help               bool
	indent             *int
	join               bool
	modulePaths        []string
	monoOutput         bool
	nullInput          bool
	raw                bool
	rawInput           bool
	rawOutput0         bool
	seq                bool
	slurp              bool
	sortKeys           bool
	stream             bool
	streamErrors       bool
	tab                bool
	unbuffered         bool
	version            bool
	arg                map[string]string
	argJSON            map[string]string
	rawFile            map[string]string
	slurpFile          map[string]string
	stringArgs         []string
	jsonArgsRaw        []string
}

type jqSources struct {
	names []string
	data  [][]byte
}

func NewJQ() *JQ {
	return &JQ{}
}

func Register(registry commands.CommandRegistry) error {
	if registry == nil {
		return nil
	}
	return registry.Register(NewJQ())
}

func (c *JQ) Name() string {
	return "jq"
}

func (c *JQ) Run(ctx context.Context, inv *commands.Invocation) error {
	opts, filter, inputs, err := parseJQArgs(inv)
	if err != nil {
		return err
	}
	if opts.help {
		_, _ = io.WriteString(inv.Stdout, jqHelpText)
		return nil
	}
	if opts.version {
		_, _ = io.WriteString(inv.Stdout, jqVersionText)
		return nil
	}
	if opts.buildConfiguration {
		_, _ = io.WriteString(inv.Stdout, jqBuildConfigurationText)
		return nil
	}

	filter, err = loadJQFilter(ctx, inv, &opts, filter)
	if err != nil {
		return err
	}

	variableNames, variableValues, err := buildJQVariables(ctx, inv, &opts)
	if err != nil {
		return err
	}

	query, err := gojq.Parse(filter)
	if err != nil {
		return exitf(inv, 3, "jq: invalid query: %v", err)
	}

	compileIter := newJQInputIter(ctx, inv, &opts, inputs)
	defer func() { _ = compileIter.Close() }()

	compileOptions := []gojq.CompilerOption{
		gojq.WithEnvironLoader(func() []string { return jqEnviron(inv.Env) }),
		gojq.WithVariables(variableNames),
		gojq.WithFunction("input_filename", 0, 0, func(any, []any) any {
			name := compileIter.Name()
			if name != "" && (len(inputs) > 0 || !opts.nullInput) {
				return name
			}
			return nil
		}),
		gojq.WithInputIter(compileIter),
	}
	if len(opts.modulePaths) > 0 {
		compileOptions = append(compileOptions, gojq.WithModuleLoader(newSandboxJQModuleLoader(ctx, inv, opts.modulePaths)))
	}
	code, err := gojq.Compile(
		query,
		compileOptions...,
	)
	if err != nil {
		return exitf(inv, 3, "jq: compile error: %v", err)
	}

	processIter := jqIter(compileIter)
	if opts.nullInput {
		processIter = newJQNullInputIter()
	} else if err := compileIter.Load(); err != nil {
		return err
	}
	if err := processJQInputs(ctx, inv, code, processIter, variableValues, &opts); err != nil {
		return err
	}
	if err := compileIter.StickyError(); err != nil {
		return err
	}
	return nil
}

func parseJQArgs(inv *commands.Invocation) (opts jqOptions, filter string, inputs []string, err error) {
	args := inv.Args
	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			args = args[1:]
			break
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			break
		}
		if strings.HasPrefix(arg, "--") {
			args, err = parseJQLongFlag(inv, &opts, args)
			if err != nil {
				return jqOptions{}, "", nil, err
			}
			continue
		}

		args, err = parseJQShortFlags(inv, &opts, args)
		if err != nil {
			return jqOptions{}, "", nil, err
		}
	}

	opts.normalize()
	if opts.help || opts.version || opts.buildConfiguration {
		return opts, "", nil, nil
	}
	if len(args) == 0 {
		return jqOptions{}, "", nil, exitf(inv, 1, "jq: missing filter")
	}

	filter = args[0]
	args = args[1:]
	for len(args) > 0 {
		switch args[0] {
		case "--args":
			opts.stringArgs = append(opts.stringArgs, args[1:]...)
			return opts, filter, inputs, nil
		case "--jsonargs":
			opts.jsonArgsRaw = append(opts.jsonArgsRaw, args[1:]...)
			return opts, filter, inputs, nil
		default:
			inputs = append(inputs, args[0])
			args = args[1:]
		}
	}
	return opts, filter, inputs, nil
}

func parseJQLongFlag(inv *commands.Invocation, opts *jqOptions, args []string) ([]string, error) {
	arg := args[0]
	name, value, hasValue := splitJQLongFlag(arg)

	switch name {
	case "ascii":
		opts.asciiOutput = true
	case "ascii-output":
		opts.asciiOutput = true
	case "build-configuration":
		opts.buildConfiguration = true
	case "color":
		opts.colorOutput = true
	case "color-output":
		opts.colorOutput = true
	case "compact":
		opts.compact = true
	case "compact-output":
		opts.compact = true
	case "exit-status":
		opts.exitStatus = true
	case "from-file":
		opts.fromFile = true
	case "help":
		opts.help = true
	case "join-output":
		opts.join = true
	case "library-path":
		pathValue, rest, err := parseJQStringValue(inv, arg, value, hasValue, args[1:])
		if err != nil {
			return nil, err
		}
		opts.modulePaths = append(opts.modulePaths, pathValue)
		return rest, nil
	case "monochrome":
		opts.monoOutput = true
	case "monochrome-output":
		opts.monoOutput = true
	case "null-input":
		opts.nullInput = true
	case "raw-input":
		opts.rawInput = true
	case "raw-output":
		opts.raw = true
	case "raw-output0":
		opts.rawOutput0 = true
	case "seq":
		opts.seq = true
	case "slurp":
		opts.slurp = true
	case "sort-keys":
		opts.sortKeys = true
	case "stream":
		opts.stream = true
	case "stream-errors":
		opts.streamErrors = true
	case "tab":
		opts.tab = true
	case "unbuffered":
		opts.unbuffered = true
	case "version":
		opts.version = true
	case "indent":
		indentValue, rest, err := parseJQIntValue(inv, arg, value, hasValue, args[1:])
		if err != nil {
			return nil, err
		}
		opts.indent = &indentValue
		return rest, nil
	case "arg":
		nameValue, argValue, rest, err := parseJQPairValue(inv, arg, args[1:])
		if err != nil {
			return nil, err
		}
		if opts.arg == nil {
			opts.arg = make(map[string]string)
		}
		if _, exists := opts.arg[nameValue]; !exists {
			opts.arg[nameValue] = argValue
		}
		return rest, nil
	case "argjson":
		nameValue, argValue, rest, err := parseJQPairValue(inv, arg, args[1:])
		if err != nil {
			return nil, err
		}
		if opts.argJSON == nil {
			opts.argJSON = make(map[string]string)
		}
		if _, exists := opts.argJSON[nameValue]; !exists {
			opts.argJSON[nameValue] = argValue
		}
		return rest, nil
	case "rawfile":
		nameValue, argValue, rest, err := parseJQPairValue(inv, arg, args[1:])
		if err != nil {
			return nil, err
		}
		if opts.rawFile == nil {
			opts.rawFile = make(map[string]string)
		}
		if _, exists := opts.rawFile[nameValue]; !exists {
			opts.rawFile[nameValue] = argValue
		}
		return rest, nil
	case "slurpfile":
		nameValue, argValue, rest, err := parseJQPairValue(inv, arg, args[1:])
		if err != nil {
			return nil, err
		}
		if opts.slurpFile == nil {
			opts.slurpFile = make(map[string]string)
		}
		if _, exists := opts.slurpFile[nameValue]; !exists {
			opts.slurpFile[nameValue] = argValue
		}
		return rest, nil
	default:
		return nil, exitf(inv, 1, "jq: unrecognized option %q", arg)
	}
	return args[1:], nil
}

func parseJQShortFlags(inv *commands.Invocation, opts *jqOptions, args []string) ([]string, error) {
	shorts := args[0][1:]
	for shorts != "" {
		flag := shorts[0]
		shorts = shorts[1:]
		switch flag {
		case 'C':
			opts.colorOutput = true
		case 'L':
			if shorts != "" {
				opts.modulePaths = append(opts.modulePaths, shorts)
				return args[1:], nil
			}
			if len(args) < 2 {
				return nil, exitf(inv, 1, "jq: expected argument for -L")
			}
			opts.modulePaths = append(opts.modulePaths, args[1])
			return args[2:], nil
		case 'M':
			opts.monoOutput = true
		case 'a':
			opts.asciiOutput = true
		case 'c':
			opts.compact = true
		case 'e':
			opts.exitStatus = true
		case 'f':
			opts.fromFile = true
			if shorts != "" {
				return append([]string{shorts}, args[1:]...), nil
			}
		case 'h':
			opts.help = true
		case 'j':
			opts.join = true
		case 'n':
			opts.nullInput = true
		case 'r':
			opts.raw = true
		case 'R':
			opts.rawInput = true
		case 's':
			opts.slurp = true
		case 'S':
			opts.sortKeys = true
		case 'V':
			opts.version = true
		case 'v':
			opts.version = true
		default:
			return nil, exitf(inv, 1, "jq: invalid option -- %q", string(flag))
		}
	}
	return args[1:], nil
}

func (opts *jqOptions) normalize() {
	if opts == nil {
		return
	}
	if opts.rawOutput0 {
		opts.raw = true
	}
	if opts.join {
		opts.raw = true
	}
	if opts.streamErrors {
		opts.stream = true
	}
}

func splitJQLongFlag(arg string) (name, value string, hasValue bool) {
	name = strings.TrimPrefix(arg, "--")
	if before, after, ok := strings.Cut(name, "="); ok {
		return before, after, true
	}
	return name, "", false
}

func parseJQIntValue(inv *commands.Invocation, arg, inlineValue string, hasValue bool, rest []string) (parsed int, remaining []string, err error) {
	value := inlineValue
	if !hasValue {
		if len(rest) == 0 {
			return 0, nil, exitf(inv, 1, "jq: expected argument for %s", arg)
		}
		value = rest[0]
		rest = rest[1:]
	}
	parsed, err = strconv.Atoi(value)
	if err != nil {
		return 0, nil, exitf(inv, 1, "jq: invalid argument for %s: %v", arg, err)
	}
	if parsed < 0 {
		return 0, nil, exitf(inv, 1, "jq: negative indentation count: %d", parsed)
	}
	if parsed > 7 {
		return 0, nil, exitf(inv, 1, "jq: too many indentation count: %d", parsed)
	}
	return parsed, rest, nil
}

func parseJQStringValue(inv *commands.Invocation, arg, inlineValue string, hasValue bool, rest []string) (parsed string, remaining []string, err error) {
	value := inlineValue
	if !hasValue {
		if len(rest) == 0 {
			return "", nil, exitf(inv, 1, "jq: expected argument for %s", arg)
		}
		value = rest[0]
		rest = rest[1:]
	}
	return value, rest, nil
}

func parseJQPairValue(inv *commands.Invocation, arg string, rest []string) (name, value string, remaining []string, err error) {
	if len(rest) < 2 {
		return "", "", nil, exitf(inv, 1, "jq: expected 2 arguments for %s", arg)
	}
	return rest[0], rest[1], rest[2:], nil
}

func loadJQFilter(ctx context.Context, inv *commands.Invocation, opts *jqOptions, filter string) (string, error) {
	if !opts.fromFile {
		return filter, nil
	}
	data, err := readJQFile(ctx, inv, filter)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func buildJQVariables(ctx context.Context, inv *commands.Invocation, opts *jqOptions) (names []string, values []any, err error) {
	named := make(map[string]any)

	appendVar := func(name string, value any) {
		names = append(names, "$"+name)
		values = append(values, value)
		named[name] = value
	}

	for _, name := range sortedMapKeys(opts.arg) {
		appendVar(name, opts.arg[name])
	}

	for _, name := range sortedMapKeys(opts.argJSON) {
		value, err := decodeSingleJQJSON([]byte(opts.argJSON[name]))
		if err != nil {
			return nil, nil, exitf(inv, 1, "jq: --argjson %s: %v", name, err)
		}
		appendVar(name, value)
	}

	for _, name := range sortedMapKeys(opts.slurpFile) {
		data, err := readJQFile(ctx, inv, opts.slurpFile[name])
		if err != nil {
			return nil, nil, err
		}
		value, err := decodeJQJSON(data)
		if err != nil {
			return nil, nil, exitf(inv, 5, "jq: parse error in %s: %v", opts.slurpFile[name], err)
		}
		if value == nil {
			value = []any{}
		}
		appendVar(name, value)
	}

	for _, name := range sortedMapKeys(opts.rawFile) {
		data, err := readJQFile(ctx, inv, opts.rawFile[name])
		if err != nil {
			return nil, nil, err
		}
		appendVar(name, string(data))
	}

	positional := make([]any, 0, len(opts.stringArgs)+len(opts.jsonArgsRaw))
	for _, value := range opts.stringArgs {
		positional = append(positional, value)
	}
	for _, raw := range opts.jsonArgsRaw {
		value, err := decodeSingleJQJSON([]byte(raw))
		if err != nil {
			return nil, nil, exitf(inv, 1, "jq: --jsonargs: %v", err)
		}
		positional = append(positional, value)
	}
	appendVar("ARGS", map[string]any{
		"named":      named,
		"positional": positional,
	})

	return names, values, nil
}

func readJQInputSources(ctx context.Context, inv *commands.Invocation, inputs []string) (*jqSources, error) {
	if len(inputs) == 0 {
		data, err := readAllStdin(ctx, inv)
		if err != nil {
			return nil, err
		}
		return &jqSources{
			names: []string{"<stdin>"},
			data:  [][]byte{data},
		}, nil
	}

	sources := &jqSources{
		names: make([]string, 0, len(inputs)),
		data:  make([][]byte, 0, len(inputs)),
	}
	stdinUsed := false
	for _, input := range inputs {
		var (
			data []byte
			err  error
			name string
		)
		if input == "-" {
			name = "<stdin>"
			if stdinUsed {
				data = nil
			} else {
				data, err = readAllStdin(ctx, inv)
				stdinUsed = true
			}
		} else {
			name = input
			data, err = readJQFile(ctx, inv, input)
		}
		if err != nil {
			return nil, err
		}
		sources.names = append(sources.names, name)
		sources.data = append(sources.data, data)
	}
	return sources, nil
}

func rawLines(data []byte) []any {
	if len(data) == 0 {
		return nil
	}
	lines := make([]any, 0, bytes.Count(data, []byte{'\n'})+1)
	start := 0
	for start < len(data) {
		idx := bytes.IndexByte(data[start:], '\n')
		if idx < 0 {
			lines = append(lines, string(data[start:]))
			break
		}
		lines = append(lines, string(data[start:start+idx]))
		start += idx + 1
	}
	return lines
}

func readJQFile(ctx context.Context, inv *commands.Invocation, name string) ([]byte, error) {
	data, _, err := readAllFile(ctx, inv, name)
	if err == nil {
		return data, nil
	}

	var pathErr *stdfs.PathError
	switch {
	case errors.Is(err, stdfs.ErrNotExist), errors.As(err, &pathErr) && errors.Is(pathErr.Err, stdfs.ErrNotExist):
		return nil, exitf(inv, 2, "jq: %s: No such file or directory", name)
	default:
		return nil, exitf(inv, 2, "jq: %s: %v", name, err)
	}
}

func decodeJQJSON(data []byte) ([]any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var values []any
	for {
		var value any
		err := decoder.Decode(&value)
		if errors.Is(err, io.EOF) {
			return values, nil
		}
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
}

func decodeSingleJQJSON(data []byte) (any, error) {
	values, err := decodeJQJSON(data)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, errors.New("empty JSON input")
	}
	if len(values) > 1 {
		return nil, errors.New("expected a single JSON value")
	}
	return values[0], nil
}

func processJQInputs(ctx context.Context, inv *commands.Invocation, code *gojq.Code, iter jqIter, variableValues []any, opts *jqOptions) error {
	formatter := newJQFormatter(inv, opts)
	hadOutput := false
	var lastValue any

	for {
		value, ok := iter.Next()
		if !ok {
			break
		}
		stopped, err := runJQQuery(ctx, inv, code, value, variableValues, formatter, &hadOutput, &lastValue)
		if err != nil {
			return err
		}
		if stopped {
			break
		}
	}

	if opts.exitStatus {
		switch {
		case !hadOutput:
			return &commands.ExitError{Code: 4}
		case lastValue == nil || lastValue == false:
			return &commands.ExitError{Code: 1}
		}
	}
	return nil
}

func runJQQuery(ctx context.Context, inv *commands.Invocation, code *gojq.Code, input any, variableValues []any, formatter *jqFormatter, hadOutput *bool, lastValue *any) (bool, error) {
	iter := code.RunWithContext(ctx, input, variableValues...)
	for {
		value, ok := iter.Next()
		if !ok {
			return false, nil
		}
		if err, ok := value.(error); ok {
			var haltErr *gojq.HaltError
			if errors.As(err, &haltErr) && haltErr.Value() == nil {
				return true, nil
			}
			if _, ok := commands.ExitCode(err); ok {
				return false, err
			}
			return false, exitf(inv, 5, "jq: %v", err)
		}

		*hadOutput = true
		*lastValue = value

		if err := formatter.WriteValue(value); err != nil {
			if _, ok := commands.ExitCode(err); ok {
				return false, err
			}
			return false, &commands.ExitError{Code: 5, Err: err}
		}
	}
}

func jqEnviron(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+env[key])
	}
	return pairs
}

func sortedMapKeys[V any](values map[string]V) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

const jqHelpText = `jq - query JSON values inside the gbash sandbox

Usage:
  jq [options] <filter> [file ...]

Supported options:
  -a, --ascii-output     escape non-ASCII code points in JSON strings
  -c, --compact, --compact-output
                         produce compact JSON output
  -C, --color-output     force ANSI color in JSON output
  -e, --exit-status      set exit status based on the last output value
  -f, --from-file        read the jq filter from a file
  -h, --help             show this help text
  -j, --join-output      do not print a trailing newline after each result
  -L, --library-path dir search jq modules from dir inside the sandbox
  -M, --monochrome-output
                         disable ANSI color in JSON output
  -n, --null-input       run the filter once with null as input
  -r, --raw-output       print string results without JSON quotes
  -R, --raw-input        read input as raw strings
  -s, --slurp            read all inputs into an array and run once
  -S, --sort-keys        accepted for compatibility
      --build-configuration
                         show gbash jq build metadata
      --seq              parse input and frame output as JSON text sequences
      --stream           parse input in stream fashion
      --stream-errors    imply --stream and surface parse errors as tuples
      --unbuffered       flush after each output value when supported
  -V, --version          show version information
  --arg name value       bind a string variable
  --argjson name value   bind a JSON variable
  --args                 treat remaining arguments as string positional values
  --from-file            read the jq filter from a file
  --indent number        set indentation width
  --ascii                legacy alias for --ascii-output
  --color                legacy alias for --color-output
  --jsonargs             treat remaining arguments as JSON positional values
  --monochrome           legacy alias for --monochrome-output
  --raw-output0          write NUL delimiters instead of newlines
  --rawfile name file    bind a file's raw contents to a variable
  --slurpfile name file  bind a file's JSON values as an array
  --tab                  use tabs for indentation
  -v                     legacy alias for --version
`

const gojqVersion = "0.12.18"

const jqVersionText = "jq (gbash) backed by gojq v" + gojqVersion + "\n"

const jqBuildConfigurationText = "" +
	"implementation: jq (gbash)\n" +
	"engine: gojq v" + gojqVersion + "\n" +
	"module_loader: sandbox-fs\n" +
	"features: ascii-output,color-output,monochrome-output,unbuffered,stream,stream-errors,seq,library-path\n"

func exitf(inv *commands.Invocation, code int, format string, args ...any) error {
	return commands.Exitf(inv, code, format, args...)
}

func readAllStdin(ctx context.Context, inv *commands.Invocation) ([]byte, error) {
	data, err := commands.ReadAllStdin(ctx, inv)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func readAllFile(ctx context.Context, inv *commands.Invocation, name string) (data []byte, abs string, err error) {
	abs = name
	if inv != nil && inv.FS != nil {
		abs = inv.FS.Resolve(name)
	}

	file, err := inv.FS.Open(ctx, abs)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = file.Close() }()

	data, err = commands.ReadAll(ctx, inv, file)
	if err != nil {
		return nil, "", err
	}
	return data, abs, nil
}

var _ commands.Command = (*JQ)(nil)
