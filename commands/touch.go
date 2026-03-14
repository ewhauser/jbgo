package commands

import (
	"context"
	"fmt"
	stdfs "io/fs"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ewhauser/gbash/policy"
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
			{Name: "file", ValueName: "FILE", Repeatable: true, Required: true},
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
	clone.Args = append([]string{"--posix-stamp", first}, inv.Args[1:]...)
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
	switch matches.Value("time") {
	case "":
	case "access", "atime", "use":
		opts.affectMtime = false
	case "mtime", "modify":
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
	base := touchTimes{
		atime: time.Now().UTC(),
		mtime: time.Now().UTC(),
	}
	switch {
	case opts.reference != "":
		ref, err := touchReferenceTimes(ctx, inv, opts.reference, opts.noDereference)
		if err != nil {
			return touchTimes{}, err
		}
		base = ref
	case opts.timestamp != "":
		ts, err := parseTouchTimestamp(opts.timestamp)
		if err != nil {
			return touchTimes{}, exitf(inv, 1, "touch: invalid date format %q", opts.timestamp)
		}
		base = touchTimes{atime: ts, mtime: ts}
	}
	if opts.date != "" {
		atime, err := parseTouchDateValue(base.atime, opts.date)
		if err != nil {
			return touchTimes{}, exitf(inv, 1, "touch: invalid date format %q", opts.date)
		}
		mtime, err := parseTouchDateValue(base.mtime, opts.date)
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
	if name == "-" {
		return nil
	}
	info, abs, exists, err := touchStatMaybe(ctx, inv, name, opts.noDereference)
	if err != nil {
		return err
	}
	if !exists {
		if opts.noCreate {
			return nil
		}
		if opts.noDereference && !hasTrailingSlash(name) {
			return exitf(inv, 1, "touch: cannot touch %q: No such file or directory", name)
		}
		abs, err = allowPath(ctx, inv, policy.FileActionWrite, name)
		if err != nil {
			return err
		}
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
		recordFileMutation(inv.trace, "touch", abs, "", abs)
		info, _, err = statPath(ctx, inv, name)
		if err != nil {
			return err
		}
	} else if info.IsDir() {
		if _, err := allowPath(ctx, inv, policy.FileActionWrite, name); err != nil {
			return err
		}
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
			mtime = info.ModTime()
		}
	}
	if err := inv.FS.Chtimes(ctx, abs, atime, mtime); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func touchStatMaybe(ctx context.Context, inv *Invocation, name string, noDereference bool) (info stdfs.FileInfo, abs string, exists bool, err error) {
	if noDereference && !hasTrailingSlash(name) {
		return lstatMaybe(ctx, inv, policy.FileActionLstat, name)
	}
	return statMaybe(ctx, inv, policy.FileActionStat, name)
}

func parseTouchDateValue(base time.Time, value string) (time.Time, error) {
	if value == "now" {
		return time.Now().UTC(), nil
	}
	if shifted, ok, err := parseTouchRelativeDate(base, value); ok || err != nil {
		return shifted, err
	}
	return parseTouchTime(value)
}

func parseTouchRelativeDate(base time.Time, value string) (time.Time, bool, error) {
	fields := strings.Fields(value)
	if len(fields) != 2 {
		return time.Time{}, false, nil
	}
	amount, err := strconv.Atoi(fields[0])
	if err != nil {
		return time.Time{}, false, nil
	}
	switch fields[1] {
	case "day", "days":
		return base.AddDate(0, 0, amount).UTC(), true, nil
	default:
		return time.Time{}, false, nil
	}
}

func parseTouchTime(value string) (time.Time, error) {
	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02 15:04 -0700",
		"2006/01/02 15:04:05",
		"2006/01/02",
		"2006-01-02",
	}
	for _, layout := range layouts {
		parsed, err := time.ParseInLocation(layout, value, time.UTC)
		if err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported time format")
}

func parseTouchTimestamp(value string) (time.Time, error) {
	layouts := []string{
		"200601021504.05",
		"200601021504",
		"0601021504.05",
		"0601021504",
		"01021504.05",
		"01021504",
	}
	for _, layout := range layouts {
		if len(value) != len(layout) {
			continue
		}
		parsed, err := time.ParseInLocation(layout, value, time.UTC)
		if err == nil {
			if strings.HasPrefix(layout, "0102") {
				parsed = parsed.AddDate(time.Now().UTC().Year()-parsed.Year(), 0, 0)
			}
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp")
}

func touchLegacyTimestampActive(inv *Invocation) bool {
	if inv == nil || inv.Env == nil {
		return false
	}
	return inv.Env["_POSIX2_VERSION"] == "199209"
}

func touchHasExplicitSource(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "-d", "--date", "-r", "--reference", "-t":
			return true
		}
		if strings.HasPrefix(arg, "--date=") || strings.HasPrefix(arg, "--reference=") {
			return true
		}
	}
	return false
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
		year, _ := strconv.Atoi(value[:2])
		return year >= 69 && year <= 99
	}
	return true
}

var _ Command = (*Touch)(nil)
var _ SpecProvider = (*Touch)(nil)
var _ ParsedRunner = (*Touch)(nil)
var _ ParseInvocationNormalizer = (*Touch)(nil)
