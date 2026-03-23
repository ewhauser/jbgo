package builtins

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// defaultNproc is the deterministic processor count reported inside the
// sandbox.  There is no real hardware to query, so we pick a small,
// realistic default.
const defaultNproc = 2

type Nproc struct{}

func NewNproc() *Nproc {
	return &Nproc{}
}

func (c *Nproc) Name() string {
	return "nproc"
}

func (c *Nproc) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Nproc) Spec() CommandSpec {
	return CommandSpec{
		Name:  c.Name(),
		About: "Print the number of processing units available",
		Usage: "nproc [OPTION]...",
		Options: []OptionSpec{
			{
				Name: "all",
				Long: "all",
				Help: "print the number of installed processors",
			},
			{
				Name:      "ignore",
				Long:      "ignore",
				Arity:     OptionRequiredValue,
				ValueName: "N",
				Help:      "if possible, exclude N processing units",
			},
			{
				Name:  "version",
				Short: 'V',
				Long:  "version",
				Help:  "output version information and exit",
			},
		},
		Parse: ParseConfig{
			InferLongOptions:      true,
			LongOptionValueEquals: true,
			AutoHelp:              true,
		},
	}
}

func (c *Nproc) RunParsed(_ context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("version") {
		return RenderSimpleVersion(inv.Stdout, c.Name())
	}
	if positionals := matches.Positionals(); len(positionals) > 0 {
		return commandUsageError(inv, c.Name(), "extra operand %s", quoteGNUOperand(positionals[0]))
	}

	var ignore int
	if matches.Has("ignore") {
		v := matches.Value("ignore")
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 0 {
			return exitf(inv, 1, "nproc: invalid number: %s", quoteGNUOperand(v))
		}
		ignore = n
	}

	all := matches.Has("all")

	cores := defaultNproc
	if !all {
		// OMP_NUM_THREADS overrides the available count (but not --all).
		// GNU takes the first comma-separated value; 0 and parse errors
		// are rejected (fall back to default).
		if raw := strings.TrimSpace(inv.Env["OMP_NUM_THREADS"]); raw != "" {
			first, _, _ := strings.Cut(raw, ",")
			if n, err := strconv.Atoi(strings.TrimSpace(first)); err == nil && n > 0 {
				cores = n
			}
		}
	}

	// OMP_THREAD_LIMIT caps the result (0 and parse errors are ignored).
	// Like OMP_NUM_THREADS, the limit is ignored when --all is set.
	if !all {
		if raw := strings.TrimSpace(inv.Env["OMP_THREAD_LIMIT"]); raw != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && n > 0 {
				if n < cores {
					cores = n
				}
			}
		}
	}

	if cores <= ignore {
		cores = 1
	} else {
		cores -= ignore
	}

	if _, err := fmt.Fprintln(inv.Stdout, cores); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

var _ Command = (*Nproc)(nil)
var _ SpecProvider = (*Nproc)(nil)
var _ ParsedRunner = (*Nproc)(nil)
