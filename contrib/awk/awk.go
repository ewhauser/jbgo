package awk

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ewhauser/gbash/contrib/awk/goawk/interp"
	"github.com/ewhauser/gbash/contrib/awk/goawk/parser"

	"github.com/ewhauser/gbash/commands"
)

type AWK struct{}

type awkOptions struct {
	fieldSeparator string
	programParts   []awkProgramPart
	vars           []string
	noArgVars      bool
	inputMode      interp.IOMode
	showHelp       bool
	showVersion    bool
	showCopyright  bool
}

type awkProgramPart struct {
	fromFile bool
	value    string
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
	switch {
	case opts.showHelp:
		_, _ = io.WriteString(inv.Stdout, gawkHelpText)
		return nil
	case opts.showVersion:
		_, _ = io.WriteString(inv.Stdout, gawkVersionText)
		return nil
	case opts.showCopyright:
		_, _ = io.WriteString(inv.Stdout, gawkCopyrightText)
		return nil
	}
	programSource, err := loadAWKProgram(ctx, inv, &opts, programText)
	if err != nil {
		return exitCodef(inv, 2, "awk: %v", err)
	}

	funcs := newAWKFuncs(inv)
	compiled, err := parser.ParseProgram([]byte(programSource), &parser.ParserConfig{
		Funcs: funcs,
	})
	if err != nil {
		return exitCodef(inv, 1, "awk: parse error: %v", err)
	}

	stdin := newLazyAWKStdin(ctx, inv)

	config := &interp.Config{
		Stdin:       stdin,
		Output:      inv.Stdout,
		Error:       inv.Stderr,
		Argv0:       "awk",
		Args:        inputs,
		NoArgVars:   opts.noArgVars,
		Vars:        buildAWKVars(&opts),
		Environ:     awkEnviron(inv.Env),
		InputMode:   opts.inputMode,
		FileOpener:  newAWKFileOpener(ctx, inv, stdin),
		FileWriter:  newAWKFileWriter(ctx, inv),
		ShellRunner: newAWKShellRunner(ctx, inv),
		PipeReader:  newAWKPipeReader(ctx, inv),
		PipeWriter:  newAWKPipeWriter(ctx, inv),
		Funcs:       funcs,
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

parseLoop:
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
		case arg == "-h" || arg == "--help":
			opts.showHelp = true
		case arg == "-V" || arg == "--version":
			opts.showVersion = true
		case arg == "-C" || arg == "--copyright":
			opts.showCopyright = true
		case arg == "-F":
			if len(args) < 2 {
				return awkOptions{}, "", nil, exitf(inv, "awk: option requires an argument -- 'F'")
			}
			opts.fieldSeparator = args[1]
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-F") && len(arg) > 2:
			opts.fieldSeparator = arg[2:]
		case arg == "--field-separator":
			if len(args) < 2 {
				return awkOptions{}, "", nil, exitf(inv, "awk: option requires an argument -- 'F'")
			}
			opts.fieldSeparator = args[1]
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "--field-separator="):
			opts.fieldSeparator = arg[len("--field-separator="):]
		case arg == "-f":
			if len(args) < 2 {
				return awkOptions{}, "", nil, exitf(inv, "awk: option requires an argument -- 'f'")
			}
			opts.programParts = append(opts.programParts, awkProgramPart{fromFile: true, value: args[1]})
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-f") && len(arg) > 2:
			opts.programParts = append(opts.programParts, awkProgramPart{fromFile: true, value: arg[2:]})
		case arg == "--file":
			if len(args) < 2 {
				return awkOptions{}, "", nil, exitf(inv, "awk: option requires an argument -- 'f'")
			}
			opts.programParts = append(opts.programParts, awkProgramPart{fromFile: true, value: args[1]})
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "--file="):
			opts.programParts = append(opts.programParts, awkProgramPart{fromFile: true, value: arg[len("--file="):]})
		case arg == "-e":
			if len(args) < 2 {
				return awkOptions{}, "", nil, exitf(inv, "awk: option requires an argument -- 'e'")
			}
			opts.programParts = append(opts.programParts, awkProgramPart{value: args[1]})
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-e") && len(arg) > 2:
			opts.programParts = append(opts.programParts, awkProgramPart{value: arg[2:]})
		case arg == "--source":
			if len(args) < 2 {
				return awkOptions{}, "", nil, exitf(inv, "awk: option requires an argument -- 'e'")
			}
			opts.programParts = append(opts.programParts, awkProgramPart{value: args[1]})
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "--source="):
			opts.programParts = append(opts.programParts, awkProgramPart{value: arg[len("--source="):]})
		case arg == "-i":
			if len(args) < 2 {
				return awkOptions{}, "", nil, exitf(inv, "awk: option requires an argument -- 'i'")
			}
			opts.programParts = append(opts.programParts, awkProgramPart{fromFile: true, value: args[1]})
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-i") && len(arg) > 2:
			opts.programParts = append(opts.programParts, awkProgramPart{fromFile: true, value: arg[2:]})
		case arg == "--include":
			if len(args) < 2 {
				return awkOptions{}, "", nil, exitf(inv, "awk: option requires an argument -- 'i'")
			}
			opts.programParts = append(opts.programParts, awkProgramPart{fromFile: true, value: args[1]})
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "--include="):
			opts.programParts = append(opts.programParts, awkProgramPart{fromFile: true, value: arg[len("--include="):]})
		case arg == "-E":
			if len(args) < 2 {
				return awkOptions{}, "", nil, exitf(inv, "awk: option requires an argument -- 'E'")
			}
			opts.programParts = append(opts.programParts, awkProgramPart{fromFile: true, value: args[1]})
			opts.noArgVars = true
			args = args[2:]
			break parseLoop
		case strings.HasPrefix(arg, "-E") && len(arg) > 2:
			opts.programParts = append(opts.programParts, awkProgramPart{fromFile: true, value: arg[2:]})
			opts.noArgVars = true
			args = args[1:]
			break parseLoop
		case arg == "--exec":
			if len(args) < 2 {
				return awkOptions{}, "", nil, exitf(inv, "awk: option requires an argument -- 'E'")
			}
			opts.programParts = append(opts.programParts, awkProgramPart{fromFile: true, value: args[1]})
			opts.noArgVars = true
			args = args[2:]
			break parseLoop
		case strings.HasPrefix(arg, "--exec="):
			opts.programParts = append(opts.programParts, awkProgramPart{fromFile: true, value: arg[len("--exec="):]})
			opts.noArgVars = true
			args = args[1:]
			break parseLoop
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
		case arg == "--assign":
			if len(args) < 2 {
				return awkOptions{}, "", nil, exitf(inv, "awk: option requires an argument -- 'v'")
			}
			if !strings.Contains(args[1], "=") {
				return awkOptions{}, "", nil, exitf(inv, "awk: expected name=value after -v")
			}
			opts.vars = append(opts.vars, args[1])
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "--assign="):
			value := arg[len("--assign="):]
			if !strings.Contains(value, "=") {
				return awkOptions{}, "", nil, exitf(inv, "awk: expected name=value after -v")
			}
			opts.vars = append(opts.vars, value)
		case arg == "-k" || arg == "--csv":
			opts.inputMode = interp.CSVMode
		case arg == "-b" || arg == "--characters-as-bytes" || arg == "-P" || arg == "--posix" || arg == "-S" || arg == "--sandbox":
			// These flags are compatible with the existing sandboxed wrapper
			// semantics and do not require extra runtime wiring here.
		case arg == "-W":
			if len(args) < 2 {
				return awkOptions{}, "", nil, exitf(inv, "awk: option requires an argument -- 'W'")
			}
			if err := applyAWKWOption(&opts, args[1]); err != nil {
				return awkOptions{}, "", nil, err
			}
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-W") && len(arg) > 2:
			if err := applyAWKWOption(&opts, arg[2:]); err != nil {
				return awkOptions{}, "", nil, err
			}
		case arg == "-l" || strings.HasPrefix(arg, "-l") || arg == "--load" || strings.HasPrefix(arg, "--load="):
			return awkOptions{}, "", nil, exitf(inv, "awk: option %s is not supported in gbash awk", strings.SplitN(arg, "=", 2)[0])
		case arg == "-D" || strings.HasPrefix(arg, "-D") || arg == "--debug" || strings.HasPrefix(arg, "--debug="):
			return awkOptions{}, "", nil, exitf(inv, "awk: option %s is not supported in gbash awk", strings.SplitN(arg, "=", 2)[0])
		default:
			return awkOptions{}, "", nil, exitf(inv, "awk: unsupported flag %s", arg)
		}
		args = args[1:]
	}

	if len(opts.programParts) == 0 && !opts.showHelp && !opts.showVersion && !opts.showCopyright {
		if len(args) == 0 {
			return awkOptions{}, "", nil, exitf(inv, "awk: missing program")
		}
		programText = args[0]
		args = args[1:]
	}
	return opts, programText, args, nil
}

func loadAWKProgram(ctx context.Context, inv *commands.Invocation, opts *awkOptions, programText string) (string, error) {
	if opts == nil {
		return programText, nil
	}
	if len(opts.programParts) == 0 {
		return programText, nil
	}
	var parts []string
	for _, part := range opts.programParts {
		if part.fromFile {
			data, err := readAllFile(ctx, inv, part.value)
			if err != nil {
				return "", err
			}
			parts = append(parts, string(data))
			continue
		}
		parts = append(parts, part.value)
	}
	return strings.Join(parts, "\n"), nil
}

func buildAWKVars(opts *awkOptions) []string {
	if opts == nil {
		return nil
	}
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

func newAWKFileOpener(ctx context.Context, inv *commands.Invocation, stdin io.Reader) func(string) (io.ReadCloser, error) {
	return func(name string) (io.ReadCloser, error) {
		if name == "-" {
			return io.NopCloser(stdin), nil
		}
		file, err := inv.FS.Open(ctx, name)
		if err != nil {
			return nil, err
		}
		return file, nil
	}
}

func newAWKFileWriter(ctx context.Context, inv *commands.Invocation) func(string, bool) (io.WriteCloser, error) {
	return func(name string, appendMode bool) (io.WriteCloser, error) {
		flag := os.O_CREATE | os.O_WRONLY
		if appendMode {
			flag |= os.O_APPEND
		} else {
			flag |= os.O_TRUNC
		}
		return inv.FS.OpenFile(ctx, name, flag, 0o644)
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

func newAWKShellRequest(code string) *commands.ExecutionRequest {
	return &commands.ExecutionRequest{
		Name:         "awk",
		Interpreter:  "sh",
		ShellVariant: commands.ShellVariantSH,
		Script:       code,
	}
}

func newAWKShellRunner(ctx context.Context, inv *commands.Invocation) func(string, io.Reader, io.Writer, io.Writer) (int, error) {
	return func(code string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
		if inv.Exec == nil {
			return -1, fmt.Errorf("awk: shell execution is unavailable")
		}
		req := newAWKShellRequest(code)
		req.Stdin = stdin
		req.Stdout = stdout
		req.Stderr = stderr
		result, err := inv.Exec(ctx, req)
		if err != nil {
			if result != nil {
				return result.ExitCode, nil
			}
			return -1, err
		}
		if result == nil {
			return 0, nil
		}
		return result.ExitCode, nil
	}
}

func newAWKPipeReader(ctx context.Context, inv *commands.Invocation) func(string, io.Reader, io.Writer) (io.ReadCloser, func() (int, error), error) {
	return func(code string, stdin io.Reader, stderr io.Writer) (io.ReadCloser, func() (int, error), error) {
		if inv.Exec == nil {
			return nil, nil, fmt.Errorf("awk: shell execution is unavailable")
		}

		reader, writer := io.Pipe()
		done := make(chan struct {
			code int
			err  error
		}, 1)
		go func() {
			req := newAWKShellRequest(code)
			req.Stdin = stdin
			req.Stdout = writer
			req.Stderr = stderr
			result, err := inv.Exec(ctx, req)
			closeErr := writer.Close()
			if err == nil && closeErr != nil {
				err = closeErr
			}
			exitCode := 0
			if result != nil {
				exitCode = result.ExitCode
			}
			done <- struct {
				code int
				err  error
			}{code: exitCode, err: err}
		}()
		return reader, func() (int, error) {
			result := <-done
			return result.code, result.err
		}, nil
	}
}

func newAWKPipeWriter(ctx context.Context, inv *commands.Invocation) func(string, io.Writer, io.Writer) (io.WriteCloser, func() (int, error), error) {
	return func(code string, stdout, stderr io.Writer) (io.WriteCloser, func() (int, error), error) {
		if inv.Exec == nil {
			return nil, nil, fmt.Errorf("awk: shell execution is unavailable")
		}

		reader, writer := io.Pipe()
		done := make(chan struct {
			code int
			err  error
		}, 1)
		go func() {
			req := newAWKShellRequest(code)
			req.Stdin = reader
			req.Stdout = stdout
			req.Stderr = stderr
			result, err := inv.Exec(ctx, req)
			closeErr := reader.Close()
			if err == nil && closeErr != nil {
				err = closeErr
			}
			exitCode := 0
			if result != nil {
				exitCode = result.ExitCode
			}
			done <- struct {
				code int
				err  error
			}{code: exitCode, err: err}
		}()
		return writer, func() (int, error) {
			result := <-done
			return result.code, result.err
		}, nil
	}
}

func newAWKFuncs(inv *commands.Invocation) map[string]any {
	location := awkTimeLocation(inv)
	return map[string]any{
		"and": func(a, b float64) float64 {
			return float64(awkBitValue(a) & awkBitValue(b))
		},
		"compl": func(v float64) float64 {
			return float64((^awkBitValue(v)) & awkBitMask)
		},
		"gensub": awkGensub,
		"lshift": func(v, shift float64) float64 {
			return float64((awkBitValue(v) << uint(int64(shift))) & awkBitMask)
		},
		"mktime": func(spec string, rest ...float64) float64 {
			return awkMktime(location, spec, rest...)
		},
		"or": func(a, b float64) float64 {
			return float64(awkBitValue(a) | awkBitValue(b))
		},
		"rshift": func(v, shift float64) float64 {
			return float64(awkBitValue(v) >> uint(int64(shift)))
		},
		"strftime": func(format string, rest ...float64) string {
			return awkStrftime(inv, location, format, rest...)
		},
		"strtonum": awkStrToNum,
		"systime": func() float64 {
			return float64(inv.Now().Unix())
		},
		"xor": func(a, b float64) float64 {
			return float64(awkBitValue(a) ^ awkBitValue(b))
		},
	}
}

const awkBitMask uint64 = (1 << 53) - 1

func awkBitValue(v float64) uint64 {
	return uint64(int64(v)) & awkBitMask
}

func awkTimeLocation(inv *commands.Invocation) *time.Location {
	if inv != nil {
		if tz, ok := inv.Env["TZ"]; ok && strings.TrimSpace(tz) != "" {
			if loc, err := time.LoadLocation(tz); err == nil {
				return loc
			}
		}
	}
	return time.Local
}

func awkStrToNum(text string) float64 {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}

	signPrefix := ""
	signMultiplier := 1.0
	if strings.HasPrefix(text, "-") {
		signPrefix = "-"
		signMultiplier = -1
		text = text[1:]
	} else if strings.HasPrefix(text, "+") {
		text = text[1:]
	}

	lower := strings.ToLower(text)
	switch {
	case strings.HasPrefix(lower, "0x"):
		if value, err := strconv.ParseUint(lower[2:], 16, 64); err == nil {
			return signMultiplier * float64(value)
		}
	case len(text) > 1 && text[0] == '0':
		if value, err := strconv.ParseUint(text[1:], 8, 64); err == nil {
			return signMultiplier * float64(value)
		}
	}

	value, err := strconv.ParseFloat(signPrefix+text, 64)
	if err != nil {
		return 0
	}
	return value
}

func awkMktime(location *time.Location, spec string, rest ...float64) float64 {
	fields := strings.Fields(spec)
	if len(fields) < 6 {
		return -1
	}

	values := make([]int, 7)
	for i := range 6 {
		value, err := strconv.Atoi(fields[i])
		if err != nil {
			return -1
		}
		values[i] = value
	}
	if len(fields) > 6 {
		value, err := strconv.Atoi(fields[6])
		if err != nil {
			return -1
		}
		values[6] = value
	} else {
		values[6] = -1
	}

	loc := location
	if len(rest) > 0 && rest[0] != 0 {
		loc = time.UTC
	}
	t := time.Date(values[0], time.Month(values[1]), values[2], values[3], values[4], values[5], 0, loc)
	return float64(t.Unix())
}

func awkStrftime(inv *commands.Invocation, location *time.Location, format string, rest ...float64) string {
	when := inv.Now().In(location)
	if len(rest) > 0 {
		when = time.Unix(int64(rest[0]), 0).In(location)
	}
	if len(rest) > 1 && rest[1] != 0 {
		when = when.UTC()
	}
	return awkStrftimeFormat(format, when)
}

func awkStrftimeFormat(layout string, when time.Time) string {
	var b strings.Builder
	for i := 0; i < len(layout); i++ {
		if layout[i] != '%' || i+1 >= len(layout) {
			b.WriteByte(layout[i])
			continue
		}
		i++
		switch layout[i] {
		case '%':
			b.WriteByte('%')
		case 'a':
			b.WriteString(when.Format("Mon"))
		case 'A':
			b.WriteString(when.Format("Monday"))
		case 'b', 'h':
			b.WriteString(when.Format("Jan"))
		case 'B':
			b.WriteString(when.Format("January"))
		case 'c':
			b.WriteString(when.Format("Mon Jan _2 15:04:05 2006"))
		case 'd':
			fmt.Fprintf(&b, "%02d", when.Day())
		case 'e':
			fmt.Fprintf(&b, "%2d", when.Day())
		case 'F':
			b.WriteString(when.Format("2006-01-02"))
		case 'H':
			fmt.Fprintf(&b, "%02d", when.Hour())
		case 'I':
			hour := when.Hour() % 12
			if hour == 0 {
				hour = 12
			}
			fmt.Fprintf(&b, "%02d", hour)
		case 'j':
			fmt.Fprintf(&b, "%03d", when.YearDay())
		case 'm':
			fmt.Fprintf(&b, "%02d", int(when.Month()))
		case 'M':
			fmt.Fprintf(&b, "%02d", when.Minute())
		case 'p':
			b.WriteString(when.Format("PM"))
		case 'R':
			b.WriteString(when.Format("15:04"))
		case 'S':
			fmt.Fprintf(&b, "%02d", when.Second())
		case 's':
			b.WriteString(strconv.FormatInt(when.Unix(), 10))
		case 'T':
			b.WriteString(when.Format("15:04:05"))
		case 'u':
			weekday := int(when.Weekday())
			if weekday == 0 {
				weekday = 7
			}
			b.WriteString(strconv.Itoa(weekday))
		case 'w':
			b.WriteString(strconv.Itoa(int(when.Weekday())))
		case 'x':
			b.WriteString(when.Format("01/02/06"))
		case 'X':
			b.WriteString(when.Format("15:04:05"))
		case 'Y':
			fmt.Fprintf(&b, "%04d", when.Year())
		case 'y':
			fmt.Fprintf(&b, "%02d", when.Year()%100)
		case 'z':
			b.WriteString(when.Format("-0700"))
		case 'Z':
			b.WriteString(when.Format("MST"))
		default:
			b.WriteByte('%')
			b.WriteByte(layout[i])
		}
	}
	return b.String()
}

func awkGensub(regex, replacement, how string, rest ...string) (string, error) {
	target := ""
	if len(rest) > 0 {
		target = rest[0]
	}
	re, err := regexp.Compile(regex)
	if err != nil {
		return "", err
	}
	matches := re.FindAllStringSubmatchIndex(target, -1)
	if len(matches) == 0 {
		return target, nil
	}

	global := strings.EqualFold(how, "g")
	targetMatch := 1
	if !global {
		if how != "" {
			n, err := strconv.Atoi(how)
			if err != nil || n <= 0 {
				return "", fmt.Errorf("gensub: invalid replacement selector %q", how)
			}
			targetMatch = n
		}
	}

	var out strings.Builder
	last := 0
	replaced := 0
	for _, match := range matches {
		replaced++
		if !global && replaced != targetMatch {
			continue
		}
		start, end := match[0], match[1]
		out.WriteString(target[last:start])
		out.WriteString(awkExpandGensubReplacement(replacement, target, match))
		last = end
		if !global {
			break
		}
	}
	if !global && replaced < targetMatch {
		return target, nil
	}
	out.WriteString(target[last:])
	return out.String(), nil
}

func awkExpandGensubReplacement(replacement, target string, match []int) string {
	var out strings.Builder
	for i := 0; i < len(replacement); i++ {
		switch replacement[i] {
		case '&':
			out.WriteString(target[match[0]:match[1]])
		case '\\':
			i++
			if i >= len(replacement) {
				out.WriteByte('\\')
				break
			}
			if replacement[i] >= '0' && replacement[i] <= '9' {
				group := int(replacement[i] - '0')
				index := group * 2
				if index+1 < len(match) && match[index] >= 0 && match[index+1] >= 0 {
					out.WriteString(target[match[index]:match[index+1]])
				}
				break
			}
			out.WriteByte(replacement[i])
		default:
			out.WriteByte(replacement[i])
		}
	}
	return out.String()
}

func applyAWKWOption(opts *awkOptions, value string) error {
	switch value {
	case "help":
		opts.showHelp = true
	case "version":
		opts.showVersion = true
	case "copyright":
		opts.showCopyright = true
	case "posix", "sandbox", "compat":
		// Accepted for GNU-compat CLI handling, but the current wrapper does
		// not need extra runtime changes for them beyond existing behavior.
	default:
		return fmt.Errorf("awk: unsupported -W option %s", value)
	}
	return nil
}

const gawkVersionText = "" +
	"GNU Awk 5.3.2, API 4.0, PMA Avon 8-g1\n" +
	"Copyright (C) 1989, 1991-2025 Free Software Foundation.\n" +
	"\n" +
	"This program is free software; you can redistribute it and/or modify\n" +
	"it under the terms of the GNU General Public License as published by\n" +
	"the Free Software Foundation; either version 3 of the License, or\n" +
	"(at your option) any later version.\n" +
	"\n" +
	"This program is distributed in the hope that it will be useful,\n" +
	"but WITHOUT ANY WARRANTY; without even the implied warranty of\n" +
	"MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the\n" +
	"GNU General Public License for more details.\n" +
	"\n" +
	"You should have received a copy of the GNU General Public License\n" +
	"along with this program. If not, see http://www.gnu.org/licenses/.\n"

const gawkCopyrightText = "" +
	"Copyright (C) 1989, 1991-2025 Free Software Foundation.\n" +
	"\n" +
	"This program is free software; you can redistribute it and/or modify\n" +
	"it under the terms of the GNU General Public License as published by\n" +
	"the Free Software Foundation; either version 3 of the License, or\n" +
	"(at your option) any later version.\n" +
	"\n" +
	"This program is distributed in the hope that it will be useful,\n" +
	"but WITHOUT ANY WARRANTY; without even the implied warranty of\n" +
	"MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the\n" +
	"GNU General Public License for more details.\n" +
	"\n" +
	"You should have received a copy of the GNU General Public License\n" +
	"along with this program. If not, see http://www.gnu.org/licenses/.\n"

const gawkHelpText = "" +
	"Usage: awk [POSIX or GNU style options] -f progfile [--] file ...\n" +
	"Usage: awk [POSIX or GNU style options] [--] 'program' file ...\n" +
	"POSIX options:\t\tGNU long options: (standard)\n" +
	"\t-f progfile\t\t--file=progfile\n" +
	"\t-F fs\t\t\t--field-separator=fs\n" +
	"\t-v var=val\t\t--assign=var=val\n" +
	"Short options:\t\tGNU long options: (extensions)\n" +
	"\t-b\t\t\t--characters-as-bytes\n" +
	"\t-c\t\t\t--traditional\n" +
	"\t-C\t\t\t--copyright\n" +
	"\t-d[file]\t\t--dump-variables[=file]\n" +
	"\t-D[file]\t\t--debug[=file]\n" +
	"\t-e 'program-text'\t--source='program-text'\n" +
	"\t-E file\t\t\t--exec=file\n" +
	"\t-g\t\t\t--gen-pot\n" +
	"\t-h\t\t\t--help\n" +
	"\t-i includefile\t\t--include=includefile\n" +
	"\t-I\t\t\t--trace\n" +
	"\t-k\t\t\t--csv\n" +
	"\t-l library\t\t--load=library\n" +
	"\t-L[fatal|invalid|no-ext]\t--lint[=fatal|invalid|no-ext]\n" +
	"\t-M\t\t\t--bignum\n" +
	"\t-N\t\t\t--use-lc-numeric\n" +
	"\t-n\t\t\t--non-decimal-data\n" +
	"\t-o[file]\t\t--pretty-print[=file]\n" +
	"\t-O\t\t\t--optimize\n" +
	"\t-p[file]\t\t--profile[=file]\n" +
	"\t-P\t\t\t--posix\n" +
	"\t-r\t\t\t--re-interval\n" +
	"\t-s\t\t\t--no-optimize\n" +
	"\t-S\t\t\t--sandbox\n" +
	"\t-t\t\t\t--lint-old\n" +
	"\t-V\t\t\t--version\n" +
	"\n" +
	"To report bugs, use the `gawkbug' program.\n" +
	"For full instructions, see the node `Bugs' in `gawk.info'\n" +
	"which is section `Reporting Problems and Bugs' in the\n" +
	"printed version.  This same information may be found at\n" +
	"https://www.gnu.org/software/gawk/manual/html_node/Bugs.html.\n" +
	"PLEASE do NOT try to report bugs by posting in comp.lang.awk,\n" +
	"or by using a web forum such as Stack Overflow.\n" +
	"\n" +
	"Source code for gawk may be obtained from\n" +
	"https://ftp.gnu.org/gnu/gawk/gawk-5.3.2.tar.gz\n" +
	"\n" +
	"gawk is a pattern scanning and processing language.\n" +
	"By default it reads standard input and writes standard output.\n" +
	"\n" +
	"Examples:\n" +
	"\tawk '{ sum += $1 }; END { print sum }' file\n" +
	"\tawk -F: '{ print $1 }' /etc/passwd\n"

func exitf(inv *commands.Invocation, format string, args ...any) error {
	return exitCodef(inv, 2, format, args...)
}

func exitCodef(inv *commands.Invocation, code int, format string, args ...any) error {
	return commands.Exitf(inv, code, format, args...)
}

var _ commands.Command = (*AWK)(nil)
