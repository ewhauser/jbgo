package builtins

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

type Date struct{}

type dateSourceKind int

type dateFormatKind int

type dateOptions struct {
	utc       bool
	debug     bool
	help      bool
	version   bool
	source    dateSourceKind
	sourceArg string
	setValue  string
	legacySet bool
	format    dateFormatKind
	formatArg string
}

const (
	dateSourceNow dateSourceKind = iota
	dateSourceString
	dateSourceFile
	dateSourceReference
	dateSourceResolution
)

const (
	dateFormatDefault dateFormatKind = iota
	dateFormatCustom
	dateFormatISO8601
	dateFormatRFCEmail
	dateFormatRFC3339
	dateFormatResolution
)

const dateVersionText = "date (gbash)\n"

const dateHelpText = `Usage: date [OPTION]... [+FORMAT]
  or:  date [-u|--utc|--universal] [MMDDhhmm[[CC]YY][.ss]]
Display date and time in the given FORMAT.
With -s, or with [MMDDhhmm[[CC]YY][.ss]], set the date and time.

Mandatory arguments to long options are mandatory for short options too.
  -d, --date=STRING          display time described by STRING, not 'now'
      --debug                annotate the parsed date, and
                              warn about questionable usage to standard error
  -f, --file=DATEFILE        like --date; once for each line of DATEFILE
  -I[FMT], --iso-8601[=FMT]  output date/time in ISO 8601 format.
                               FMT='date' for date only (the default),
                               'hours', 'minutes', 'seconds', or 'ns'
                               for date and time to the indicated precision.
                               Example: 2006-08-14T02:34:56-06:00
      --resolution           output the available resolution of timestamps
                               Example: 0.000000001
  -R, --rfc-email            output date and time in RFC 5322 format.
                               Example: Mon, 14 Aug 2006 02:34:56 -0600
      --rfc-3339=FMT         output date/time in RFC 3339 format.
                               FMT='date', 'seconds', or 'ns'
                               for date and time to the indicated precision.
                               Example: 2006-08-14 02:34:56-06:00
  -r, --reference=FILE       display the last modification time of FILE
  -s, --set=STRING           set time described by STRING
  -u, --utc, --universal     print or set Coordinated Universal Time (UTC)
      --help                 display this help and exit
      --version              output version information and exit

All options that specify the date to display are mutually exclusive.
I.e.: --date, --file, --reference, --resolution.

FORMAT controls the output.  Interpreted sequences are:

  %%   a literal %
  %a   locale's abbreviated weekday name (e.g., Sun)
  %A   locale's full weekday name (e.g., Sunday)
  %b   locale's abbreviated month name (e.g., Jan)
  %B   locale's full month name (e.g., January)
  %c   locale's date and time (e.g., Thu Mar  3 23:05:25 2005)
  %C   century; like %Y, except omit last two digits (e.g., 20)
  %d   day of month (e.g., 01)
  %D   date (ambiguous); same as %m/%d/%y
  %e   day of month, space padded; same as %_d
  %F   full date; like %+4Y-%m-%d
  %g   last two digits of year of ISO week number (ambiguous; 00-99); see %G
  %G   year of ISO week number; normally useful only with %V
  %h   same as %b
  %H   hour (00..23)
  %I   hour (01..12)
  %j   day of year (001..366)
  %k   hour, space padded ( 0..23); same as %_H
  %l   hour, space padded ( 1..12); same as %_I
  %m   month (01..12)
  %M   minute (00..59)
  %n   a newline
  %N   nanoseconds (000000000..999999999)
  %p   locale's equivalent of either AM or PM; blank if not known
  %P   like %p, but lower case
  %q   quarter of year (1..4)
  %r   locale's 12-hour clock time (e.g., 11:11:04 PM)
  %R   24-hour hour and minute; same as %H:%M
  %s   seconds since the Epoch (1970-01-01 00:00 UTC)
  %S   second (00..60)
  %t   a tab
  %T   time; same as %H:%M:%S
  %u   day of week (1..7); 1 is Monday
  %U   week number of year, with Sunday as first day of week (00..53)
  %V   ISO week number, with Monday as first day of week (01..53)
  %w   day of week (0..6); 0 is Sunday
  %W   week number of year, with Monday as first day of week (00..53)
  %x   locale's date (can be ambiguous; e.g., 12/31/99)
  %X   locale's time representation (e.g., 23:13:48)
  %y   last two digits of year (ambiguous; 00..99)
  %Y   year
  %z   +hhmm numeric time zone (e.g., -0400)
  %:z  +hh:mm numeric time zone (e.g., -04:00)
  %::z  +hh:mm:ss numeric time zone (e.g., -04:00:00)
  %:::z  numeric time zone with : to necessary precision (e.g., -04, +05:30)
  %Z   alphabetic time zone abbreviation (e.g., EDT)

By default, date pads numeric fields with zeroes.
The following optional flags may follow '%':

  -  (hyphen) do not pad the field
  _  (underscore) pad with spaces
  0  (zero) pad with zeros
  +  pad with zeros, and put '+' before future years with >4 digits
  ^  use upper case if possible
  #  use opposite case if possible

After any flags comes an optional field width, as a decimal number;
then an optional modifier, which is either
E to use the locale's alternate representations if available, or
O to use the locale's alternate numeric symbols if available.

Examples:
Convert seconds since the Epoch (1970-01-01 UTC) to a date
  $ date --date='@2147483647'

Show the time on the west coast of the US (use tzselect(1) to find TZ)
  $ TZ='America/Los_Angeles' date

Show the local time for 9AM next Friday on the west coast of the US
  $ date --date='TZ="America/Los_Angeles" 09:00 next Fri'
`

func NewDate() *Date {
	return &Date{}
}

func (c *Date) Name() string {
	return "date"
}

func (c *Date) Run(ctx context.Context, inv *Invocation) error {
	opts, err := parseDateArgs(inv, inv.Args)
	if err != nil {
		return err
	}
	if opts.help {
		_, err := io.WriteString(inv.Stdout, dateHelpText)
		return err
	}
	if opts.version {
		_, err := io.WriteString(inv.Stdout, dateVersionText)
		return err
	}

	loc := resolveDateLocationFromEnv(inv.Env, opts.utc)
	now := inv.Now().In(loc)

	if opts.setValue != "" {
		parsed, info, err := parseDateSetValue(opts.setValue, now, loc)
		if err != nil {
			return exitf(inv, 1, "date: invalid date %q", opts.setValue)
		}
		dateWriteDebug(inv, opts.debug, info)
		if err := inv.SetTime(parsed.UTC()); err != nil {
			return exitf(inv, 1, "date: cannot set date: %v", err)
		}
		return nil
	}

	switch opts.source {
	case dateSourceNow:
		current := now
		if opts.utc {
			current = current.UTC()
		}
		dateWriteDebug(inv, opts.debug, buildDateParseInfo("now", current, false))
		return writeDateOutput(inv, current, &opts)
	case dateSourceString:
		parsed, info, err := parseDateValue(opts.sourceArg, now, loc)
		if err != nil {
			return exitf(inv, 1, "date: invalid date %q", opts.sourceArg)
		}
		dateWriteDebug(inv, opts.debug, info)
		return writeDateOutput(inv, dateOutputTime(parsed, loc, opts.utc), &opts)
	case dateSourceReference:
		info, _, err := statPath(ctx, inv, opts.sourceArg)
		if err != nil {
			return exitf(inv, 1, "date: %s: %s", opts.sourceArg, readAllErrorText(err))
		}
		current := dateOutputTime(info.ModTime(), loc, opts.utc)
		dateWriteDebug(inv, opts.debug, buildDateParseInfo(opts.sourceArg, current, false))
		return writeDateOutput(inv, current, &opts)
	case dateSourceResolution:
		current := dateOutputTime(time.Unix(0, 1).UTC(), loc, opts.utc)
		dateWriteDebug(inv, opts.debug, buildDateParseInfo("resolution", current, false))
		return writeDateOutput(inv, current, &opts)
	case dateSourceFile:
		return runDateFileSource(ctx, inv, &opts, now, loc)
	default:
		return nil
	}
}

func parseDateArgs(inv *Invocation, args []string) (dateOptions, error) {
	opts := dateOptions{source: dateSourceNow}
	var (
		positionals []string
		parsing     = true
		sourceKinds = make(map[dateSourceKind]struct{})
		setSeen     bool
	)

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if parsing && arg == "--" {
			parsing = false
			continue
		}
		if parsing && strings.HasPrefix(arg, "--") && arg != "--" {
			name, value, hasValue := strings.Cut(arg[2:], "=")
			switch name {
			case "help":
				if hasValue {
					return dateOptions{}, dateUsageError(inv, "option '--help' doesn't allow an argument")
				}
				opts.help = true
			case "version":
				if hasValue {
					return dateOptions{}, dateUsageError(inv, "option '--version' doesn't allow an argument")
				}
				opts.version = true
			case "debug":
				if hasValue {
					return dateOptions{}, dateUsageError(inv, "option '--debug' doesn't allow an argument")
				}
				opts.debug = true
			case "utc", "universal":
				if hasValue {
					return dateOptions{}, dateUsageError(inv, "option '--%s' doesn't allow an argument", name)
				}
				opts.utc = true
			case "date":
				var err error
				value, i, err = dateRequireValue(inv, args, i, name, value, hasValue)
				if err != nil {
					return dateOptions{}, err
				}
				opts.source = dateSourceString
				opts.sourceArg = value
				sourceKinds[dateSourceString] = struct{}{}
			case "file":
				var err error
				value, i, err = dateRequireValue(inv, args, i, name, value, hasValue)
				if err != nil {
					return dateOptions{}, err
				}
				opts.source = dateSourceFile
				opts.sourceArg = value
				sourceKinds[dateSourceFile] = struct{}{}
			case "reference":
				var err error
				value, i, err = dateRequireValue(inv, args, i, name, value, hasValue)
				if err != nil {
					return dateOptions{}, err
				}
				opts.source = dateSourceReference
				opts.sourceArg = value
				sourceKinds[dateSourceReference] = struct{}{}
			case "set":
				var err error
				value, i, err = dateRequireValue(inv, args, i, name, value, hasValue)
				if err != nil {
					return dateOptions{}, err
				}
				opts.setValue = value
				setSeen = true
			case "resolution":
				if hasValue {
					return dateOptions{}, dateUsageError(inv, "option '--resolution' doesn't allow an argument")
				}
				opts.source = dateSourceResolution
				sourceKinds[dateSourceResolution] = struct{}{}
			case "rfc-email":
				if hasValue {
					return dateOptions{}, dateUsageError(inv, "option '--rfc-email' doesn't allow an argument")
				}
				opts.format = dateFormatRFCEmail
			case "rfc-3339":
				var err error
				value, i, err = dateRequireValue(inv, args, i, name, value, hasValue)
				if err != nil {
					return dateOptions{}, err
				}
				opts.format = dateFormatRFC3339
				opts.formatArg = value
			case "iso-8601":
				opts.format = dateFormatISO8601
				if hasValue {
					opts.formatArg = value
				} else {
					opts.formatArg = ""
				}
			default:
				return dateOptions{}, dateUsageError(inv, "unrecognized option '--%s'", name)
			}
			continue
		}
		if parsing && strings.HasPrefix(arg, "-") && arg != "-" {
			shorts := arg[1:]
			for j := 0; j < len(shorts); j++ {
				ch := shorts[j]
				rest := shorts[j+1:]
				switch ch {
				case 'u':
					opts.utc = true
				case 'R':
					opts.format = dateFormatRFCEmail
				case 'd', 'f', 'r', 's':
					var value string
					if rest != "" {
						value = rest
					} else {
						if i+1 >= len(args) {
							return dateOptions{}, dateUsageError(inv, "option requires an argument -- '%c'", ch)
						}
						i++
						value = args[i]
					}
					switch ch {
					case 'd':
						opts.source = dateSourceString
						opts.sourceArg = value
						sourceKinds[dateSourceString] = struct{}{}
					case 'f':
						opts.source = dateSourceFile
						opts.sourceArg = value
						sourceKinds[dateSourceFile] = struct{}{}
					case 'r':
						opts.source = dateSourceReference
						opts.sourceArg = value
						sourceKinds[dateSourceReference] = struct{}{}
					case 's':
						opts.setValue = value
						setSeen = true
					}
					j = len(shorts)
				case 'I':
					opts.format = dateFormatISO8601
					if rest != "" {
						rest = strings.TrimPrefix(rest, "=")
						opts.formatArg = rest
					}
					j = len(shorts)
				default:
					return dateOptions{}, dateUsageError(inv, "invalid option -- '%c'", ch)
				}
			}
			continue
		}
		positionals = append(positionals, arg)
	}

	if opts.help || opts.version {
		return opts, nil
	}

	if opts.format == dateFormatISO8601 {
		value, err := normalizeDateISOPrecision(opts.formatArg)
		if err != nil {
			return dateOptions{}, exitf(inv, 1, "date: invalid argument %q for '--iso-8601'", opts.formatArg)
		}
		opts.formatArg = value
	}
	if opts.format == dateFormatRFC3339 {
		value, err := normalizeDateRFC3339Precision(opts.formatArg)
		if err != nil {
			return dateOptions{}, exitf(inv, 1, "date: invalid argument %q for '--rfc-3339'", opts.formatArg)
		}
		opts.formatArg = value
	}

	if len(sourceKinds) > 1 || (len(sourceKinds) > 0 && setSeen) {
		return dateOptions{}, dateUsageError(inv, "the options to specify dates for printing are mutually exclusive")
	}

	if len(positionals) > 1 {
		return dateOptions{}, exitf(inv, 1, "date: extra operand %s\nTry 'date --help' for more information.", quoteGNUOperand(positionals[1]))
	}
	if len(positionals) == 1 {
		arg := positionals[0]
		switch {
		case strings.HasPrefix(arg, "+"):
			opts.format = dateFormatCustom
			opts.formatArg = arg[1:]
		case opts.source != dateSourceNow || opts.setValue != "":
			return dateOptions{}, exitf(inv, 1, "date: the argument %s lacks a leading '+';\nwhen using an option to specify date(s), any non-option\nargument must be a format string beginning with '+'\nTry 'date --help' for more information.", quoteGNUOperand(arg))
		case isDateLegacyTimestamp(arg):
			opts.setValue = arg
			opts.legacySet = true
		default:
			return dateOptions{}, exitf(inv, 1, "date: invalid date %q", arg)
		}
	}
	if len(sourceKinds) > 0 && opts.legacySet {
		return dateOptions{}, dateUsageError(inv, "the options to specify dates for printing are mutually exclusive")
	}

	if opts.source == dateSourceResolution && opts.format == dateFormatDefault {
		opts.format = dateFormatResolution
	}
	return opts, nil
}

func dateRequireValue(inv *Invocation, args []string, index int, longName, value string, hasValue bool) (string, int, error) {
	if hasValue {
		return value, index, nil
	}
	if index+1 >= len(args) {
		return "", index, dateUsageError(inv, "option '--%s' requires an argument", longName)
	}
	return args[index+1], index + 1, nil
}

func normalizeDateISOPrecision(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "date":
		return "date", nil
	case "hour", "hours":
		return "hours", nil
	case "minute", "minutes":
		return "minutes", nil
	case "second", "seconds":
		return "seconds", nil
	case "ns":
		return "ns", nil
	default:
		return "", fmt.Errorf("invalid")
	}
}

func normalizeDateRFC3339Precision(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "date":
		return "date", nil
	case "second", "seconds":
		return "seconds", nil
	case "ns":
		return "ns", nil
	default:
		return "", fmt.Errorf("invalid")
	}
}

func parseDateSetValue(value string, now time.Time, loc *time.Location) (time.Time, dateParseInfo, error) {
	if isDateLegacyTimestamp(value) {
		parsed, err := parseDateLegacyTimestamp(value, now, loc)
		if err != nil {
			return time.Time{}, dateParseInfo{}, err
		}
		return parsed, buildDateParseInfo(value, parsed, false), nil
	}
	return parseDateValue(value, now, loc)
}

func runDateFileSource(ctx context.Context, inv *Invocation, opts *dateOptions, now time.Time, loc *time.Location) error {
	var (
		reader io.Reader
		closer io.Closer
	)
	if opts.sourceArg == "-" {
		reader = inv.Stdin
		if reader == nil {
			reader = strings.NewReader("")
		}
	} else {
		info, _, err := statPath(ctx, inv, opts.sourceArg)
		if err != nil {
			return exitf(inv, 1, "date: %s: %s", opts.sourceArg, readAllErrorText(err))
		}
		if info.IsDir() {
			return exitf(inv, 1, "date: %s: Is a directory", opts.sourceArg)
		}
		file, _, err := openRead(ctx, inv, opts.sourceArg)
		if err != nil {
			return exitf(inv, 1, "date: %s: %s", opts.sourceArg, readAllErrorText(err))
		}
		reader = file
		closer = file
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}

	scanner := bufio.NewScanner(reader)
	status := 0
	for scanner.Scan() {
		line := scanner.Text()
		parsed, info, err := parseDateValue(line, now, loc)
		if err != nil {
			_, _ = fmt.Fprintf(inv.Stderr, "date: invalid date %q\n", line)
			status = 1
			continue
		}
		dateWriteDebug(inv, opts.debug, info)
		if err := writeDateOutput(inv, dateOutputTime(parsed, loc, opts.utc), opts); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return exitf(inv, 1, "date: %s", err)
	}
	if status != 0 {
		return &ExitError{Code: status}
	}
	return nil
}

func dateOutputTime(parsed time.Time, loc *time.Location, utc bool) time.Time {
	if utc {
		return parsed.UTC()
	}
	return parsed.In(loc)
}

func writeDateOutput(inv *Invocation, current time.Time, opts *dateOptions) error {
	text, err := formatDateOutput(current, opts)
	if err != nil {
		return exitf(inv, 1, "date: %v", err)
	}
	if _, err := fmt.Fprintln(inv.Stdout, text); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func formatDateOutput(current time.Time, opts *dateOptions) (string, error) {
	switch opts.format {
	case dateFormatCustom:
		return formatDateString(current, opts.formatArg)
	case dateFormatISO8601:
		switch opts.formatArg {
		case "date":
			return current.Format("2006-01-02"), nil
		case "hours":
			return current.Format("2006-01-02T15-07:00"), nil
		case "minutes":
			return current.Format("2006-01-02T15:04-07:00"), nil
		case "seconds":
			return current.Format("2006-01-02T15:04:05-07:00"), nil
		case "ns":
			return current.Format("2006-01-02T15:04:05") + "," + fmt.Sprintf("%09d", current.Nanosecond()) + current.Format("-07:00"), nil
		default:
			return "", fmt.Errorf("invalid iso-8601 precision")
		}
	case dateFormatRFCEmail:
		return current.Format(time.RFC1123Z), nil
	case dateFormatRFC3339:
		switch opts.formatArg {
		case "date":
			return current.Format("2006-01-02"), nil
		case "seconds":
			return current.Format("2006-01-02 15:04:05-07:00"), nil
		case "ns":
			return current.Format("2006-01-02 15:04:05") + "." + fmt.Sprintf("%09d", current.Nanosecond()) + current.Format("-07:00"), nil
		default:
			return "", fmt.Errorf("invalid rfc-3339 precision")
		}
	case dateFormatResolution:
		return "0.000000001", nil
	default:
		return current.Format("Mon Jan _2 15:04:05 MST 2006"), nil
	}
}

func dateWriteDebug(inv *Invocation, enabled bool, info dateParseInfo) {
	if inv == nil || inv.Stderr == nil || !enabled {
		return
	}
	_, _ = fmt.Fprintf(inv.Stderr, "date: input string: %q\n", info.Input)
	_, _ = fmt.Fprintf(inv.Stderr, "date: parsed date part: %s\n", info.DatePart)
	_, _ = fmt.Fprintf(inv.Stderr, "date: parsed time part: %s\n", info.TimePart)
	_, _ = fmt.Fprintf(inv.Stderr, "date: input timezone: %s\n", info.ZonePart)
	if info.UsedMidnight {
		_, _ = io.WriteString(inv.Stderr, "date: warning: using midnight as starting time: 00:00:00\n")
	}
}

func dateUsageError(inv *Invocation, format string, args ...any) error {
	return exitf(inv, 1, "date: %s\nTry 'date --help' for more information.", fmt.Sprintf(format, args...))
}

var _ Command = (*Date)(nil)
