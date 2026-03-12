package commands

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"strconv"
	"strings"
	"time"
)

const (
	uptimeDefaultUsers      = 1
	uptimeDefaultLoadAvg    = "load average: 0.00, 0.00, 0.00"
	uptimeUnknownUptimeText = "up ???? days ??:??,"
	uptimeBootEnvKey        = "GBASH_SESSION_BOOT_AT"
	uptimeRecordSize        = 384
	uptimeBootTimeType      = 2
	uptimeUserProcessType   = 7
)

type Uptime struct{}

func NewUptime() *Uptime {
	return &Uptime{}
}

func (c *Uptime) Name() string {
	return "uptime"
}

func (c *Uptime) Run(ctx context.Context, inv *Invocation) error {
	opts, err := parseUptimeArgs(inv)
	if err != nil {
		return err
	}

	switch opts.mode {
	case "help":
		_, err := fmt.Fprintln(inv.Stdout, "usage: uptime [-p|--pretty] [-s|--since] [FILE]")
		if err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		return nil
	case "version":
		_, err := fmt.Fprintln(inv.Stdout, "uptime (gbash)")
		if err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		return nil
	}

	now := time.Now()
	bootAt := uptimeBootTimeFromEnv(inv.Env)

	if opts.path == "" {
		switch {
		case opts.since:
			return uptimeWriteLine(inv.Stdout, bootAt.Local().Format("2006-01-02 15:04:05"))
		case opts.pretty:
			return uptimeWriteLine(inv.Stdout, "up "+uptimeFormatPretty(now.Sub(bootAt)))
		default:
			line := fmt.Sprintf(" %s  up  %s,  %s,  %s",
				now.Format("15:04:05"),
				uptimeFormatDefault(now.Sub(bootAt)),
				uptimeFormatUserCount(uptimeDefaultUsers),
				uptimeDefaultLoadAvg,
			)
			return uptimeWriteLine(inv.Stdout, line)
		}
	}

	info, abs, err := lstatPath(ctx, inv, opts.path)
	if err != nil {
		return c.writeFallback(inv, fmt.Sprintf("uptime: couldn't get boot time: %s", uptimeIOMessage(err)))
	}
	if info.IsDir() {
		return c.writeFallback(inv, "uptime: couldn't get boot time: Is a directory")
	}
	if info.Mode()&stdfs.ModeNamedPipe != 0 {
		return c.writeFallback(inv, "uptime: couldn't get boot time: Illegal seek")
	}

	data, _, err := readAllFile(ctx, inv, abs)
	if err != nil {
		return c.writeFallback(inv, fmt.Sprintf("uptime: couldn't get boot time: %s", uptimeIOMessage(err)))
	}
	parsedBootAt, userCount, parseErr := uptimeParseUtmp(data)
	switch {
	case opts.since && parseErr == nil:
		return uptimeWriteLine(inv.Stdout, parsedBootAt.Local().Format("2006-01-02 15:04:05"))
	case opts.since:
		return c.writeFallback(inv, "uptime: couldn't get boot time")
	case opts.pretty:
		if parseErr != nil {
			return c.writeFallback(inv, "uptime: couldn't get boot time")
		}
		return uptimeWriteLine(inv.Stdout, "up "+uptimeFormatPretty(now.Sub(parsedBootAt)))
	case parseErr != nil:
		return c.writeFallback(inv, "uptime: couldn't get boot time")
	default:
		line := fmt.Sprintf(" %s  up  %s,  %s,  %s",
			now.Format("15:04:05"),
			uptimeFormatDefault(now.Sub(parsedBootAt)),
			uptimeFormatUserCount(userCount),
			uptimeDefaultLoadAvg,
		)
		return uptimeWriteLine(inv.Stdout, line)
	}
}

func (c *Uptime) writeFallback(inv *Invocation, stderrLine string) error {
	if _, err := fmt.Fprintln(inv.Stderr, stderrLine); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	line := fmt.Sprintf(" %s  %s  %s,  %s",
		time.Now().Format("15:04:05"),
		uptimeUnknownUptimeText,
		uptimeFormatUserCount(0),
		uptimeDefaultLoadAvg,
	)
	if err := uptimeWriteLine(inv.Stdout, line); err != nil {
		return err
	}
	return &ExitError{Code: 1}
}

type uptimeOptions struct {
	mode   string
	pretty bool
	since  bool
	path   string
}

func parseUptimeArgs(inv *Invocation) (uptimeOptions, error) {
	args := append([]string(nil), inv.Args...)
	opts := uptimeOptions{}

	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		switch {
		case arg == "--help":
			opts.mode = "help"
			return opts, nil
		case arg == "--version":
			opts.mode = "version"
			return opts, nil
		case arg == "-p" || arg == "--pretty":
			opts.pretty = true
		case arg == "-s" || arg == "--since":
			opts.since = true
		case arg == "--":
			if len(args) > 1 {
				return uptimeOptions{}, exitf(inv, 1, "uptime: unexpected value '%s'", args[1])
			}
			if len(args) == 1 {
				opts.path = args[0]
			}
			return opts, nil
		case strings.HasPrefix(arg, "--"):
			return uptimeOptions{}, exitf(inv, 1, "uptime: unrecognized option '%s'", arg)
		case strings.HasPrefix(arg, "-") && arg != "-":
			return uptimeOptions{}, exitf(inv, 1, "uptime: invalid option -- '%s'", strings.TrimPrefix(arg, "-"))
		case opts.path == "":
			opts.path = arg
		default:
			return uptimeOptions{}, exitf(inv, 1, "uptime: unexpected value '%s'", arg)
		}
	}

	return opts, nil
}

func uptimeBootTimeFromEnv(env map[string]string) time.Time {
	if env != nil {
		if raw := strings.TrimSpace(env[uptimeBootEnvKey]); raw != "" {
			if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
				return parsed
			}
		}
	}
	return time.Now().UTC()
}

func uptimeWriteLine(w io.Writer, line string) error {
	if _, err := fmt.Fprintln(w, line); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func uptimeFormatDefault(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	totalMinutes := int(duration / time.Minute)
	days := totalMinutes / (24 * 60)
	hours := (totalMinutes / 60) % 24
	minutes := totalMinutes % 60
	clock := fmt.Sprintf("%d:%02d", hours, minutes)
	if days == 0 {
		return clock
	}
	if days == 1 {
		return fmt.Sprintf("1 day, %s", clock)
	}
	return fmt.Sprintf("%d days, %s", days, clock)
}

func uptimeFormatPretty(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	totalMinutes := int(duration / time.Minute)
	days := totalMinutes / (24 * 60)
	hours := (totalMinutes / 60) % 24
	minutes := totalMinutes % 60
	parts := make([]string, 0, 3)
	if days > 0 {
		parts = append(parts, uptimePlural(days, "day"))
	}
	if hours > 0 {
		parts = append(parts, uptimePlural(hours, "hour"))
	}
	if minutes > 0 || len(parts) == 0 {
		parts = append(parts, uptimePlural(minutes, "minute"))
	}
	return strings.Join(parts, ", ")
}

func uptimePlural(count int, singular string) string {
	if count == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(count) + " " + singular + "s"
}

func uptimeFormatUserCount(count int) string {
	if count == 1 {
		return "1 user"
	}
	return fmt.Sprintf("%d users", count)
}

func uptimeIOMessage(err error) string {
	switch {
	case errors.Is(err, stdfs.ErrNotExist):
		return "No such file or directory"
	default:
		return err.Error()
	}
}

func uptimeParseUtmp(data []byte) (time.Time, int, error) {
	if len(data) < uptimeRecordSize {
		return time.Time{}, 0, errors.New("missing utmp records")
	}
	userCount := 0
	var bootTime int64
	for offset := 0; offset+uptimeRecordSize <= len(data); offset += uptimeRecordSize {
		record := data[offset : offset+uptimeRecordSize]
		recordType := int32(binary.NativeEndian.Uint32(record[0:4]))
		switch recordType {
		case uptimeBootTimeType:
			bootTime = int64(int32(binary.NativeEndian.Uint32(record[340:344])))
		case uptimeUserProcessType:
			userCount++
		}
	}
	if bootTime <= 0 {
		return time.Time{}, userCount, errors.New("missing boot time")
	}
	return time.Unix(bootTime, 0).UTC(), userCount, nil
}

var _ Command = (*Uptime)(nil)
