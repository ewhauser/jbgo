package commands

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Date struct{}

func NewDate() *Date {
	return &Date{}
}

func (c *Date) Name() string {
	return "date"
}

func (c *Date) Run(_ context.Context, inv *Invocation) error {
	current, formatKind, formatText, err := parseDateArgs(inv)
	if err != nil {
		return err
	}
	var output string
	switch formatKind {
	case "help":
		_, _ = fmt.Fprintln(inv.Stdout, "usage: date [-u] [-d STRING] [+FORMAT] [-I|-R]")
		return nil
	case "iso":
		output = current.Format("2006-01-02T15:04:05-0700")
	case "rfc":
		output = current.Format(time.RFC1123Z)
	case "custom":
		output, err = formatDateString(current, formatText)
		if err != nil {
			return exitf(inv, 1, "date: %v", err)
		}
	default:
		output = current.Format("Mon Jan _2 15:04:05 UTC 2006")
	}
	_, err = fmt.Fprintln(inv.Stdout, output)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func parseDateArgs(inv *Invocation) (current time.Time, formatKind, formatText string, err error) {
	args := inv.Args
	current = time.Now().UTC()
	for len(args) > 0 {
		arg := args[0]
		switch {
		case arg == "--help":
			return current, "help", "", nil
		case arg == "-u":
			args = args[1:]
		case arg == "-I":
			formatKind = "iso"
			args = args[1:]
		case arg == "-R":
			formatKind = "rfc"
			args = args[1:]
		case arg == "-d":
			if len(args) < 2 {
				return time.Time{}, "", "", exitf(inv, 1, "date: option requires an argument -- 'd'")
			}
			current, err = parseDateInput(args[1], current)
			if err != nil {
				return time.Time{}, "", "", exitf(inv, 1, "date: invalid date %q", args[1])
			}
			args = args[2:]
		case strings.HasPrefix(arg, "--date="):
			current, err = parseDateInput(strings.TrimPrefix(arg, "--date="), current)
			if err != nil {
				return time.Time{}, "", "", exitf(inv, 1, "date: invalid date %q", strings.TrimPrefix(arg, "--date="))
			}
			args = args[1:]
		case len(arg) > 1 && arg[0] == '+':
			formatKind = "custom"
			formatText = arg[1:]
			args = args[1:]
		default:
			return time.Time{}, "", "", exitf(inv, 1, "date: unsupported argument %s", arg)
		}
	}
	return current, formatKind, formatText, nil
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
