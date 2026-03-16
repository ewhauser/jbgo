package builtins

import (
	"context"
	"encoding/binary"
	"fmt"
	stdfs "io/fs"
	"path"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/ewhauser/gbash/internal/commandutil"
)

const (
	whoRecordSize = 384

	whoRunLevelType    = 1
	whoBootTimeType    = 2
	whoNewTimeType     = 3
	whoOldTimeType     = 4
	whoInitProcessType = 5
	whoLoginType       = 6
	whoUserType        = 7
	whoDeadType        = 8
)

type Who struct{}

type whoOptions struct {
	file            string
	shortList       bool
	shortOutput     bool
	includeIdle     bool
	includeHeading  bool
	includeMesg     bool
	includeExit     bool
	needBootTime    bool
	needDeadProcs   bool
	needLogin       bool
	needInitSpawn   bool
	needClockChange bool
	needRunLevel    bool
	needUsers       bool
	myLineOnly      bool
}

type whoRecord struct {
	recordType int16
	pid        int32
	line       string
	id         string
	user       string
	host       string
	timestamp  int64
	exitTerm   int16
	exitStatus int16
}

func NewWho() *Who {
	return &Who{}
}

func (c *Who) Name() string {
	return "who"
}

func (c *Who) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Who) Spec() CommandSpec {
	return CommandSpec{
		Name:  c.Name(),
		About: "Print information about users who are currently logged in.",
		Usage: "who [OPTION]... [ FILE | ARG1 ARG2 ]",
		AfterHelp: fmt.Sprintf(
			"If FILE is not specified, use %s. /var/log/wtmp as FILE is common.\nIf ARG1 ARG2 are given, -m is presumed: 'am i' or 'mom likes' are usual.",
			whoDefaultFile(),
		),
		Options: []OptionSpec{
			{Name: "all", Short: 'a', Long: "all", Help: "same as -b -d --login -p -r -t -T -u"},
			{Name: "boot", Short: 'b', Long: "boot", Help: "time of last system boot"},
			{Name: "dead", Short: 'd', Long: "dead", Help: "print dead processes"},
			{Name: "heading", Short: 'H', Long: "heading", Help: "print line of column headings"},
			{Name: "login", Short: 'l', Long: "login", Help: "print system login processes"},
			{Name: "lookup", Long: "lookup", Help: "attempt to canonicalize hostnames via DNS (unsupported in gbash)"},
			{Name: "only-hostname-user", Short: 'm', Help: "only hostname and user associated with stdin"},
			{Name: "process", Short: 'p', Long: "process", Help: "print active processes spawned by init"},
			{Name: "count", Short: 'q', Long: "count", Help: "all login names and number of users logged on"},
			{Name: "runlevel", Short: 'r', Long: "runlevel", Help: whoRunlevelHelp()},
			{Name: "short", Short: 's', Long: "short", Help: "print only name, line, and time (default)"},
			{Name: "time", Short: 't', Long: "time", Help: "print last system clock change"},
			{Name: "users", Short: 'u', Long: "users", Help: "list users logged in"},
			{Name: "mesg", Short: 'T', ShortAliases: []rune{'w'}, Long: "mesg", Aliases: []string{"message", "writable"}, Help: "add user's message status as +, - or ?"},
		},
		Args: []ArgSpec{
			{Name: "arg1", ValueName: "FILE"},
			{Name: "arg2", ValueName: "ARG2"},
		},
		Parse: ParseConfig{
			InferLongOptions:  true,
			GroupShortOptions: true,
			AutoHelp:          true,
			AutoVersion:       true,
		},
	}
}

func (c *Who) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("lookup") {
		return exitf(inv, 1, "who: --lookup is unsupported in this sandbox")
	}
	opts := whoOptionsFromParsed(matches)
	records, err := whoReadRecords(ctx, inv, opts.file)
	if err != nil {
		return err
	}

	if opts.shortList {
		return whoWriteShortList(inv, records)
	}

	if opts.includeHeading {
		if err := whoWriteHeading(inv, opts); err != nil {
			return err
		}
	}

	curTTY := ""
	if opts.myLineOnly {
		curTTY = whoCurrentTTY(inv)
	}

	for i := range records {
		record := &records[i]
		if opts.myLineOnly && curTTY != record.line {
			continue
		}
		switch {
		case opts.needUsers && record.isUserProcess():
			if err := whoWriteUser(ctx, inv, opts, record); err != nil {
				return err
			}
		default:
			var writeErr error
			switch record.recordType {
			case whoRunLevelType:
				if opts.needRunLevel && runtime.GOOS == "linux" {
					writeErr = whoWriteRunlevel(inv, opts, record)
				}
			case whoBootTimeType:
				if opts.needBootTime {
					writeErr = whoWriteNamedRecord(inv, opts, "", "system boot", record, "", "")
				}
			case whoNewTimeType:
				if opts.needClockChange {
					writeErr = whoWriteNamedRecord(inv, opts, "", "clock change", record, "", "")
				}
			case whoInitProcessType:
				if opts.needInitSpawn {
					comment := fmt.Sprintf("id=%s", record.id)
					writeErr = whoWriteNamedRecord(inv, opts, "", record.line, record, fmt.Sprintf("%d", record.pid), comment)
				}
			case whoLoginType:
				if opts.needLogin {
					comment := fmt.Sprintf("id=%s", record.id)
					writeErr = whoWriteNamedRecord(inv, opts, "LOGIN", record.line, record, fmt.Sprintf("%d", record.pid), comment)
				}
			case whoDeadType:
				if opts.needDeadProcs {
					comment := fmt.Sprintf("id=%s", record.id)
					exitInfo := fmt.Sprintf("term=%d exit=%d", record.exitTerm, record.exitStatus)
					writeErr = whoWriteLine(inv, opts, "", ' ', record.line, whoTimeString(inv.Env, record.timestamp), "", fmt.Sprintf("%d", record.pid), comment, exitInfo)
				}
			}
			if writeErr != nil {
				return writeErr
			}
		}
	}

	return nil
}

func whoOptionsFromParsed(matches *ParsedCommand) whoOptions {
	arg1 := matches.Arg("arg1")
	arg2 := matches.Arg("arg2")
	files := make([]string, 0, 2)
	if arg1 != "" {
		files = append(files, arg1)
	}
	if arg2 != "" {
		files = append(files, arg2)
	}

	all := matches.Has("all")
	needBootTime := all || matches.Has("boot")
	needDeadProcs := all || matches.Has("dead")
	needLogin := all || matches.Has("login")
	needInitSpawn := all || matches.Has("process")
	needClockChange := all || matches.Has("time")
	needRunLevel := all || matches.Has("runlevel")
	useDefaults := !all &&
		!needBootTime &&
		!needDeadProcs &&
		!needLogin &&
		!needInitSpawn &&
		!needRunLevel &&
		!needClockChange &&
		!matches.Has("users")
	needUsers := all || matches.Has("users") || useDefaults
	includeExit := needDeadProcs

	file := whoDefaultFile()
	if len(files) == 1 {
		file = files[0]
	}

	return whoOptions{
		file:            file,
		shortList:       matches.Has("count"),
		shortOutput:     !includeExit && useDefaults,
		includeIdle:     needDeadProcs || needLogin || needRunLevel || needUsers,
		includeHeading:  matches.Has("heading"),
		includeMesg:     all || matches.Has("mesg"),
		includeExit:     includeExit,
		needBootTime:    needBootTime,
		needDeadProcs:   needDeadProcs,
		needLogin:       needLogin,
		needInitSpawn:   needInitSpawn,
		needClockChange: needClockChange,
		needRunLevel:    needRunLevel,
		needUsers:       needUsers,
		myLineOnly:      matches.Has("only-hostname-user") || len(files) == 2,
	}
}

func whoRunlevelHelp() string {
	if runtime.GOOS == "linux" {
		return "print current runlevel"
	}
	return "print current runlevel (This is meaningless on non Linux)"
}

func whoDefaultFile() string {
	switch runtime.GOOS {
	case "darwin", "netbsd":
		return "/var/run/utmpx"
	default:
		return "/var/run/utmp"
	}
}

func whoReadRecords(ctx context.Context, inv *Invocation, file string) ([]whoRecord, error) {
	data, _, err := readAllFile(ctx, inv, file)
	if err != nil {
		if exitCodeForError(err) == 126 {
			return nil, err
		}
		return nil, nil
	}
	return whoParseRecords(data), nil
}

func whoParseRecords(data []byte) []whoRecord {
	records := make([]whoRecord, 0, len(data)/whoRecordSize)
	for offset := 0; offset+whoRecordSize <= len(data); offset += whoRecordSize {
		record := data[offset : offset+whoRecordSize]
		records = append(records, whoRecord{
			recordType: int16(binary.NativeEndian.Uint16(record[0:2])),
			pid:        int32(binary.NativeEndian.Uint32(record[4:8])),
			line:       whoCString(record[8:40]),
			id:         whoCString(record[40:44]),
			user:       whoCString(record[44:76]),
			host:       whoCString(record[76:332]),
			exitTerm:   int16(binary.NativeEndian.Uint16(record[332:334])),
			exitStatus: int16(binary.NativeEndian.Uint16(record[334:336])),
			timestamp:  int64(int32(binary.NativeEndian.Uint32(record[340:344]))),
		})
	}
	return records
}

func whoCString(data []byte) string {
	if idx := bytesIndex(data, 0); idx >= 0 {
		return string(data[:idx])
	}
	return string(data)
}

func bytesIndex(data []byte, target byte) int {
	for i, b := range data {
		if b == target {
			return i
		}
	}
	return -1
}

func (r *whoRecord) isUserProcess() bool {
	return r.recordType == whoUserType && r.user != ""
}

func whoWriteShortList(inv *Invocation, records []whoRecord) error {
	users := make([]string, 0, len(records))
	for _, record := range records {
		if record.isUserProcess() {
			users = append(users, record.user)
		}
	}
	if _, err := fmt.Fprintln(inv.Stdout, strings.Join(users, " ")); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if _, err := fmt.Fprintf(inv.Stdout, "# users=%d\n", len(users)); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func whoWriteHeading(inv *Invocation, opts whoOptions) error {
	return whoWriteLine(inv, opts, "NAME", ' ', "LINE", "TIME", "IDLE", "PID", "COMMENT", "EXIT")
}

func whoWriteUser(ctx context.Context, inv *Invocation, opts whoOptions, record *whoRecord) error {
	mesg := '?'
	idle := "  ?"
	if record.line != "" {
		info, _, err := statPath(ctx, inv, path.Join("/dev", record.line))
		if err == nil {
			if info.Mode().Perm()&0o020 == 0 {
				mesg = '-'
			} else {
				mesg = '+'
			}
			if access, ok := whoIdleTimestamp(info); ok {
				idle = whoIdleString(access)
			}
		}
	}

	host := record.host
	comment := ""
	if host != "" {
		comment = "(" + host + ")"
	}

	return whoWriteLine(
		inv,
		opts,
		record.user,
		mesg,
		record.line,
		whoTimeString(inv.Env, record.timestamp),
		idle,
		fmt.Sprintf("%d", record.pid),
		comment,
		"",
	)
}

func whoWriteRunlevel(inv *Invocation, opts whoOptions, record *whoRecord) error {
	last := byte(record.pid / 256)
	curr := byte(record.pid % 256)
	comment := ""
	if last >= 32 && last != 127 {
		prev := 'N'
		if last == 'N' {
			prev = 'S'
		}
		comment = fmt.Sprintf("last=%c", prev)
	}
	return whoWriteLine(
		inv,
		opts,
		"",
		' ',
		fmt.Sprintf("run-level %c", curr),
		whoTimeString(inv.Env, record.timestamp),
		"",
		"",
		comment,
		"",
	)
}

func whoWriteNamedRecord(inv *Invocation, opts whoOptions, user, line string, record *whoRecord, pid, comment string) error {
	return whoWriteLine(inv, opts, user, ' ', line, whoTimeString(inv.Env, record.timestamp), "", pid, comment, "")
}

func whoWriteLine(inv *Invocation, opts whoOptions, user string, state rune, line, when, idle, pid, comment, exit string) error {
	var b strings.Builder

	fmt.Fprintf(&b, "%-8s", user)
	if opts.includeMesg {
		b.WriteByte(' ')
		b.WriteRune(state)
	}
	fmt.Fprintf(&b, " %-12s", line)
	fmt.Fprintf(&b, " %-12s", when)
	if !opts.shortOutput {
		if opts.includeIdle {
			fmt.Fprintf(&b, " %-6s", idle)
		}
		fmt.Fprintf(&b, " %10s", pid)
	}
	fmt.Fprintf(&b, " %-8s", comment)
	if opts.includeExit {
		fmt.Fprintf(&b, " %-12s", exit)
	}

	if _, err := fmt.Fprintln(inv.Stdout, strings.TrimRight(b.String(), " ")); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func whoCurrentTTY(inv *Invocation) string {
	if inv == nil || inv.Stdin == nil {
		return whoEnvTTY(inv)
	}
	meta, ok := inv.Stdin.(commandutil.RedirectMetadata)
	if !ok {
		return whoEnvTTY(inv)
	}
	p := strings.TrimSpace(meta.RedirectPath())
	if !strings.HasPrefix(path.Clean(p), "/dev/") {
		return whoEnvTTY(inv)
	}
	return strings.TrimPrefix(path.Clean(p), "/dev/")
}

func whoEnvTTY(inv *Invocation) string {
	if inv == nil || inv.Env == nil {
		return ""
	}
	tty := strings.TrimSpace(inv.Env["TTY"])
	if tty == "" {
		return ""
	}
	tty = path.Clean(tty)
	if stripped, ok := strings.CutPrefix(tty, "/dev/"); ok {
		return stripped
	}
	return tty
}

func whoIdleString(lastChange int64) string {
	now := time.Now().Unix()
	if lastChange > 0 && lastChange <= now && now-lastChange < 24*3600 {
		secondsIdle := now - lastChange
		if secondsIdle < 60 {
			return "  .  "
		}
		return fmt.Sprintf("%02d:%02d", secondsIdle/3600, (secondsIdle%3600)/60)
	}
	return " old"
}

func whoTimeString(env map[string]string, timestamp int64) string {
	when := time.Unix(timestamp, 0).Local()
	if whoUseCLocale(env) {
		return when.Format("Jan _2 15:04")
	}
	return when.Format("2006-01-02 15:04")
}

func whoUseCLocale(env map[string]string) bool {
	for _, key := range []string{"LC_ALL", "LC_TIME", "LANG"} {
		if env != nil {
			if strings.TrimSpace(env[key]) == "C" {
				return true
			}
			if strings.TrimSpace(env[key]) != "" {
				return false
			}
		}
	}
	return false
}

func whoIdleTimestamp(info stdfs.FileInfo) (int64, bool) {
	if access, ok := whoAccessTime(info); ok {
		return access.Unix(), true
	}
	if mod := info.ModTime(); !mod.IsZero() {
		return mod.Unix(), true
	}
	return 0, false
}

func whoAccessTime(info stdfs.FileInfo) (time.Time, bool) {
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
	if field := sys.FieldByName("Atim"); field.IsValid() {
		return whoTimespec(field)
	}
	if field := sys.FieldByName("Atimespec"); field.IsValid() {
		return whoTimespec(field)
	}
	if sec := sys.FieldByName("Atime"); sec.IsValid() {
		nsec := sys.FieldByName("AtimeNsec")
		return time.Unix(int64(whoUintField(sec)), int64(whoUintField(nsec))), true
	}
	return time.Time{}, false
}

func whoTimespec(value reflect.Value) (time.Time, bool) {
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
	return time.Unix(int64(whoUintField(sec)), int64(whoUintField(nsec))), true
}

func whoUintField(value reflect.Value) uint64 {
	switch value.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return uint64(value.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return value.Uint()
	default:
		return 0
	}
}

var _ Command = (*Who)(nil)
var _ SpecProvider = (*Who)(nil)
var _ ParsedRunner = (*Who)(nil)
