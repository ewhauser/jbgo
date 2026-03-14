package builtins

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
	return RunCommand(ctx, c, inv)
}

func (c *Uptime) Spec() CommandSpec {
	return CommandSpec{
		Name:  "uptime",
		About: "Tell how long the system has been running.",
		Usage: "uptime [OPTION]... [FILE]",
		Options: []OptionSpec{
			{Name: "since", Short: 's', Long: "since", Help: "system up since, in yyyy-mm-dd HH:MM:SS format"},
			{Name: "pretty", Short: 'p', Long: "pretty", Help: "show uptime in pretty format"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions: true,
			AutoHelp:         true,
			AutoVersion:      true,
		},
	}
}

func (c *Uptime) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := parseUptimeMatches(inv, matches)
	if err != nil {
		return err
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
	pretty bool
	since  bool
	path   string
}

func parseUptimeMatches(inv *Invocation, matches *ParsedCommand) (uptimeOptions, error) {
	files := matches.Args("file")
	if len(files) > 1 {
		return uptimeOptions{}, exitf(inv, 1, "uptime: unexpected value '%s'", files[1])
	}
	opts := uptimeOptions{
		pretty: matches.Has("pretty"),
		since:  matches.Has("since"),
	}
	if len(files) == 1 {
		opts.path = files[0]
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
var _ SpecProvider = (*Uptime)(nil)
var _ ParsedRunner = (*Uptime)(nil)
