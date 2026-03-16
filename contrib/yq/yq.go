package yq

import (
	"bytes"
	"container/list"
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/ewhauser/gbash/commands"
	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/mikefarah/yq/v4/pkg/yqlib"
	logging "gopkg.in/op/go-logging.v1"
)

type YQ struct{}

type yqMode int

const (
	yqModeEval yqMode = iota
	yqModeEvalAll
)

type yqOptions struct {
	mode            yqMode
	help            bool
	version         bool
	nullInput       bool
	exitStatus      bool
	inPlace         bool
	prettyPrint     bool
	noDocSeparators bool
	nulOutput       bool
	inputFormat     string
	outputFormat    string
	indent          int
	unwrapScalar    *bool
	expressionFile  string
}

type yqFormatConfig struct {
	inputFormat  *yqlib.Format
	outputFormat *yqlib.Format
	unwrapScalar bool
}

type namedInput struct {
	Name      string
	Abs       string
	Data      []byte
	FromStdin bool
}

var yqEvalMu sync.Mutex

func init() {
	backend := logging.AddModuleLevel(logging.NewLogBackend(io.Discard, "", 0))
	backend.SetLevel(logging.ERROR, "")
	logging.SetBackend(backend)
}

func NewYQ() *YQ {
	return &YQ{}
}

func Register(registry commands.CommandRegistry) error {
	if registry == nil {
		return nil
	}
	return registry.Register(NewYQ())
}

func (c *YQ) Name() string {
	return "yq"
}

func (c *YQ) Run(ctx context.Context, inv *commands.Invocation) error {
	opts, expression, inputs, err := parseYQArgs(inv)
	if err != nil {
		return err
	}
	if opts.help {
		_, _ = io.WriteString(inv.Stdout, yqHelpText)
		return nil
	}
	if opts.version {
		_, _ = io.WriteString(inv.Stdout, yqVersionText)
		return nil
	}

	expression, err = loadYQExpression(ctx, inv, &opts, expression)
	if err != nil {
		return err
	}
	expression = processYQExpression(expression, opts.prettyPrint)

	if opts.nullInput && len(inputs) > 0 {
		return exitf(inv, 1, "yq: cannot pass files in when using null-input flag")
	}
	if opts.inPlace && (len(inputs) == 0 || inputs[0] == "-") {
		return exitf(inv, 1, "yq: write in place flag only applicable when giving an expression and at least one file")
	}

	formats, err := resolveYQFormats(&opts, inputs)
	if err != nil {
		return exitf(inv, 1, "yq: %v", err)
	}

	namedInputs, err := readNamedInputs(ctx, inv, inputs, !opts.nullInput)
	if err != nil {
		return err
	}

	var output bytes.Buffer
	writer := inv.Stdout
	if opts.inPlace {
		writer = &output
	}

	printed, err := executeYQ(ctx, inv, &opts, expression, namedInputs, formats, writer)
	if err != nil {
		return err
	}
	if opts.exitStatus && !printed {
		return exitf(inv, 1, "yq: no matches found")
	}
	if opts.inPlace {
		info, err := inv.FS.Stat(ctx, namedInputs[0].Abs)
		if err != nil {
			return &commands.ExitError{Code: 1, Err: err}
		}
		if err := writeFileContents(ctx, inv, namedInputs[0].Abs, output.Bytes(), info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

func parseYQArgs(inv *commands.Invocation) (opts yqOptions, expression string, inputs []string, err error) {
	opts = yqOptions{
		mode:         yqModeEval,
		inputFormat:  "auto",
		outputFormat: "auto",
		indent:       2,
	}
	args := inv.Args
	if len(args) > 0 {
		switch args[0] {
		case "eval", "e":
			args = args[1:]
		case "eval-all", "ea":
			opts.mode = yqModeEvalAll
			args = args[1:]
		}
	}

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
			args, err = parseYQLongFlag(inv, &opts, args)
			if err != nil {
				return yqOptions{}, "", nil, err
			}
			continue
		}
		args, err = parseYQShortFlags(inv, &opts, args)
		if err != nil {
			return yqOptions{}, "", nil, err
		}
	}

	if opts.help || opts.version {
		return opts, "", nil, nil
	}

	expression, inputs, err = classifyYQArgs(inv, &opts, args)
	if err != nil {
		return yqOptions{}, "", nil, err
	}
	return opts, expression, inputs, nil
}

func parseYQLongFlag(inv *commands.Invocation, opts *yqOptions, args []string) ([]string, error) {
	arg := args[0]
	name, value, hasValue := splitYQLongFlag(arg)

	switch name {
	case "help":
		opts.help = true
	case "version":
		opts.version = true
	case "null-input":
		opts.nullInput = true
	case "exit-status":
		opts.exitStatus = true
	case "inplace":
		opts.inPlace = true
	case "prettyPrint":
		opts.prettyPrint = true
	case "no-doc":
		opts.noDocSeparators = true
	case "nul-output":
		opts.nulOutput = true
	case "input-format":
		val, rest, err := parseYQValue(inv, arg, value, hasValue, args[1:])
		if err != nil {
			return nil, err
		}
		opts.inputFormat = normalizeYQFormat(val)
		return rest, nil
	case "output-format":
		val, rest, err := parseYQValue(inv, arg, value, hasValue, args[1:])
		if err != nil {
			return nil, err
		}
		opts.outputFormat = normalizeYQFormat(val)
		return rest, nil
	case "from-file":
		val, rest, err := parseYQValue(inv, arg, value, hasValue, args[1:])
		if err != nil {
			return nil, err
		}
		opts.expressionFile = val
		return rest, nil
	case "indent":
		val, rest, err := parseYQValue(inv, arg, value, hasValue, args[1:])
		if err != nil {
			return nil, err
		}
		indent, err := parseYQPositiveInt(val)
		if err != nil {
			return nil, exitf(inv, 1, "yq: invalid argument for %s: %v", arg, err)
		}
		opts.indent = indent
		return rest, nil
	case "unwrapScalar":
		if !hasValue {
			value = "true"
		}
		unwrap, err := parseBoolValue(value)
		if err != nil {
			return nil, exitf(inv, 1, "yq: invalid argument for %s: %v", arg, err)
		}
		opts.unwrapScalar = &unwrap
	default:
		return nil, exitf(inv, 1, "yq: unrecognized option %q", arg)
	}
	return args[1:], nil
}

func parseYQShortFlags(inv *commands.Invocation, opts *yqOptions, args []string) ([]string, error) {
	arg := args[0]
	for idx := 1; idx < len(arg); idx++ {
		flag := arg[idx]
		switch flag {
		case 'P':
			opts.prettyPrint = true
		case 'n':
			opts.nullInput = true
		case 'e':
			opts.exitStatus = true
		case 'i':
			opts.inPlace = true
		case 'N':
			opts.noDocSeparators = true
		case '0':
			opts.nulOutput = true
		case 'V':
			opts.version = true
		case 'j':
			opts.outputFormat = "json"
		case 'r':
			val := true
			opts.unwrapScalar = &val
		case 'p', 'o', 'I':
			inline := arg[idx+1:]
			if inline == "" {
				if len(args) < 2 {
					return nil, exitf(inv, 1, "yq: expected argument for -%c", flag)
				}
				inline = args[1]
				args = append(args[:1], args[2:]...)
			}
			switch flag {
			case 'p':
				opts.inputFormat = normalizeYQFormat(inline)
			case 'o':
				opts.outputFormat = normalizeYQFormat(inline)
			case 'I':
				indent, err := parseYQPositiveInt(inline)
				if err != nil {
					return nil, exitf(inv, 1, "yq: invalid argument for -I: %v", err)
				}
				opts.indent = indent
			}
			return args[1:], nil
		default:
			return nil, exitf(inv, 1, "yq: unsupported flag -%c", flag)
		}
	}
	return args[1:], nil
}

func classifyYQArgs(inv *commands.Invocation, opts *yqOptions, args []string) (expression string, inputs []string, err error) {
	if opts.expressionFile != "" {
		if len(args) == 0 {
			return ".", nil, nil
		}
		if !opts.nullInput && len(args) == 1 && yqLooksLikeInput(inv, args[0]) {
			return ".", args, nil
		}
		return ".", args, nil
	}

	switch len(args) {
	case 0:
		return ".", nil, nil
	case 1:
		if !opts.nullInput && yqLooksLikeInput(inv, args[0]) {
			return ".", args, nil
		}
		return args[0], nil, nil
	default:
		return args[0], args[1:], nil
	}
}

func yqLooksLikeInput(inv *commands.Invocation, token string) bool {
	if token == "-" {
		return true
	}
	if strings.HasPrefix(token, "-") && token != "./-" && token != "../-" {
		return false
	}

	knownExt := map[string]struct{}{
		".yml":        {},
		".yaml":       {},
		".json":       {},
		".toml":       {},
		".xml":        {},
		".props":      {},
		".properties": {},
		".ini":        {},
		".csv":        {},
		".tsv":        {},
	}
	if _, ok := knownExt[strings.ToLower(filepath.Ext(token))]; ok {
		return true
	}
	if inv == nil || inv.FS == nil {
		return false
	}

	info, err := inv.FS.Stat(context.Background(), inv.FS.Resolve(token))
	return err == nil && !info.IsDir()
}

func loadYQExpression(ctx context.Context, inv *commands.Invocation, opts *yqOptions, expression string) (string, error) {
	if opts.expressionFile == "" {
		return expression, nil
	}
	data, _, err := readAllFile(ctx, inv, opts.expressionFile)
	if err != nil {
		return "", err
	}
	normalized := strings.ReplaceAll(string(data), "\r\n", "\n")
	return strings.TrimRight(normalized, "\r\n"), nil
}

func processYQExpression(expression string, pretty bool) string {
	if !pretty {
		return expression
	}
	if expression == "" || expression == "." {
		return yqlib.PrettyPrintExp
	}
	return fmt.Sprintf("%s | %s", expression, yqlib.PrettyPrintExp)
}

func resolveYQFormats(opts *yqOptions, inputs []string) (*yqFormatConfig, error) {
	inputExplicit := opts.inputFormat != "" && opts.inputFormat != "auto" && opts.inputFormat != "a"
	inputName := opts.inputFormat
	if inputName == "" || inputName == "auto" || inputName == "a" {
		inputName = yqlib.FormatStringFromFilename(firstYQInputName(inputs))
	}
	outputName := opts.outputFormat
	if outputName == "" || outputName == "auto" || outputName == "a" {
		if inputExplicit {
			outputName = "yaml"
		} else {
			outputName = inputName
		}
	}

	inputFormat, err := yqlib.FormatFromString(inputName)
	if err != nil {
		return nil, err
	}
	if inputFormat.DecoderFactory == nil {
		return nil, fmt.Errorf("no support for %s input format", inputName)
	}
	outputFormat, err := yqlib.FormatFromString(outputName)
	if err != nil {
		return nil, err
	}
	if outputFormat.EncoderFactory == nil {
		return nil, fmt.Errorf("no support for %s output format", outputName)
	}

	unwrap := outputFormat == yqlib.YamlFormat || outputFormat == yqlib.PropertiesFormat
	if opts.unwrapScalar != nil {
		unwrap = *opts.unwrapScalar
	}

	return &yqFormatConfig{
		inputFormat:  inputFormat,
		outputFormat: outputFormat,
		unwrapScalar: unwrap,
	}, nil
}

func executeYQ(ctx context.Context, inv *commands.Invocation, opts *yqOptions, expression string, inputs []namedInput, formats *yqFormatConfig, out io.Writer) (bool, error) {
	encoder, err := newYQEncoder(formats.outputFormat, opts, formats.unwrapScalar)
	if err != nil {
		return false, exitf(inv, 1, "yq: %v", err)
	}
	printer := yqlib.NewPrinter(encoder, yqlib.NewSinglePrinterWriter(out))
	if opts.nulOutput {
		printer.SetNulSepOutput(true)
	}

	err = withYQSandbox(func() error {
		switch opts.mode {
		case yqModeEvalAll:
			return runYQEvalAll(ctx, expression, inputs, printer, formats.inputFormat)
		default:
			return runYQEval(ctx, expression, inputs, printer, formats.inputFormat, opts.nullInput)
		}
	})
	if err != nil {
		return false, normalizeYQError(inv, err)
	}
	return printer.PrintedAnything(), nil
}

func withYQSandbox(run func() error) error {
	yqEvalMu.Lock()
	defer yqEvalMu.Unlock()

	yqlib.InitExpressionParser()
	security := yqlib.ConfiguredSecurityPreferences
	yqlib.ConfiguredSecurityPreferences.DisableEnvOps = true
	yqlib.ConfiguredSecurityPreferences.DisableFileOps = true
	defer func() {
		yqlib.ConfiguredSecurityPreferences = security
	}()

	return run()
}


func runYQEval(ctx context.Context, expression string, inputs []namedInput, printer yqlib.Printer, inputFormat *yqlib.Format, nullInput bool) error {
	stream := yqlib.NewStreamEvaluator()
	if nullInput {
		return stream.EvaluateNew(expression, printer)
	}

	node, err := yqlib.ExpressionParser.ParseExpression(expression)
	if err != nil {
		return err
	}

	var totalDocs uint
	for _, input := range inputs {
		if err := ctx.Err(); err != nil {
			return err
		}
		decoder, err := newYQDecoder(inputFormat)
		if err != nil {
			return err
		}
		processed, err := stream.Evaluate(input.Abs, bytes.NewReader(input.Data), node, printer, decoder)
		if err != nil {
			return err
		}
		totalDocs += processed
	}
	if totalDocs == 0 {
		return stream.EvaluateNew(expression, printer)
	}
	return nil
}

func runYQEvalAll(ctx context.Context, expression string, inputs []namedInput, printer yqlib.Printer, inputFormat *yqlib.Format) error {
	if len(inputs) == 0 {
		return yqlib.NewStreamEvaluator().EvaluateNew(expression, printer)
	}

	candidates := list.New()
	for index, input := range inputs {
		if err := ctx.Err(); err != nil {
			return err
		}
		decoder, err := newYQDecoder(inputFormat)
		if err != nil {
			return err
		}
		documents, err := yqlib.ReadDocuments(bytes.NewReader(input.Data), decoder)
		if err != nil {
			return err
		}
		for elem := documents.Front(); elem != nil; elem = elem.Next() {
			node := elem.Value.(*yqlib.CandidateNode)
			node.SetFilename(input.Abs)
			node.SetFileIndex(index)
		}
		candidates.PushBackList(documents)
	}
	if candidates.Len() == 0 {
		candidates.PushBack(&yqlib.CandidateNode{
			Kind: yqlib.ScalarNode,
			Tag:  "!!null",
		})
	}

	matches, err := yqlib.NewAllAtOnceEvaluator().EvaluateCandidateNodes(expression, candidates)
	if err != nil {
		return err
	}
	return printer.PrintResults(matches)
}

func newYQDecoder(format *yqlib.Format) (yqlib.Decoder, error) {
	switch format {
	case yqlib.YamlFormat:
		prefs := yqlib.ConfiguredYamlPreferences.Copy()
		return yqlib.NewYamlDecoder(prefs), nil
	case yqlib.KYamlFormat:
		prefs := yqlib.ConfiguredYamlPreferences.Copy()
		return yqlib.NewYamlDecoder(prefs), nil
	case yqlib.JSONFormat:
		return yqlib.NewJSONDecoder(), nil
	default:
		decoder := format.DecoderFactory()
		if decoder == nil {
			return nil, fmt.Errorf("no support for %s input format", format.FormalName)
		}
		return decoder, nil
	}
}

func newYQEncoder(format *yqlib.Format, opts *yqOptions, unwrap bool) (yqlib.Encoder, error) {
	switch format {
	case yqlib.YamlFormat:
		prefs := yqlib.ConfiguredYamlPreferences.Copy()
		prefs.Indent = opts.indent
		prefs.UnwrapScalar = unwrap
		prefs.ColorsEnabled = false
		prefs.PrintDocSeparators = !opts.noDocSeparators
		return yqlib.NewYamlEncoder(prefs), nil
	case yqlib.KYamlFormat:
		prefs := yqlib.ConfiguredKYamlPreferences.Copy()
		prefs.Indent = opts.indent
		prefs.UnwrapScalar = unwrap
		prefs.ColorsEnabled = false
		prefs.PrintDocSeparators = !opts.noDocSeparators
		return yqlib.NewKYamlEncoder(prefs), nil
	case yqlib.JSONFormat:
		prefs := yqlib.ConfiguredJSONPreferences.Copy()
		prefs.Indent = opts.indent
		prefs.UnwrapScalar = unwrap
		prefs.ColorsEnabled = false
		return yqlib.NewJSONEncoder(prefs), nil
	default:
		encoder := format.EncoderFactory()
		if encoder == nil {
			return nil, fmt.Errorf("no support for %s output format", format.FormalName)
		}
		return encoder, nil
	}
}

func normalizeYQError(inv *commands.Invocation, err error) error {
	if err == nil {
		return nil
	}
	if _, ok := commands.ExitCode(err); ok {
		return err
	}
	return exitf(inv, 1, "yq: %v", err)
}

func splitYQLongFlag(arg string) (name, value string, hasValue bool) {
	raw := strings.TrimPrefix(arg, "--")
	before, after, found := strings.Cut(raw, "=")
	return before, after, found
}

func parseYQValue(inv *commands.Invocation, arg, inline string, hasValue bool, rest []string) (value string, remaining []string, err error) {
	if hasValue {
		return inline, rest, nil
	}
	if len(rest) == 0 {
		return "", nil, exitf(inv, 1, "yq: expected argument for %s", arg)
	}
	return rest[0], rest[1:], nil
}

func parseYQPositiveInt(raw string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	if value < 0 {
		return 0, fmt.Errorf("negative value %d", value)
	}
	return value, nil
}

func parseBoolValue(raw string) (bool, error) {
	switch strings.ToLower(raw) {
	case "1", "t", "true", "y", "yes", "on":
		return true, nil
	case "0", "f", "false", "n", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean %q", raw)
	}
}

func firstYQInputName(inputs []string) string {
	for _, input := range inputs {
		if input != "-" {
			return input
		}
	}
	return ""
}

func normalizeYQFormat(format string) string {
	if format == "" {
		return "auto"
	}
	if format == "a" {
		return "auto"
	}
	return format
}

func exitf(inv *commands.Invocation, code int, format string, args ...any) error {
	return commands.Exitf(inv, code, format, args...)
}

func readAllFile(ctx context.Context, inv *commands.Invocation, name string) (data []byte, abs string, err error) {
	file, abs, err := openRead(ctx, inv, name)
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

func readAllStdin(ctx context.Context, inv *commands.Invocation) ([]byte, error) {
	data, err := commands.ReadAllStdin(ctx, inv)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func readNamedInputs(ctx context.Context, inv *commands.Invocation, names []string, defaultStdin bool) ([]namedInput, error) {
	if len(names) == 0 {
		if !defaultStdin {
			return nil, nil
		}
		data, err := readAllStdin(ctx, inv)
		if err != nil {
			return nil, err
		}
		return []namedInput{{
			Name:      "-",
			Abs:       "-",
			Data:      data,
			FromStdin: true,
		}}, nil
	}

	var (
		inputs      []namedInput
		stdinData   []byte
		stdinLoaded bool
	)
	for _, name := range names {
		if name == "-" {
			if !stdinLoaded {
				data, err := readAllStdin(ctx, inv)
				if err != nil {
					return nil, err
				}
				stdinData = data
				stdinLoaded = true
			}
			inputs = append(inputs, namedInput{
				Name:      "-",
				Abs:       "-",
				Data:      stdinData,
				FromStdin: true,
			})
			continue
		}
		data, abs, err := readAllFile(ctx, inv, name)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, namedInput{
			Name: path.Base(abs),
			Abs:  abs,
			Data: data,
		})
	}
	return inputs, nil
}

func statMaybe(ctx context.Context, inv *commands.Invocation, name string) (info stdfs.FileInfo, abs string, exists bool, err error) {
	abs = name
	if inv != nil && inv.FS != nil {
		abs = inv.FS.Resolve(name)
	}
	info, err = inv.FS.Stat(ctx, abs)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			return nil, abs, false, nil
		}
		return nil, "", false, err
	}
	return info, abs, true, nil
}

func openRead(ctx context.Context, inv *commands.Invocation, name string) (gbfs.File, string, error) {
	abs := name
	if inv != nil && inv.FS != nil {
		abs = inv.FS.Resolve(name)
	}
	file, err := inv.FS.Open(ctx, abs)
	if err != nil {
		return nil, "", err
	}
	return file, abs, nil
}

func ensureParentDirExists(ctx context.Context, inv *commands.Invocation, targetAbs string) error {
	parent := path.Dir(targetAbs)
	info, _, exists, err := statMaybe(ctx, inv, parent)
	if err != nil {
		return err
	}
	if !exists {
		return &commands.ExitError{
			Code: 1,
			Err:  fmt.Errorf("%s: No such file or directory", parent),
		}
	}
	if !info.IsDir() {
		return &commands.ExitError{
			Code: 1,
			Err:  fmt.Errorf("%s: Not a directory", parent),
		}
	}
	return nil
}

func writeFileContents(ctx context.Context, inv *commands.Invocation, targetAbs string, data []byte, perm stdfs.FileMode) error {
	if err := ensureParentDirExists(ctx, inv, targetAbs); err != nil {
		return err
	}

	file, err := inv.FS.OpenFile(ctx, targetAbs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return &commands.ExitError{Code: 1, Err: err}
	}
	defer func() { _ = file.Close() }()

	if _, err := file.Write(data); err != nil {
		return &commands.ExitError{Code: 1, Err: err}
	}
	return nil
}

const yqHelpText = `yq - query YAML and JSON values inside the gbash sandbox

Usage:
  yq [eval|e|eval-all|ea] [options] [expression] [file ...]

Supported options:
  -p, --input-format FORMAT   input format (auto, yaml, json, ...)
  -o, --output-format FORMAT  output format (auto, yaml, json, ...)
  -j                          shorthand for -o=json
  -P, --prettyPrint           apply yq pretty-print styling
  -n, --null-input            evaluate without reading input files or stdin
  -e, --exit-status           return exit 1 if no matches or only null/false results
  -I, --indent N              output indentation
  -i, --inplace               write the result back to the first file input
  -N, --no-doc                suppress YAML document separators
  -0, --nul-output            separate results with NUL bytes
  -r, --unwrapScalar          unwrap scalar output
      --unwrapScalar=BOOL     explicitly enable or disable scalar unwrapping
      --from-file PATH        read the expression from a file
      --help                  show this help
  -V, --version               show version

Notes:
  - The wrapper is sandboxed: file reads and writes go through the virtual filesystem.
  - yqlib env and file operators are disabled; use explicit command arguments instead.
`

const yqVersionText = "yq (gbash) backed by mikefarah/yq v4.52.4\n"

var _ commands.Command = (*YQ)(nil)
