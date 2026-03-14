package builtins

import (
	"context"
	"fmt"
	"strings"
)

type Vdir struct{}

func NewVdir() *Vdir {
	return &Vdir{}
}

func (c *Vdir) Name() string {
	return "vdir"
}

func (c *Vdir) Run(ctx context.Context, inv *Invocation) error {
	spec := vdirSpec()
	matches, action, err := ParseCommandSpec(inv, &spec)
	if err != nil {
		if code, ok := ExitCode(err); ok && code == 1 {
			return &ExitError{Code: 2, Err: err}
		}
		return err
	}
	switch action {
	case "help":
		return renderStaticHelp(vdirHelpText())(inv.Stdout, spec)
	case "version":
		return renderStaticVersion(vdirVersionText)(inv.Stdout, spec)
	}
	return runVdirParsed(ctx, inv, matches)
}

func vdirSpec() CommandSpec {
	return CommandSpec{
		Name:  "vdir",
		Usage: "vdir [OPTION]... [FILE]...",
		Options: append(lsOptionSpecs(),
			OptionSpec{Name: "version", Long: "version", Help: "show version information"},
		),
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			GroupShortOptions:     true,
			LongOptionValueEquals: true,
		},
		HelpRenderer:    renderStaticHelp(vdirHelpText()),
		VersionRenderer: renderStaticVersion(vdirVersionText),
	}
}

func runVdirParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("help") {
		return renderStaticHelp(vdirHelpText())(inv.Stdout, vdirSpec())
	}
	if matches.Has("version") {
		return renderStaticVersion(vdirVersionText)(inv.Stdout, vdirSpec())
	}

	opts, err := lsOptionsFromParsed(inv, matches)
	if err != nil {
		return err
	}
	if !vdirHasExplicitQuoting(matches) {
		opts.quotingMode = lsQuoteEscape
	}
	if !lsHasExplicitFormat(matches) {
		opts.longFormat = true
	}

	targets := matches.Args("file")
	if len(targets) == 0 {
		targets = []string{"."}
	}

	var stdout strings.Builder
	exitCode := 0
	lister := &Dir{}
	for i, target := range targets {
		if i > 0 && stdout.Len() > 0 && !strings.HasSuffix(stdout.String(), "\n\n") {
			stdout.WriteByte('\n')
		}

		output, status, _, err := lister.listPath(ctx, inv, "vdir", target, &opts, len(targets) > 1, false)
		if err != nil {
			return err
		}
		stdout.WriteString(output)
		if status > exitCode {
			exitCode = status
		}
	}

	if _, err := fmt.Fprint(inv.Stdout, stdout.String()); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func vdirHelpText() string {
	return strings.Replace(lsHelpText, "ls - list directory contents\n\nUsage:\n  ls [OPTION]... [FILE]...", "vdir - list directory contents\n\nUsage:\n  vdir [OPTION]... [FILE]...", 1)
}

func vdirHasExplicitQuoting(matches *ParsedCommand) bool {
	for _, option := range matches.OptionOrder() {
		switch option {
		case "literal", "escape", "quote-name", "quoting-style":
			return true
		}
	}
	return false
}

const vdirVersionText = "vdir (gbash) dev\n"

var _ Command = (*Vdir)(nil)
