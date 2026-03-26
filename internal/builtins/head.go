package builtins

import (
	"context"
	"fmt"
	"io"
)

type Head struct{}

type headMode int

const (
	headModeFirstLines headMode = iota
	headModeAllButLastLines
	headModeFirstBytes
	headModeAllButLastBytes
)

type headOptions struct {
	quiet            bool
	verbose          bool
	zeroTerminated   bool
	presumeInputPipe bool
	files            []string
	mode             headMode
	count            uint64
}

func NewHead() *Head {
	return &Head{}
}

func (c *Head) Name() string {
	return "head"
}

func (c *Head) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Head) NormalizeInvocation(inv *Invocation) *Invocation {
	if inv == nil {
		return nil
	}
	normalized := normalizeHeadArgs(inv.Args)
	if splitSliceEqual(normalized, inv.Args) {
		return inv
	}
	parseInv := *inv
	parseInv.Args = normalized
	return &parseInv
}

func (c *Head) Spec() CommandSpec {
	return CommandSpec{
		Name:  "head",
		Usage: "head [OPTION]... [FILE]...",
		Options: []OptionSpec{
			{Name: "lines", Short: 'n', Long: "lines", ValueName: "[-]NUM", Arity: OptionRequiredValue, Help: "print the first NUM lines instead of the first 10; with leading '-', print all but the last NUM lines"},
			{Name: "bytes", Short: 'c', Long: "bytes", ValueName: "[-]NUM", Arity: OptionRequiredValue, Help: "print the first NUM bytes of each file; with leading '-', print all but the last NUM bytes"},
			{Name: "quiet", Short: 'q', Long: "quiet", Aliases: []string{"silent"}, Help: "never print headers giving file names"},
			{Name: "verbose", Short: 'v', Long: "verbose", Help: "always print headers giving file names"},
			{Name: "zero-terminated", Short: 'z', Long: "zero-terminated", Help: "line delimiter is NUL, not newline"},
			{Name: "presume-input-pipe", Long: "-presume-input-pipe", Aliases: []string{"presume-input-pipe"}, Hidden: true, Help: "treat input as non-seekable even when seek is available"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			InferLongOptions:         true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
	}
}

func (c *Head) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := parseHeadMatches(inv, matches)
	if err != nil {
		return err
	}

	showHeaders := opts.verbose || (!opts.quiet && len(opts.files) > 1)
	exitCode := 0
	printedSection := false
	for _, file := range opts.files {
		var (
			reader  io.Reader
			closeFn func()
		)
		switch file {
		case "-":
			reader = inv.Stdin
			closeFn = func() {}
		default:
			handle, _, openErr := openRead(ctx, inv, file)
			if openErr != nil {
				_, _ = fmt.Fprintf(inv.Stderr, "head: cannot open %s for reading: %s\n", quoteGNUOperand(file), readAllErrorText(openErr))
				if code := exitCodeForError(openErr); code > exitCode {
					exitCode = code
				}
				continue
			}
			reader = handle
			closeFn = func() { _ = handle.Close() }
		}

		displayName := headDisplayName(file)
		if showHeaders {
			if printedSection {
				if _, err := fmt.Fprintln(inv.Stdout); err != nil {
					closeFn()
					return headStdoutWriteError(inv)
				}
			}
			if _, err := fmt.Fprintf(inv.Stdout, "==> %s <==\n", displayName); err != nil {
				closeFn()
				return headStdoutWriteError(inv)
			}
			printedSection = true
		}

		if err := headWriteFromReader(inv, reader, displayName, opts); err != nil {
			closeFn()
			return err
		}
		closeFn()
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func headDisplayName(file string) string {
	if file == "-" {
		return "standard input"
	}
	return file
}

var _ Command = (*Head)(nil)
var _ SpecProvider = (*Head)(nil)
var _ ParsedRunner = (*Head)(nil)
var _ ParseInvocationNormalizer = (*Head)(nil)
