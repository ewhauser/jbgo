package builtins

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Timeout struct{}

type timeoutOptions struct {
	duration       time.Duration
	killAfter      time.Duration
	hasKillAfter   bool
	signal         timeoutSignal
	foreground     bool
	preserveStatus bool
	verbose        bool
	command        []string
}

type timeoutSignal struct {
	number int
	name   string
}

func NewTimeout() *Timeout {
	return &Timeout{}
}

func (c *Timeout) Name() string {
	return "timeout"
}

func (c *Timeout) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Timeout) Spec() CommandSpec {
	return CommandSpec{
		Name:  "timeout",
		About: "Start COMMAND, and kill it if still running after DURATION.",
		Usage: "timeout [OPTION] DURATION COMMAND [ARG]...",
		Options: []OptionSpec{
			{Name: "foreground", Short: 'f', Long: "foreground", Help: "when not running timeout directly from a shell prompt, allow COMMAND to read from the TTY and get TTY signals"},
			{Name: "kill-after", Short: 'k', Long: "kill-after", Arity: OptionRequiredValue, ValueName: "DURATION", Help: "also send a KILL signal if COMMAND is still running this long after the initial signal was sent"},
			{Name: "preserve-status", Short: 'p', Long: "preserve-status", Help: "exit with the same status as COMMAND, even when the command times out"},
			{Name: "signal", Short: 's', Long: "signal", Arity: OptionRequiredValue, ValueName: "SIGNAL", Help: "specify the signal to be sent on timeout"},
			{Name: "verbose", Short: 'v', Long: "verbose", Help: "diagnose to stderr any signal sent upon timeout"},
		},
		Args: []ArgSpec{
			{Name: "duration", ValueName: "DURATION", Required: true},
			{Name: "command", ValueName: "COMMAND", Repeatable: true, Required: true},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			StopAtFirstPositional:    true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
	}
}

func (c *Timeout) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := parseTimeoutMatches(inv, matches)
	if err != nil {
		return err
	}
	result, err := executeCommand(ctx, inv, &executeCommandOptions{
		Argv:    opts.command,
		Env:     inv.Env,
		WorkDir: inv.Cwd,
		Stdin:   inv.Stdin,
		Stdout:  inv.Stdout,
		Stderr:  inv.Stderr,
		Timeout: opts.duration,
	})
	if err != nil {
		return err
	}
	timedOut := result != nil && result.ExitCode == 124 && result.ControlStderr != ""
	if timedOut && opts.verbose {
		if err := timeoutWriteVerbose(inv, &opts); err != nil {
			return err
		}
	}
	if result != nil && result.ControlStderr != "" {
		if _, err := fmt.Fprintln(inv.Stderr, result.ControlStderr); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if timedOut {
		if opts.preserveStatus {
			return &ExitError{Code: 128 + opts.signal.number}
		}
		if opts.signal.number == 0 && opts.hasKillAfter {
			return &ExitError{Code: 137}
		}
	}
	return exitForExecutionResult(result)
}

func parseTimeoutMatches(inv *Invocation, matches *ParsedCommand) (timeoutOptions, error) {
	duration, err := parseTimeoutDuration(matches.Arg("duration"))
	if err != nil {
		return timeoutOptions{}, exitf(inv, 125, "timeout: invalid time interval %q", matches.Arg("duration"))
	}
	signal, err := parseTimeoutSignal(matches.Value("signal"))
	if err != nil {
		return timeoutOptions{}, exitf(inv, 125, "timeout: invalid signal %q", matches.Value("signal"))
	}
	var killAfter time.Duration
	hasKillAfter := false
	if matches.Has("kill-after") {
		killAfter, err = parseTimeoutDuration(matches.Value("kill-after"))
		if err != nil {
			return timeoutOptions{}, exitf(inv, 125, "timeout: invalid time interval %q", matches.Value("kill-after"))
		}
		hasKillAfter = true
	}
	opts := timeoutOptions{
		duration:       duration,
		killAfter:      killAfter,
		hasKillAfter:   hasKillAfter,
		signal:         signal,
		foreground:     matches.Has("foreground"),
		preserveStatus: matches.Has("preserve-status"),
		verbose:        matches.Has("verbose"),
		command:        matches.Args("command"),
	}
	// The current sandbox execution engine only exposes a single timeout duration.
	// Preserve the accepted uutils/GNU flag surface here even though foreground
	// handling does not yet change process-group behavior in this environment.
	_ = opts.foreground
	return opts, nil
}

func (c *Timeout) NormalizeParseError(inv *Invocation, err error) error {
	if err == nil {
		return nil
	}
	return exitf(inv, 125, "%s", err.Error())
}

func parseTimeoutDuration(value string) (time.Duration, error) {
	if value == "" {
		return 0, fmt.Errorf("invalid time interval %q", value)
	}
	multiplier := time.Second
	last := value[len(value)-1]
	switch last {
	case 's':
		multiplier = time.Second
		value = value[:len(value)-1]
	case 'm':
		multiplier = time.Minute
		value = value[:len(value)-1]
	case 'h':
		multiplier = time.Hour
		value = value[:len(value)-1]
	case 'd':
		multiplier = 24 * time.Hour
		value = value[:len(value)-1]
	case 'D':
		return 0, fmt.Errorf("invalid time interval %q", value+"D")
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || number < 0 {
		return 0, fmt.Errorf("invalid time interval %q", value)
	}
	duration := time.Duration(number * float64(multiplier))
	if duration == 0 && number > 0 {
		return time.Nanosecond, nil
	}
	return duration, nil
}

func parseTimeoutSignal(value string) (timeoutSignal, error) {
	if value == "" {
		return timeoutSignal{number: 15, name: "TERM"}, nil
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return timeoutSignal{}, fmt.Errorf("invalid signal")
	}
	if number, err := strconv.Atoi(trimmed); err == nil {
		return timeoutSignal{number: number, name: timeoutSignalName(number)}, nil
	}
	normalized := strings.ToUpper(strings.TrimPrefix(trimmed, "SIG"))
	number, ok := timeoutSignalNumbers[normalized]
	if !ok {
		return timeoutSignal{}, fmt.Errorf("invalid signal")
	}
	return timeoutSignal{number: number, name: normalized}, nil
}

func timeoutSignalName(number int) string {
	for name, value := range timeoutSignalNumbers {
		if value == number {
			return name
		}
	}
	return strconv.Itoa(number)
}

func timeoutWriteVerbose(inv *Invocation, opts *timeoutOptions) error {
	if len(opts.command) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(inv.Stderr, "timeout: sending signal %s to command %q\n", opts.signal.name, opts.command[0]); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if opts.signal.number == 0 && opts.hasKillAfter {
		if _, err := fmt.Fprintf(inv.Stderr, "timeout: sending signal KILL to command %q\n", opts.command[0]); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

var timeoutSignalNumbers = map[string]int{
	"0":    0,
	"ALRM": 14,
	"CONT": 18,
	"HUP":  1,
	"INT":  2,
	"KILL": 9,
	"PIPE": 13,
	"QUIT": 3,
	"TERM": 15,
	"USR1": 10,
	"USR2": 12,
}

var _ Command = (*Timeout)(nil)
var _ SpecProvider = (*Timeout)(nil)
var _ ParsedRunner = (*Timeout)(nil)
var _ ParseErrorNormalizer = (*Timeout)(nil)
