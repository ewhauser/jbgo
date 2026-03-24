package builtins

import (
	"context"
	"fmt"
	stdfs "io/fs"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ewhauser/gbash/internal/commandutil"
)

type Touch struct{}

type touchOptions struct {
	noCreate      bool
	noDereference bool
	affectAtime   bool
	affectMtime   bool
	date          string
	reference     string
	timestamp     string
	files         []string
}

type touchTimes struct {
	atime time.Time
	mtime time.Time
}

func NewTouch() *Touch {
	return &Touch{}
}

func (c *Touch) Name() string {
	return "touch"
}

func (c *Touch) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Touch) Spec() CommandSpec {
	return CommandSpec{
		Name:  "touch",
		About: "Update the access and modification times of each FILE to the current time.",
		Usage: "touch [OPTION]... FILE...",
		Options: []OptionSpec{
			{Name: "access", Short: 'a', Help: "change only the access time"},
			{Name: "date", Short: 'd', Long: "date", Arity: OptionRequiredValue, ValueName: "STRING", Help: "parse STRING and use it instead of current time"},
			{Name: "force", Short: 'f', Help: "(ignored)"},
			{Name: "modification", Short: 'm', Help: "change only the modification time"},
			{Name: "no-create", Short: 'c', Long: "no-create", Help: "do not create any files"},
			{Name: "no-dereference", Short: 'h', Long: "no-dereference", Help: "affect each symbolic link instead of any referenced file"},
			{Name: "reference", Short: 'r', Long: "reference", Arity: OptionRequiredValue, ValueName: "FILE", Help: "use this file's times instead of current time"},
			{Name: "timestamp", Short: 't', Arity: OptionRequiredValue, ValueName: "STAMP", Help: "use [[CC]YY]MMDDhhmm[.ss] instead of current time"},
			{Name: "time", Long: "time", Arity: OptionRequiredValue, ValueName: "WORD", Help: "change the specified time: WORD is access, atime, or use for access time; mtime or modify for modification time"},
			{Name: "posix-stamp", Long: "posix-stamp", Arity: OptionRequiredValue, ValueName: "STAMP", Hidden: true},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
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

func (c *Touch) NormalizeInvocation(inv *Invocation) *Invocation {
	if inv == nil || !touchLegacyTimestampActive(inv) || len(inv.Args) < 2 {
		return inv
	}
	if touchHasExplicitSource(inv.Args) {
		return inv
	}
	first := inv.Args[0]
	if !isTouchLegacyTimestamp(first) {
		return inv
	}
	clone := *inv
	clone.Args = append([]string{"--posix-stamp", normalizeTouchLegacyTimestamp(first)}, inv.Args[1:]...)
	return &clone
}

func (c *Touch) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := parseTouchMatches(inv, matches)
	if err != nil {
		return err
	}
	times, err := resolveTouchTimes(ctx, inv, &opts)
	if err != nil {
		return err
	}
	if len(opts.files) == 0 {
		return exitf(inv, 1, "touch: missing file operand\nTry 'touch --help' for more information.")
	}

	for _, name := range opts.files {
		if err := touchOne(ctx, inv, &opts, times, name); err != nil {
			return err
		}
	}
	return nil
}

func parseTouchMatches(inv *Invocation, matches *ParsedCommand) (touchOptions, error) {
	opts := touchOptions{
		noCreate:      matches.Has("no-create"),
		noDereference: matches.Has("no-dereference"),
		date:          matches.Value("date"),
		reference:     matches.Value("reference"),
		files:         matches.Args("file"),
	}
	if matches.Has("timestamp") {
		opts.timestamp = matches.Value("timestamp")
	}
	if matches.Has("posix-stamp") {
		opts.timestamp = matches.Value("posix-stamp")
	}

	sourceCount := 0
	if opts.reference != "" {
		sourceCount++
	}
	if opts.timestamp != "" {
		sourceCount++
	}
	if sourceCount > 1 || (opts.timestamp != "" && opts.date != "") {
		return touchOptions{}, exitf(inv, 1, "touch: cannot specify times from more than one source")
	}

	opts.affectAtime = true
	opts.affectMtime = true
	switch timeValue := strings.ToLower(strings.TrimSpace(matches.Value("time"))); {
	case timeValue == "":
	case touchMatchesTimeWord(timeValue, "access", "atime", "use"):
		opts.affectMtime = false
	case touchMatchesTimeWord(timeValue, "mtime", "modify"):
		opts.affectAtime = false
	default:
		return touchOptions{}, exitf(inv, 1, "touch: invalid argument %q for --time", matches.Value("time"))
	}
	if matches.Has("access") && !matches.Has("modification") {
		opts.affectMtime = false
	}
	if matches.Has("modification") && !matches.Has("access") {
		opts.affectAtime = false
	}
	return opts, nil
}

func resolveTouchTimes(ctx context.Context, inv *Invocation, opts *touchOptions) (touchTimes, error) {
	now := inv.Now().UTC()
	base := touchTimes{
		atime: now,
		mtime: now,
	}
	switch {
	case opts.reference != "":
		ref, err := touchReferenceTimes(ctx, inv, opts.reference, opts.noDereference)
		if err != nil {
			return touchTimes{}, err
		}
		base = ref
	case opts.timestamp != "":
		ts, err := parseTouchTimestamp(opts.timestamp, now)
		if err != nil {
			return touchTimes{}, exitf(inv, 1, "touch: invalid date format %q", opts.timestamp)
		}
		base = touchTimes{atime: ts, mtime: ts}
	}
	if opts.date != "" {
		atime, err := parseTouchDateValue(base.atime, opts.date, now)
		if err != nil {
			return touchTimes{}, exitf(inv, 1, "touch: invalid date format %q", opts.date)
		}
		mtime, err := parseTouchDateValue(base.mtime, opts.date, now)
		if err != nil {
			return touchTimes{}, exitf(inv, 1, "touch: invalid date format %q", opts.date)
		}
		base = touchTimes{atime: atime, mtime: mtime}
	}
	return base, nil
}

func touchReferenceTimes(ctx context.Context, inv *Invocation, name string, noDereference bool) (touchTimes, error) {
	var (
		info stdfs.FileInfo
		err  error
	)
	if noDereference && !hasTrailingSlash(name) {
		info, _, err = lstatPath(ctx, inv, name)
	} else {
		info, _, err = statPath(ctx, inv, name)
	}
	if err != nil {
		return touchTimes{}, err
	}
	atime, ok := statAccessTime(info)
	if !ok {
		atime = info.ModTime()
	}
	return touchTimes{atime: atime.UTC(), mtime: info.ModTime().UTC()}, nil
}

func touchOne(ctx context.Context, inv *Invocation, opts *touchOptions, times touchTimes, name string) error {
	targetName, displayName, noDereference := touchResolveTarget(inv, name, opts.noDereference)
	if name == "-" && targetName == "" {
		return nil
	}
	info, abs, exists, err := touchStatMaybe(ctx, inv, targetName, noDereference)
	if err != nil {
		return err
	}
	if !exists {
		if opts.noCreate {
			return nil
		}
		if noDereference && !hasTrailingSlash(targetName) {
			return exitf(inv, 1, "touch: cannot touch %q: No such file or directory", displayName)
		}
		abs = allowPath(inv, targetName)
		if err := ensureParentDirExists(ctx, inv, abs); err != nil {
			return err
		}
		file, err := inv.FS.OpenFile(ctx, abs, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		if err := file.Close(); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		recordFileMutation(inv.TraceRecorder(), "touch", abs, "", abs)
		info, _, err = statPath(ctx, inv, targetName)
		if err != nil {
			return err
		}
		exists = true
	}

	atime := times.atime
	mtime := times.mtime
	if exists {
		currentAtime, ok := statAccessTime(info)
		if !ok {
			currentAtime = info.ModTime()
		}
		if !opts.affectAtime {
			atime = currentAtime
		}
		if !opts.affectMtime {
			mtime = info.ModTime() //nolint:nilaway // info is non-nil when exists is true
		}
	}
	if err := inv.FS.Chtimes(ctx, abs, atime, mtime); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func touchResolveTarget(inv *Invocation, name string, noDereference bool) (targetName, displayName string, effectiveNoDereference bool) {
	if name != "-" {
		return name, name, noDereference
	}
	if inv != nil && inv.Stdout != nil {
		if redirected, ok := resolveUnderlyingWriter(inv.Stdout).(commandutil.RedirectMetadata); ok {
			if path := redirected.RedirectPath(); path != "" {
				return path, name, false
			}
		}
	}
	return "", name, false
}

func touchStatMaybe(ctx context.Context, inv *Invocation, name string, noDereference bool) (info stdfs.FileInfo, abs string, exists bool, err error) {
	if noDereference && !hasTrailingSlash(name) {
		return lstatMaybe(ctx, inv, name)
	}
	return statMaybe(ctx, inv, name)
}

func parseTouchDateValue(base time.Time, value string, now time.Time) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if strings.EqualFold(trimmed, "now") {
		return now.UTC(), nil
	}
	if shifted, ok := parseTouchRelativeDate(base, trimmed); ok {
		return shifted, nil
	}
	parsed, _, err := parseDateValue(trimmed, base.UTC(), time.UTC)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func parseTouchRelativeDate(base time.Time, value string) (time.Time, bool) {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(value)))
	if len(fields) == 0 {
		return time.Time{}, false
	}
	sign := 1
	if fields[len(fields)-1] == "ago" {
		sign = -1
		fields = fields[:len(fields)-1]
		if len(fields) == 0 {
			return time.Time{}, false
		}
	}
	shifted := base.UTC()
	matched := false
	for idx := 0; idx < len(fields); {
		amount := 1
		if parsed, err := strconv.Atoi(fields[idx]); err == nil {
			amount = parsed
			idx++
			if idx >= len(fields) {
				return time.Time{}, false
			}
		}
		amount *= sign
		switch fields[idx] {
		case "fortnight", "fortnights":
			shifted = shifted.AddDate(0, 0, amount*14)
		case "week", "weeks":
			shifted = shifted.AddDate(0, 0, amount*7)
		case "day", "days":
			shifted = shifted.AddDate(0, 0, amount)
		case "month", "months":
			shifted = shifted.AddDate(0, amount, 0)
		case "year", "years":
			shifted = shifted.AddDate(amount, 0, 0)
		case "hour", "hours":
			shifted = shifted.Add(time.Duration(amount) * time.Hour)
		case "minute", "minutes", "min", "mins":
			shifted = shifted.Add(time.Duration(amount) * time.Minute)
		case "second", "seconds", "sec", "secs":
			shifted = shifted.Add(time.Duration(amount) * time.Second)
		default:
			return time.Time{}, false
		}
		matched = true
		idx++
	}
	return shifted.UTC(), matched
}

func parseTouchTimestamp(value string, now time.Time) (time.Time, error) {
	main := value
	second := 0
	if head, tail, ok := strings.Cut(value, "."); ok {
		main = head
		parsedSecond, err := strconv.Atoi(tail)
		if err != nil || len(tail) != 2 || parsedSecond < 0 || parsedSecond > 60 {
			return time.Time{}, fmt.Errorf("unsupported timestamp")
		}
		second = parsedSecond
	}
	var (
		year   int
		month  int
		day    int
		hour   int
		minute int
		err    error
	)
	switch len(main) {
	case 8:
		year = now.UTC().Year()
		month, err = strconv.Atoi(main[0:2])
		if err != nil {
			return time.Time{}, err
		}
		day, err = strconv.Atoi(main[2:4])
		if err != nil {
			return time.Time{}, err
		}
		hour, err = strconv.Atoi(main[4:6])
		if err != nil {
			return time.Time{}, err
		}
		minute, err = strconv.Atoi(main[6:8])
		if err != nil {
			return time.Time{}, err
		}
	case 10:
		shortYear, err := strconv.Atoi(main[0:2])
		if err != nil {
			return time.Time{}, err
		}
		year = touchTwoDigitYear(shortYear)
		month, err = strconv.Atoi(main[2:4])
		if err != nil {
			return time.Time{}, err
		}
		day, err = strconv.Atoi(main[4:6])
		if err != nil {
			return time.Time{}, err
		}
		hour, err = strconv.Atoi(main[6:8])
		if err != nil {
			return time.Time{}, err
		}
		minute, err = strconv.Atoi(main[8:10])
		if err != nil {
			return time.Time{}, err
		}
	case 12:
		year, err = strconv.Atoi(main[0:4])
		if err != nil {
			return time.Time{}, err
		}
		month, err = strconv.Atoi(main[4:6])
		if err != nil {
			return time.Time{}, err
		}
		day, err = strconv.Atoi(main[6:8])
		if err != nil {
			return time.Time{}, err
		}
		hour, err = strconv.Atoi(main[8:10])
		if err != nil {
			return time.Time{}, err
		}
		minute, err = strconv.Atoi(main[10:12])
		if err != nil {
			return time.Time{}, err
		}
	default:
		return time.Time{}, fmt.Errorf("unsupported timestamp")
	}
	parsed := time.Date(year, time.Month(month), day, hour, minute, 0, 0, time.UTC)
	if parsed.Year() != year || int(parsed.Month()) != month || parsed.Day() != day || parsed.Hour() != hour || parsed.Minute() != minute {
		return time.Time{}, fmt.Errorf("unsupported timestamp")
	}
	return parsed.Add(time.Duration(second) * time.Second).UTC(), nil
}

func touchTwoDigitYear(year int) int {
	if year >= 69 {
		return 1900 + year
	}
	return 2000 + year
}

func touchLegacyTimestampActive(inv *Invocation) bool {
	if inv == nil || inv.Env == nil {
		return false
	}
	return inv.Env["_POSIX2_VERSION"] == "199209"
}

func touchHasExplicitSource(args []string) bool {
	endOfOptions := false
	for _, arg := range args {
		if endOfOptions {
			continue
		}
		if arg == "--" {
			endOfOptions = true
			continue
		}
		switch {
		case arg == "-d", arg == "--date", strings.HasPrefix(arg, "--date="):
			return true
		case arg == "-r", arg == "--reference", strings.HasPrefix(arg, "--reference="):
			return true
		case arg == "-t", arg == "--posix-stamp", strings.HasPrefix(arg, "--posix-stamp="):
			return true
		case !strings.HasPrefix(arg, "-") || arg == "-":
			continue
		case strings.HasPrefix(arg, "--"):
			continue
		default:
			for idx := 1; idx < len(arg); idx++ {
				switch arg[idx] {
				case 'd', 'r', 't':
					return true
				}
			}
		}
	}
	return false
}

func touchMatchesTimeWord(value string, words ...string) bool {
	for _, word := range words {
		if strings.HasPrefix(word, value) {
			return true
		}
	}
	return false
}

func normalizeTouchLegacyTimestamp(value string) string {
	if len(value) == 10 {
		return value[8:] + value[:8]
	}
	return value
}

func isTouchLegacyTimestamp(value string) bool {
	if len(value) != 8 && len(value) != 10 {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	if len(value) == 10 {
		year, _ := strconv.Atoi(value[8:10])
		return year >= 69 && year <= 99
	}
	return true
}

var _ Command = (*Touch)(nil)
var _ SpecProvider = (*Touch)(nil)
var _ ParsedRunner = (*Touch)(nil)
var _ ParseInvocationNormalizer = (*Touch)(nil)
