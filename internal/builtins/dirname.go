package builtins

import (
	"context"
	"fmt"
)

type Dirname struct{}

func NewDirname() *Dirname {
	return &Dirname{}
}

func (c *Dirname) Name() string {
	return "dirname"
}

func (c *Dirname) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Dirname) Spec() CommandSpec {
	return CommandSpec{
		Name:  "dirname",
		About: "Output each NAME with its last non-slash component and trailing slashes removed.",
		Usage: "dirname NAME...\n  or:  dirname OPTION NAME...",
		Options: []OptionSpec{
			{Name: "zero", Short: 'z', Long: "zero", Help: "end each output line with NUL, not newline"},
		},
		Args: []ArgSpec{
			{Name: "dir", ValueName: "NAME", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:      true,
			GroupShortOptions:     true,
			LongOptionValueEquals: true,
			AutoHelp:              true,
			AutoVersion:           true,
		},
		AfterHelp: "Examples:\n  dirname /usr/bin/\n  dirname dir1/str dir2/str",
	}
}

func (c *Dirname) RunParsed(_ context.Context, inv *Invocation, matches *ParsedCommand) error {
	terminator := "\n"
	if matches.Has("zero") {
		terminator = "\x00"
	}

	args := matches.Args("dir")
	if len(args) == 0 {
		return exitf(inv, 1, "dirname: missing operand\nTry 'dirname --help' for more information.")
	}

	for _, arg := range args {
		if _, err := fmt.Fprint(inv.Stdout, dirnameString(arg), terminator); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func dirnameString(name string) string {
	if name == "" {
		return "."
	}

	end := len(name)
	allSlashes := true
	for i := 0; i < end; i++ {
		if name[i] != '/' {
			allSlashes = false
			break
		}
	}
	if allSlashes {
		return "/"
	}

	for end > 1 && name[end-1] == '/' {
		end--
	}

	if end >= 2 && name[end-1] == '.' && name[end-2] == '/' {
		slashStart := end - 2
		for slashStart > 0 && name[slashStart-1] == '/' {
			slashStart--
		}
		if slashStart == 0 {
			if name[0] == '/' {
				return "/"
			}
			return "."
		}
		return name[:slashStart]
	}

	lastSlash := -1
	for i := end - 1; i >= 0; i-- {
		if name[i] == '/' {
			lastSlash = i
			break
		}
	}
	if lastSlash < 0 {
		return "."
	}

	result := name[:lastSlash]
	for len(result) > 1 && result[len(result)-1] == '/' {
		result = result[:len(result)-1]
	}
	if result == "" {
		return "/"
	}
	return result
}

var _ Command = (*Dirname)(nil)
var _ SpecProvider = (*Dirname)(nil)
var _ ParsedRunner = (*Dirname)(nil)
