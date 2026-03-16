package builtins

import (
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type Stat struct{}

type statOptions struct {
	format      string
	printf      bool
	dereference bool
}

func NewStat() *Stat {
	return &Stat{}
}

func (c *Stat) Name() string {
	return "stat"
}

func (c *Stat) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Stat) Spec() CommandSpec {
	return CommandSpec{
		Name:  "stat",
		About: "Display file or file system status.",
		Usage: "stat [OPTION]... FILE...",
		Options: []OptionSpec{
			{Name: "dereference", Short: 'L', Long: "dereference", Help: "follow links"},
			{Name: "format", Short: 'c', Long: "format", Arity: OptionRequiredValue, ValueName: "FORMAT", Help: "use the specified FORMAT instead of the default"},
			{Name: "printf", Long: "printf", Arity: OptionRequiredValue, ValueName: "FORMAT", Help: "like --format, but interpret backslash escapes, and do not output a mandatory trailing newline"},
			{Name: "filesystem", Short: 'f', Long: "file-system", Help: "display file system status instead of file status"},
		},
		Args: []ArgSpec{{Name: "file", ValueName: "FILE", Repeatable: true, Required: true}},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
	}
}

func (c *Stat) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := parseStatMatches(inv, matches)
	if err != nil {
		return err
	}
	if matches.Has("filesystem") {
		return runStatFilesystemMode(ctx, inv, opts, matches.Args("file"))
	}
	files := matches.Args("file")
	exitCode := 0
	for _, name := range files {
		info, abs, err := statPathForOptions(ctx, inv, name, opts)
		if err != nil {
			var exitErr *ExitError
			if errors.As(err, &exitErr) && errors.Is(exitErr.Err, stdfs.ErrNotExist) {
				_, _ = fmt.Fprintf(inv.Stderr, "stat: cannot stat %q: No such file or directory\n", name)
				exitCode = 1
				continue
			}
			_, _ = fmt.Fprintf(inv.Stderr, "stat: cannot stat %q: %v\n", name, err)
			exitCode = 1
			continue
		}
		output, err := renderStatOutput(ctx, inv, abs, info, opts)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprint(inv.Stdout, output); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func parseStatMatches(inv *Invocation, matches *ParsedCommand) (statOptions, error) {
	opts := statOptions{dereference: matches.Has("dereference")}
	if matches.Has("format") && matches.Has("printf") {
		return statOptions{}, exitf(inv, 1, "stat: cannot specify both format and printf")
	}
	if matches.Has("format") {
		opts.format = matches.Value("format")
	}
	if matches.Has("printf") {
		opts.printf = true
		opts.format = matches.Value("printf")
	}
	return opts, nil
}

func statPathForOptions(ctx context.Context, inv *Invocation, name string, opts statOptions) (stdfs.FileInfo, string, error) {
	if opts.dereference || hasTrailingSlash(name) {
		return statPath(ctx, inv, name)
	}
	return lstatPath(ctx, inv, name)
}

func hasTrailingSlash(name string) bool {
	return len(name) > 1 && strings.HasSuffix(name, "/")
}

func renderStatOutput(ctx context.Context, inv *Invocation, abs string, info stdfs.FileInfo, opts statOptions) (string, error) {
	if opts.format == "" {
		return defaultStatOutput(abs, info), nil
	}
	format := opts.format
	if opts.printf {
		decoded, err := decodeStatEscapes(format)
		if err != nil {
			return "", err
		}
		format = decoded
	}
	rendered, err := renderStatFormat(ctx, inv, abs, info, format)
	if err != nil {
		return "", &ExitError{Code: 1, Err: err}
	}
	if opts.printf {
		return rendered, nil
	}
	return rendered + "\n", nil
}

func defaultStatOutput(abs string, info stdfs.FileInfo) string {
	return fmt.Sprintf(
		"  File: %s\n  Size: %d\n  Type: %s\n  Mode: (%s/%s)\n",
		abs,
		info.Size(),
		fileTypeName(info),
		formatModeOctal(info.Mode()),
		formatModeLong(info.Mode()),
	)
}

func renderStatFormat(ctx context.Context, inv *Invocation, abs string, info stdfs.FileInfo, format string) (string, error) {
	identities := loadPermissionIdentityDB(ctx, inv)
	owner := permissionLookupOwnership(identities, info)
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i == len(format)-1 {
			b.WriteByte(format[i])
			continue
		}
		i++
		if format[i] == '%' {
			b.WriteByte('%')
			continue
		}
		start := i
		leftAlign, zeroPad, width, precision, directive := parseStatDirective(format, &i)
		value, err := statDirectiveValue(ctx, inv, abs, info, owner, directive, precision)
		if err != nil {
			return "", err
		}
		if precision >= 0 && directive == 'Y' && strings.Contains(value, ".") {
			value = trimStatFloatPrecision(value, precision)
		}
		if width > 0 {
			pad := " "
			if zeroPad && !leftAlign {
				pad = "0"
			}
			if leftAlign {
				value += strings.Repeat(pad, max(0, width-len(value)))
			} else {
				value = strings.Repeat(pad, max(0, width-len(value))) + value
			}
		}
		if start == i && value == "" {
			return "", fmt.Errorf("unsupported format sequence %%%c", directive)
		}
		b.WriteString(value)
	}
	return b.String(), nil
}

func parseStatDirective(format string, idx *int) (leftAlign, zeroPad bool, width, precision int, directive byte) {
	precision = -1
	for *idx < len(format) {
		switch format[*idx] {
		case '-':
			leftAlign = true
			*idx++
		case '0':
			zeroPad = true
			*idx++
		default:
			goto widthParse
		}
	}
widthParse:
	for *idx < len(format) && format[*idx] >= '0' && format[*idx] <= '9' {
		width = width*10 + int(format[*idx]-'0')
		*idx++
	}
	if *idx < len(format) && format[*idx] == '.' {
		*idx++
		precision = 0
		for *idx < len(format) && format[*idx] >= '0' && format[*idx] <= '9' {
			precision = precision*10 + int(format[*idx]-'0')
			*idx++
		}
	}
	if *idx < len(format) {
		directive = format[*idx]
	}
	return
}

func statDirectiveValue(ctx context.Context, inv *Invocation, abs string, info stdfs.FileInfo, owner permissionOwnership, directive byte, precision int) (string, error) {
	switch directive {
	case 'n':
		return abs, nil
	case 'N':
		return statQuotedName(ctx, inv, abs, info), nil
	case 's':
		return strconv.FormatInt(info.Size(), 10), nil
	case 'd':
		return strconv.FormatUint(statDevice(info), 10), nil
	case 'i':
		return strconv.FormatUint(statInode(info), 10), nil
	case 'F':
		return fileTypeName(info), nil
	case 'a':
		return formatModeOctal(info.Mode()), nil
	case 'A':
		return formatModeLong(info.Mode()), nil
	case 'u':
		return strconv.FormatUint(uint64(owner.uid), 10), nil
	case 'g':
		return strconv.FormatUint(uint64(owner.gid), 10), nil
	case 'U':
		return permissionNameOrID(owner.user, owner.uid), nil
	case 'G':
		return permissionNameOrID(owner.group, owner.gid), nil
	case 'm':
		return "/", nil
	case 'X':
		if atime, ok := statAccessTime(info); ok {
			return strconv.FormatInt(atime.Unix(), 10), nil
		}
		return "0", nil
	case 'Y':
		if precision >= 0 {
			return formatStatTimeWithPrecision(info.ModTime(), precision), nil
		}
		return strconv.FormatInt(info.ModTime().Unix(), 10), nil
	case 'Z':
		if ctime, ok := statChangeTime(info); ok {
			return strconv.FormatInt(ctime.Unix(), 10), nil
		}
		return strconv.FormatInt(info.ModTime().Unix(), 10), nil
	case 'W':
		if birth, ok := statBirthTime(info); ok {
			return strconv.FormatInt(birth.Unix(), 10), nil
		}
		return "0", nil
	default:
		return "", fmt.Errorf("unsupported format sequence %%%c", directive)
	}
}

func statQuotedName(ctx context.Context, inv *Invocation, abs string, info stdfs.FileInfo) string {
	if info.Mode()&stdfs.ModeSymlink != 0 {
		target, err := inv.FS.Readlink(ctx, abs)
		if err == nil {
			return fmt.Sprintf("%q -> %q", abs, target)
		}
	}
	style := inv.Env["QUOTING_STYLE"]
	if style == "locale" {
		return "'" + strings.ReplaceAll(abs, "'", "\\'") + "'"
	}
	return fmt.Sprintf("%q", abs)
}

func runStatFilesystemMode(_ context.Context, inv *Invocation, _ statOptions, files []string) error {
	if len(files) == 0 {
		return exitf(inv, 1, "stat: missing operand")
	}
	exitCode := 0
	for _, name := range files {
		if name == "-" {
			_, _ = fmt.Fprintln(inv.Stderr, "stat: cannot read file system information for '-': No such file or directory")
			exitCode = 1
			continue
		}
		_, _ = fmt.Fprintf(inv.Stdout, "  File: %s\n  ID: 0 Namelen: 255 Type: unknown\n", name)
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func decodeStatEscapes(value string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' || i == len(value)-1 {
			b.WriteByte(value[i])
			continue
		}
		i++
		switch value[i] {
		case 'a':
			b.WriteByte('\a')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case '\\':
			b.WriteByte('\\')
		case '0':
			b.WriteByte(0)
		case 'x':
			if i+1 >= len(value) {
				b.WriteByte('x')
				continue
			}
			end := min(i+3, len(value))
			hex := value[i+1 : end]
			n, err := strconv.ParseUint(hex, 16, 8)
			if err != nil {
				b.WriteByte('x')
				continue
			}
			b.WriteByte(byte(n))
			i += len(hex)
		default:
			if value[i] >= '0' && value[i] <= '7' {
				end := i + 1
				for end < len(value) && end < i+3 && value[end] >= '0' && value[end] <= '7' {
					end++
				}
				n, err := strconv.ParseUint(value[i:end], 8, 8)
				if err != nil {
					return "", err
				}
				b.WriteByte(byte(n))
				i = end - 1
				continue
			}
			b.WriteByte(value[i])
		}
	}
	return b.String(), nil
}

func formatStatTimeWithPrecision(ts time.Time, precision int) string {
	sec := ts.Unix()
	nsec := ts.Nanosecond()
	if precision <= 0 {
		return fmt.Sprintf("%d.", sec)
	}
	fraction := fmt.Sprintf("%09d", nsec)
	if precision <= 9 {
		return fmt.Sprintf("%d.%s", sec, fraction[:precision])
	}
	return fmt.Sprintf("%d.%s%s", sec, fraction, strings.Repeat("0", precision-9))
}

func trimStatFloatPrecision(value string, precision int) string {
	if precision < 0 {
		return value
	}
	before, after, ok := strings.Cut(value, ".")
	if !ok {
		return value
	}
	if precision == 0 {
		return before + "."
	}
	if len(after) >= precision {
		return before + "." + after[:precision]
	}
	return before + "." + after + strings.Repeat("0", precision-len(after))
}

func statDevice(info stdfs.FileInfo) uint64 {
	dev, _, ok := testDeviceAndInode(info)
	if !ok {
		return 0
	}
	return dev
}

func statInode(info stdfs.FileInfo) uint64 {
	_, ino, ok := testDeviceAndInode(info)
	if !ok {
		return 0
	}
	return ino
}

func statAccessTime(info stdfs.FileInfo) (time.Time, bool) {
	return statTimeFromSys(info, "Atim", "Atimespec", "Atime", "AtimeNsec")
}

func statChangeTime(info stdfs.FileInfo) (time.Time, bool) {
	return statTimeFromSys(info, "Ctim", "Ctimespec", "Ctime", "CtimeNsec")
}

func statBirthTime(info stdfs.FileInfo) (time.Time, bool) {
	return statTimeFromSys(info, "Birthtimespec", "Btim", "Birthtime", "BirthtimeNsec")
}

func statTimeFromSys(info stdfs.FileInfo, names ...string) (time.Time, bool) {
	sys := reflect.ValueOf(info.Sys())
	if !sys.IsValid() {
		return time.Time{}, false
	}
	if sys.Kind() == reflect.Pointer {
		if sys.IsNil() {
			return time.Time{}, false
		}
		sys = sys.Elem()
	}
	if sys.Kind() != reflect.Struct {
		return time.Time{}, false
	}
	if len(names) >= 2 {
		for _, name := range names[:2] {
			if field := sys.FieldByName(name); field.IsValid() {
				if t, ok := statTimespec(field); ok {
					return t, true
				}
			}
		}
	}
	if len(names) >= 4 {
		if sec := sys.FieldByName(names[2]); sec.IsValid() {
			nsec := sys.FieldByName(names[3])
			return time.Unix(int64(statUintField(sec)), int64(statUintField(nsec))), true
		}
	}
	return time.Time{}, false
}

func statTimespec(value reflect.Value) (time.Time, bool) {
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return time.Time{}, false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return time.Time{}, false
	}
	sec := value.FieldByName("Sec")
	nsec := value.FieldByName("Nsec")
	if !sec.IsValid() || !nsec.IsValid() {
		return time.Time{}, false
	}
	return time.Unix(int64(statUintField(sec)), int64(statUintField(nsec))), true
}

func statUintField(value reflect.Value) uint64 {
	switch value.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return value.Uint()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return uint64(value.Int())
	default:
		return 0
	}
}

var _ Command = (*Stat)(nil)
var _ SpecProvider = (*Stat)(nil)
var _ ParsedRunner = (*Stat)(nil)
