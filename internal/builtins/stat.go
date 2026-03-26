package builtins

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"path"
	"reflect"
	"strconv"
	"strings"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
)

type Stat struct{}

type statOptions struct {
	format      string
	printf      bool
	dereference bool
}

type statDirectiveSpec struct {
	leftAlign bool
	zeroPad   bool
	width     int
	precision int
	modifier  byte
	directive byte
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
		output, err := renderStatOutput(ctx, inv, name, abs, info, opts)
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
	if name == "-" {
		return statStdin(inv)
	}
	if opts.dereference || hasTrailingSlash(name) {
		return statFollowPath(ctx, inv, name)
	}
	return lstatPath(ctx, inv, name)
}

func hasTrailingSlash(name string) bool {
	return len(name) > 1 && strings.HasSuffix(name, "/")
}

func renderStatOutput(ctx context.Context, inv *Invocation, name, abs string, info stdfs.FileInfo, opts statOptions) (string, error) {
	if opts.format == "" {
		return defaultStatOutput(name, info), nil
	}
	format := opts.format
	if opts.printf {
		format = decodeStatEscapes(inv, format)
	}
	rendered, err := renderStatFormat(ctx, inv, name, abs, info, format)
	if err != nil {
		var invalid *statInvalidDirectiveError
		if errors.As(err, &invalid) {
			return "", exitf(inv, 1, "stat: '%s': invalid directive", invalid.spec)
		}
		return "", &ExitError{Code: 1, Err: err}
	}
	if opts.printf {
		return rendered, nil
	}
	return rendered + "\n", nil
}

func defaultStatOutput(name string, info stdfs.FileInfo) string {
	return fmt.Sprintf(
		"  File: %s\n  Size: %d\n  Type: %s\n  Mode: (%s/%s)\n",
		name,
		info.Size(),
		fileTypeName(info),
		formatModeOctal(info.Mode()),
		formatModeLong(info.Mode()),
	)
}

func renderStatFormat(ctx context.Context, inv *Invocation, name, abs string, info stdfs.FileInfo, format string) (string, error) {
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
		specStart := i - 1
		spec := parseStatDirective(format, &i)
		specEnd := len(format)
		if i < len(format) {
			specEnd = i + 1
		}
		value, ok := statDirectiveValue(ctx, inv, name, abs, info, owner, spec.modifier, spec.directive, spec.precision)
		if !ok {
			return "", &statInvalidDirectiveError{spec: format[specStart:specEnd]}
		}
		if spec.precision >= 0 && spec.directive == 'Y' && strings.Contains(value, ".") {
			value = trimStatFloatPrecision(value, spec.precision)
		}
		if spec.width > 0 {
			pad := " "
			if spec.zeroPad && !spec.leftAlign {
				pad = "0"
			}
			if spec.leftAlign {
				value += strings.Repeat(pad, max(0, spec.width-len(value)))
			} else {
				value = strings.Repeat(pad, max(0, spec.width-len(value))) + value
			}
		}
		b.WriteString(value)
	}
	return b.String(), nil
}

func parseStatDirective(format string, idx *int) statDirectiveSpec {
	spec := statDirectiveSpec{precision: -1}
	for *idx < len(format) {
		switch format[*idx] {
		case '-':
			spec.leftAlign = true
			*idx++
		case '0':
			spec.zeroPad = true
			*idx++
		default:
			goto widthParse
		}
	}
widthParse:
	for *idx < len(format) && format[*idx] >= '0' && format[*idx] <= '9' {
		spec.width = spec.width*10 + int(format[*idx]-'0')
		*idx++
	}
	if *idx < len(format) && format[*idx] == '.' {
		*idx++
		spec.precision = 0
		for *idx < len(format) && format[*idx] >= '0' && format[*idx] <= '9' {
			spec.precision = spec.precision*10 + int(format[*idx]-'0')
			*idx++
		}
	}
	if *idx < len(format) && (format[*idx] == 'H' || format[*idx] == 'L') {
		spec.modifier = format[*idx]
		*idx++
	}
	if *idx < len(format) {
		spec.directive = format[*idx]
	}
	return spec
}

func statDirectiveValue(ctx context.Context, inv *Invocation, name, abs string, info stdfs.FileInfo, owner permissionOwnership, modifier, directive byte, precision int) (string, bool) {
	switch directive {
	case 'n':
		if modifier != 0 {
			return "", false
		}
		return name, true
	case 'N':
		if modifier != 0 {
			return "", false
		}
		return statQuotedName(ctx, inv, name, abs, info), true
	case 's':
		if modifier != 0 {
			return "", false
		}
		return strconv.FormatInt(info.Size(), 10), true
	case 'd':
		if modifier == 'H' || modifier == 'L' {
			return statMajorMinorValue(statDevice(info), modifier), true
		}
		if modifier != 0 {
			return "", false
		}
		return strconv.FormatUint(statDevice(info), 10), true
	case 'i':
		if modifier != 0 {
			return "", false
		}
		return strconv.FormatUint(statInode(info), 10), true
	case 'F':
		if modifier != 0 {
			return "", false
		}
		return fileTypeName(info), true
	case 'a':
		if modifier != 0 {
			return "", false
		}
		return formatModeOctalBare(info.Mode()), true
	case 'A':
		if modifier != 0 {
			return "", false
		}
		return formatModeLong(info.Mode()), true
	case 'u':
		if modifier != 0 {
			return "", false
		}
		return strconv.FormatUint(uint64(owner.uid), 10), true
	case 'g':
		if modifier != 0 {
			return "", false
		}
		return strconv.FormatUint(uint64(owner.gid), 10), true
	case 'U':
		if modifier != 0 {
			return "", false
		}
		return permissionNameOrID(owner.user, owner.uid), true
	case 'G':
		if modifier != 0 {
			return "", false
		}
		return permissionNameOrID(owner.group, owner.gid), true
	case 'm':
		if modifier != 0 {
			return "", false
		}
		return "/", true
	case 'r':
		if modifier == 'H' || modifier == 'L' {
			return statMajorMinorValue(statRawDevice(info), modifier), true
		}
		if modifier != 0 {
			return "", false
		}
		return strconv.FormatUint(statRawDevice(info), 10), true
	case 'R':
		if modifier != 0 {
			return "", false
		}
		return fmt.Sprintf("%x", statRawDevice(info)), true
	case 'X':
		if modifier != 0 {
			return "", false
		}
		if atime, ok := statAccessTime(info); ok {
			return strconv.FormatInt(atime.Unix(), 10), true
		}
		return "0", true
	case 'x':
		if modifier != 0 {
			return "", false
		}
		if atime, ok := statAccessTime(info); ok {
			return formatStatTimestamp(atime), true
		}
		return "-", true
	case 'Y':
		if modifier != 0 {
			return "", false
		}
		if precision >= 0 {
			return formatStatTimeWithPrecision(info.ModTime(), precision), true
		}
		return strconv.FormatInt(info.ModTime().Unix(), 10), true
	case 'y':
		if modifier != 0 {
			return "", false
		}
		return formatStatTimestamp(info.ModTime()), true
	case 'Z':
		if modifier != 0 {
			return "", false
		}
		if ctime, ok := statChangeTime(info); ok {
			return strconv.FormatInt(ctime.Unix(), 10), true
		}
		return strconv.FormatInt(info.ModTime().Unix(), 10), true
	case 'z':
		if modifier != 0 {
			return "", false
		}
		if ctime, ok := statChangeTime(info); ok {
			return formatStatTimestamp(ctime), true
		}
		return formatStatTimestamp(info.ModTime()), true
	case 'W':
		if modifier != 0 {
			return "", false
		}
		if birth, ok := statBirthTime(info); ok {
			return strconv.FormatInt(birth.Unix(), 10), true
		}
		return "0", true
	case 'w':
		if modifier != 0 {
			return "", false
		}
		if birth, ok := statBirthTime(info); ok {
			return formatStatTimestamp(birth), true
		}
		return "-", true
	default:
		return "", false
	}
}

func statQuotedName(ctx context.Context, inv *Invocation, name, abs string, info stdfs.FileInfo) string {
	if info.Mode()&stdfs.ModeSymlink != 0 && abs != "" && abs != "-" {
		target, err := inv.FS.Readlink(ctx, abs)
		if err == nil {
			return fmt.Sprintf("%q -> %q", name, target)
		}
	}
	style := inv.Env["QUOTING_STYLE"]
	if style == "locale" {
		return "'" + strings.ReplaceAll(name, "'", "\\'") + "'"
	}
	return fmt.Sprintf("%q", name)
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

func decodeStatEscapes(inv *Invocation, value string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			b.WriteByte(value[i])
			continue
		}
		if i == len(value)-1 {
			statWarnf(inv, "stat: warning: backslash at end of format")
			b.WriteByte('\\')
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
		case 'x':
			end := i + 1
			for end < len(value) && end < i+3 && isStatHexDigit(value[end]) {
				end++
			}
			if end == i+1 {
				statWarnf(inv, "stat: warning: unrecognized escape '\\x'")
				b.WriteByte('x')
				continue
			}
			n, _ := strconv.ParseUint(value[i+1:end], 16, 8)
			b.WriteByte(byte(n))
			i = end - 1
		default:
			if value[i] >= '0' && value[i] <= '7' {
				end := i + 1
				for end < len(value) && end < i+3 && value[end] >= '0' && value[end] <= '7' {
					end++
				}
				n, _ := strconv.ParseUint(value[i:end], 8, 16)
				b.WriteByte(byte(n))
				i = end - 1
				continue
			}
			b.WriteByte(value[i])
		}
	}
	return b.String()
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

func formatStatTimestamp(ts time.Time) string {
	return ts.Format("2006-01-02 15:04:05.000000000 -0700")
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

func statRawDevice(info stdfs.FileInfo) uint64 {
	sys := reflect.ValueOf(info.Sys())
	if !sys.IsValid() {
		return 0
	}
	if sys.Kind() == reflect.Pointer {
		if sys.IsNil() {
			return 0
		}
		sys = sys.Elem()
	}
	if sys.Kind() != reflect.Struct {
		return 0
	}
	field := sys.FieldByName("Rdev")
	if !field.IsValid() {
		return 0
	}
	return statUintField(field)
}

func statMajorMinorValue(dev uint64, modifier byte) string {
	if modifier == 'H' {
		return strconv.FormatUint(statMajor(dev), 10)
	}
	return strconv.FormatUint(statMinor(dev), 10)
}

func statMajor(dev uint64) uint64 {
	return (dev >> 8) & 0xfff
}

func statMinor(dev uint64) uint64 {
	return (dev & 0xff) | ((dev >> 12) & 0xfff00)
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

func statFollowPath(ctx context.Context, inv *Invocation, name string) (stdfs.FileInfo, string, error) {
	resolvedName := remapCompatHostPath(inv, name)
	if hasTrailingSlash(name) && !strings.HasSuffix(resolvedName, "/") {
		resolvedName += "/"
	}
	resolved, err := canonicalizeReadlink(ctx, inv, resolvedName, readlinkModeCanonicalizeExisting)
	if err != nil {
		return nil, "", err
	}
	info, err := inv.FS.Stat(ctx, resolved)
	if err != nil {
		return nil, "", err
	}
	return info, resolved, nil
}

func statStdin(inv *Invocation) (stdfs.FileInfo, string, error) {
	reader := io.Reader(nil)
	if inv != nil {
		reader = inv.Stdin
	}
	if reader == nil {
		return nil, "-", errors.New("bad file descriptor")
	}
	ttyPath := ""
	for {
		var readerBadFDErr error
		if statter, ok := reader.(interface {
			Stat() (stdfs.FileInfo, error)
		}); ok {
			info, err := statter.Stat()
			if err == nil {
				return info, "-", nil
			}
			if !statIsBadFileDescriptor(err) {
				return nil, "-", err
			}
			readerBadFDErr = err
		}
		if meta, ok := reader.(interface {
			RedirectPath() string
		}); ok && ttyPath == "" {
			if redirectPath, ok := ttyRecognizedPath(meta.RedirectPath()); ok {
				ttyPath = redirectPath
			}
		}
		unwrapper, ok := reader.(interface {
			UnderlyingReader() io.Reader
		})
		if !ok {
			if readerBadFDErr != nil {
				return nil, "-", readerBadFDErr
			}
			break
		}
		next := unwrapper.UnderlyingReader()
		if next == nil || next == reader {
			if readerBadFDErr != nil {
				return nil, "-", readerBadFDErr
			}
			return nil, "-", errors.New("bad file descriptor")
		}
		reader = next
	}
	if ttyPath != "" {
		return statSyntheticStdinFileInfo(path.Base(ttyPath), stdfs.ModeDevice|stdfs.ModeCharDevice|0o600), "-", nil
	}
	return statSyntheticStdinFileInfo("-", stdfs.ModeNamedPipe|0o600), "-", nil
}

func statWarnf(inv *Invocation, format string, args ...any) {
	if inv == nil || inv.Stderr == nil {
		return
	}
	_, _ = fmt.Fprintf(inv.Stderr, format+"\n", args...)
}

func isStatHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func statIsBadFileDescriptor(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "bad file descriptor")
}

func statSyntheticStdinFileInfo(name string, mode stdfs.FileMode) stdfs.FileInfo {
	ownership := gbfs.DefaultOwnership()
	return statSyntheticFileInfo{
		name:    name,
		mode:    mode,
		modTime: time.Unix(0, 0).UTC(),
		uid:     ownership.UID,
		gid:     ownership.GID,
	}
}

type statInvalidDirectiveError struct {
	spec string
}

func (e *statInvalidDirectiveError) Error() string {
	return fmt.Sprintf("invalid directive %s", e.spec)
}

type statSyntheticFileInfo struct {
	name    string
	mode    stdfs.FileMode
	modTime time.Time
	uid     uint32
	gid     uint32
}

func (fi statSyntheticFileInfo) Name() string         { return fi.name }
func (fi statSyntheticFileInfo) Size() int64          { return 0 }
func (fi statSyntheticFileInfo) Mode() stdfs.FileMode { return fi.mode }
func (fi statSyntheticFileInfo) ModTime() time.Time   { return fi.modTime }
func (fi statSyntheticFileInfo) IsDir() bool          { return fi.mode.IsDir() }
func (fi statSyntheticFileInfo) Sys() any             { return gbfs.FileOwnership{UID: fi.uid, GID: fi.gid} }
func (fi statSyntheticFileInfo) Ownership() (gbfs.FileOwnership, bool) {
	return gbfs.FileOwnership{UID: fi.uid, GID: fi.gid}, true
}

var _ Command = (*Stat)(nil)
var _ SpecProvider = (*Stat)(nil)
var _ ParsedRunner = (*Stat)(nil)
