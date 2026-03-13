package jq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	compact     bool
	exitStatus  bool
	fromFile    bool
	help        bool
	indent      *int
	join        bool
	nullInput   bool
	raw         bool
	rawInput    bool
	rawOutput0  bool
	slurp       bool
	sortKeys    bool
	tab         bool
	version     bool
	arg         map[string]string
	argJSON     map[string]string
	rawFile     map[string]string
	slurpFile   map[string]string
	stringArgs  []string
	jsonArgsRaw []string
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
	code, err := gojq.Compile(
		query,
		gojq.WithEnvironLoader(func() []string { return jqEnviron(inv.Env) }),
		gojq.WithVariables(variableNames),
	)
	if err != nil {
		return exitf(inv, 3, "jq: compile error: %v", err)
	}

	if canStreamJQInputs(&opts) {
		return runJQInputStream(ctx, inv, code, &opts, inputs, variableValues)
	}

	values, err := collectJQInputs(ctx, inv, &opts, inputs)
	if err != nil {
		return err
	}

	hadOutput := false
	var lastValue any
	for _, value := range values {
		stopped, err := runJQQuery(ctx, inv, code, value, variableValues, &opts, &hadOutput, &lastValue)
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

func canStreamJQInputs(opts *jqOptions) bool {
	return opts != nil && !opts.nullInput && !opts.rawInput && !opts.slurp
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
	if opts.help || opts.version {
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
		// Accepted for compatibility with upstream; output remains UTF-8.
	case "color":
		// Accepted for compatibility with upstream; color output is ignored.
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
	case "monochrome":
		// Accepted for compatibility with upstream; output is already monochrome.
	case "null-input":
		opts.nullInput = true
	case "raw-input":
		opts.rawInput = true
	case "raw-output":
		opts.raw = true
	case "raw-output0":
		opts.rawOutput0 = true
	case "slurp":
		opts.slurp = true
	case "sort-keys":
		opts.sortKeys = true
	case "tab":
		opts.tab = true
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
			// Accepted for compatibility with upstream; color output is ignored.
		case 'M':
			// Accepted for compatibility with upstream; output is already monochrome.
		case 'a':
			// Accepted for compatibility with upstream; output remains UTF-8.
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

func collectJQInputs(ctx context.Context, inv *commands.Invocation, opts *jqOptions, inputs []string) ([]any, error) {
	if opts.nullInput {
		return []any{nil}, nil
	}

	sources, err := readJQInputSources(ctx, inv, inputs)
	if err != nil {
		return nil, err
	}
	if opts.rawInput {
		return collectRawJQInputs(sources, opts), nil
	}
	return collectJSONJQInputs(inv, sources, opts)
}

func readJQInputSources(ctx context.Context, inv *commands.Invocation, inputs []string) (*jqSources, error) {
	if len(inputs) == 0 {
		data, err := readAllStdin(inv)
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
				data, err = readAllStdin(inv)
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

func runJQInputStream(ctx context.Context, inv *commands.Invocation, code *gojq.Code, opts *jqOptions, inputs []string, variableValues []any) error {
	hadOutput := false
	var lastValue any

	stopped, err := streamJQJSONInputs(ctx, inv, inputs, func(value any) (bool, error) {
		return runJQQuery(ctx, inv, code, value, variableValues, opts, &hadOutput, &lastValue)
	})
	if err != nil {
		return err
	}
	if stopped {
		return nil
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

func streamJQJSONInputs(ctx context.Context, inv *commands.Invocation, inputs []string, visit func(value any) (bool, error)) (bool, error) {
	if len(inputs) == 0 {
		return decodeJQStream(inv, "<stdin>", jqStdinReader(inv), visit)
	}

	stdinUsed := false
	for _, input := range inputs {
		switch input {
		case "-":
			if stdinUsed {
				continue
			}
			stdinUsed = true
			stopped, err := decodeJQStream(inv, "<stdin>", jqStdinReader(inv), visit)
			if stopped || err != nil {
				return stopped, err
			}
		default:
			file, err := openJQRead(ctx, inv, input)
			if err != nil {
				return false, err
			}
			stopped, decodeErr := decodeJQStream(inv, input, file, visit)
			closeErr := file.Close()
			if decodeErr != nil {
				return false, decodeErr
			}
			if closeErr != nil {
				return false, &commands.ExitError{Code: 1, Err: closeErr}
			}
			if stopped {
				return true, nil
			}
		}
	}
	return false, nil
}

func decodeJQStream(inv *commands.Invocation, name string, reader io.Reader, visit func(value any) (bool, error)) (bool, error) {
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()

	for {
		var value any
		err := decoder.Decode(&value)
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, exitf(inv, 5, "jq: parse error in %s: %v", name, err)
		}
		stopped, visitErr := visit(value)
		if stopped || visitErr != nil {
			return stopped, visitErr
		}
	}
}

func collectRawJQInputs(sources *jqSources, opts *jqOptions) []any {
	if sources == nil {
		if opts.slurp {
			return []any{""}
		}
		return nil
	}
	if opts.slurp {
		var builder strings.Builder
		for _, data := range sources.data {
			builder.Write(data)
		}
		return []any{builder.String()}
	}

	values := make([]any, 0)
	for _, data := range sources.data {
		values = append(values, rawLines(data)...)
	}
	return values
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

func collectJSONJQInputs(inv *commands.Invocation, sources *jqSources, opts *jqOptions) ([]any, error) {
	if sources == nil {
		if opts.slurp {
			return []any{[]any{}}, nil
		}
		return nil, nil
	}

	values := make([]any, 0)
	for i, data := range sources.data {
		decoded, err := decodeJQJSON(data)
		if err != nil {
			return nil, exitf(inv, 5, "jq: parse error in %s: %v", sources.names[i], err)
		}
		values = append(values, decoded...)
	}
	if opts.slurp {
		return []any{values}, nil
	}
	return values, nil
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

func openJQRead(ctx context.Context, inv *commands.Invocation, name string) (stdfs.File, error) {
	abs := name
	if inv != nil && inv.FS != nil {
		abs = inv.FS.Resolve(name)
	}

	file, err := inv.FS.Open(ctx, abs)
	if err == nil {
		return file, nil
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

func runJQQuery(ctx context.Context, inv *commands.Invocation, code *gojq.Code, input any, variableValues []any, opts *jqOptions, hadOutput *bool, lastValue *any) (bool, error) {
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
			return false, exitf(inv, 5, "jq: %v", err)
		}

		*hadOutput = true
		*lastValue = value

		formatted, err := formatJQValue(value, opts)
		if err != nil {
			return false, &commands.ExitError{Code: 5, Err: err}
		}
		if _, err := inv.Stdout.Write(formatted); err != nil {
			return false, &commands.ExitError{Code: 1, Err: err}
		}
		switch {
		case opts.rawOutput0:
			if _, err := inv.Stdout.Write([]byte{0x00}); err != nil {
				return false, &commands.ExitError{Code: 1, Err: err}
			}
		case !opts.join:
			if _, err := io.WriteString(inv.Stdout, "\n"); err != nil {
				return false, &commands.ExitError{Code: 1, Err: err}
			}
		}
	}
}

func formatJQValue(value any, opts *jqOptions) ([]byte, error) {
	if opts.raw {
		if s, ok := value.(string); ok {
			if opts.rawOutput0 && strings.ContainsRune(s, '\x00') {
				return nil, fmt.Errorf("cannot output a string containing NUL character")
			}
			return []byte(s), nil
		}
	}

	data, err := gojq.Marshal(value)
	if err != nil {
		return nil, err
	}
	if opts.compact {
		return data, nil
	}
	if len(data) == 0 {
		return data, nil
	}

	switch data[0] {
	case '{', '[':
		var buf bytes.Buffer
		indent := "  "
		if opts.tab {
			indent = "\t"
		} else if opts.indent != nil {
			indent = strings.Repeat(" ", *opts.indent)
		}
		if err := json.Indent(&buf, data, "", indent); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	default:
		return data, nil
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
  -a, --ascii            accept upstream ASCII mode flag (output remains UTF-8)
  -c, --compact, --compact-output
                         produce compact JSON output
  -C, --color            accept upstream color mode flag (ignored)
  -e, --exit-status      set exit status based on the last output value
  -f, --from-file        read the jq filter from a file
  -h, --help             show this help text
  -j, --join-output      do not print a trailing newline after each result
  -M, --monochrome       accept upstream monochrome mode flag (ignored)
  -n, --null-input       run the filter once with null as input
  -r, --raw-output       print string results without JSON quotes
  -R, --raw-input        read input as raw strings
  -s, --slurp            read all inputs into an array and run once
  -S, --sort-keys        accepted for compatibility
  -v, --version          show version information
  --arg name value       bind a string variable
  --argjson name value   bind a JSON variable
  --args                 treat remaining arguments as string positional values
  --from-file            read the jq filter from a file
  --indent number        set indentation width
  --jsonargs             treat remaining arguments as JSON positional values
  --raw-output0          write NUL delimiters instead of newlines
  --rawfile name file    bind a file's raw contents to a variable
  --slurpfile name file  bind a file's JSON values as an array
  --tab                  use tabs for indentation
`

const jqVersionText = "jq (gbash) backed by gojq v0.12.18\n"

func exitf(inv *commands.Invocation, code int, format string, args ...any) error {
	return commands.Exitf(inv, code, format, args...)
}

func readAllStdin(inv *commands.Invocation) ([]byte, error) {
	data, err := io.ReadAll(inv.Stdin)
	if err != nil {
		return nil, &commands.ExitError{Code: 1, Err: err}
	}
	return data, nil
}

func jqStdinReader(inv *commands.Invocation) io.Reader {
	if inv == nil || inv.Stdin == nil {
		return strings.NewReader("")
	}
	return inv.Stdin
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

	data, err = io.ReadAll(file)
	if err != nil {
		return nil, "", &commands.ExitError{Code: 1, Err: err}
	}
	return data, abs, nil
}

var _ commands.Command = (*JQ)(nil)
