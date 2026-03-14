package builtins

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Date struct{}

type dateOptions struct {
	current    time.Time
	formatKind string
	formatText string
}

func NewDate() *Date {
	return &Date{}
}

func (c *Date) Name() string {
	return "date"
}

func (c *Date) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Date) Spec() CommandSpec {
	return CommandSpec{
		Name:  "date",
		About: "Display the current time in the given FORMAT, or set the system date.",
		Usage: "date [-u|--utc] [-d STRING|--date STRING] [+FORMAT] [-I|--iso-8601|-R|--rfc-email]",
		Options: []OptionSpec{
			{Name: "utc", Short: 'u', Long: "utc", Aliases: []string{"universal"}, HelpAliases: []string{"universal"}, Help: "print or set Coordinated Universal Time (UTC)"},
			{Name: "date", Short: 'd', Long: "date", Arity: OptionRequiredValue, ValueName: "STRING", Help: "display time described by STRING, not 'now'"},
			{Name: "iso-8601", Short: 'I', Long: "iso-8601", Help: "output date/time in ISO 8601 format"},
			{Name: "rfc-email", Short: 'R', Long: "rfc-email", Help: "output date and time in RFC 5322 format"},
		},
		Args: []ArgSpec{
			{Name: "format", ValueName: "FORMAT", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:      true,
			GroupShortOptions:     true,
			LongOptionValueEquals: true,
			AutoHelp:              true,
			AutoVersion:           true,
		},
	}
}

func (c *Date) RunParsed(_ context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := parseDateMatches(inv, matches)
	if err != nil {
		return err
	}
	var output string
	switch opts.formatKind {
	case "iso":
		output = opts.current.Format("2006-01-02T15:04:05-0700")
	case "rfc":
		output = opts.current.Format(time.RFC1123Z)
	case "custom":
		output, err = formatDateString(opts.current, opts.formatText)
		if err != nil {
			return exitf(inv, 1, "date: %v", err)
		}
	default:
		output = opts.current.Format("Mon Jan _2 15:04:05 UTC 2006")
	}
	_, err = fmt.Fprintln(inv.Stdout, output)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func (c *Date) NormalizeInvocation(inv *Invocation) *Invocation {
	if inv == nil {
		return nil
	}
	clone := *inv
	clone.Args = normalizeDateArgs(inv.Args)
	return &clone
}

func parseDateMatches(inv *Invocation, matches *ParsedCommand) (dateOptions, error) {
	opts := dateOptions{current: time.Now().UTC()}
	if matches.Has("date") {
		current, err := parseDateInput(matches.Value("date"), opts.current)
		if err != nil {
			return dateOptions{}, exitf(inv, 1, "date: invalid date %q", matches.Value("date"))
		}
		opts.current = current
	}
	if matches.Has("iso-8601") {
		opts.formatKind = "iso"
	}
	if matches.Has("rfc-email") {
		opts.formatKind = "rfc"
	}
	for _, arg := range matches.Positionals() {
		if len(arg) > 1 && arg[0] == '+' {
			opts.formatKind = "custom"
			opts.formatText = arg[1:]
			continue
		}
		return dateOptions{}, exitf(inv, 1, "date: unsupported argument %s", arg)
	}
	return opts, nil
}

func normalizeDateArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.HasPrefix(arg, "+") && len(arg) > 1 {
			normalized = append(normalized, "--", arg)
			continue
		}
		normalized = append(normalized, arg)
	}
	return normalized
}

func parseDateInput(value string, now time.Time) (time.Time, error) {
	now = now.UTC()
	switch value {
	case "now":
		return now, nil
	case "today":
		return time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second(), 0, time.UTC), nil
	case "yesterday":
		current := now.Add(-24 * time.Hour)
		return time.Date(current.Year(), current.Month(), current.Day(), now.Hour(), now.Minute(), now.Second(), 0, time.UTC), nil
	case "tomorrow":
		current := now.Add(24 * time.Hour)
		return time.Date(current.Year(), current.Month(), current.Day(), now.Hour(), now.Minute(), now.Second(), 0, time.UTC), nil
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if parsed, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported date")
}

func formatDateString(current time.Time, format string) (string, error) {
	current = current.UTC()
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			b.WriteByte(format[i])
			continue
		}
		if i+1 >= len(format) {
			return "", fmt.Errorf("dangling %% in format")
		}
		i++
		switch format[i] {
		case '%':
			b.WriteByte('%')
		case 'Y':
			b.WriteString(current.Format("2006"))
		case 'm':
			b.WriteString(current.Format("01"))
		case 'd':
			b.WriteString(current.Format("02"))
		case 'F':
			b.WriteString(current.Format("2006-01-02"))
		case 'T':
			b.WriteString(current.Format("15:04:05"))
		case 'H':
			b.WriteString(current.Format("15"))
		case 'I':
			b.WriteString(current.Format("03"))
		case 'M':
			b.WriteString(current.Format("04"))
		case 'S':
			b.WriteString(current.Format("05"))
		case 'a':
			b.WriteString(current.Format("Mon"))
		case 'b':
			b.WriteString(current.Format("Jan"))
		case 's':
			_, _ = fmt.Fprintf(&b, "%d", current.Unix())
		case 'p':
			b.WriteString(current.Format("PM"))
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'Z':
			b.WriteString("UTC")
		case 'z':
			b.WriteString("+0000")
		default:
			return "", fmt.Errorf("unsupported format %%%c", format[i])
		}
	}
	return b.String(), nil
}

var _ Command = (*Date)(nil)
var _ SpecProvider = (*Date)(nil)
var _ ParsedRunner = (*Date)(nil)
var _ ParseInvocationNormalizer = (*Date)(nil)
