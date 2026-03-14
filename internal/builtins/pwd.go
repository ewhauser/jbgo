package builtins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

type Pwd struct{}

type pwdOptions struct {
	logical  bool
	physical bool
}

func NewPwd() *Pwd {
	return &Pwd{}
}

func (c *Pwd) Name() string {
	return "pwd"
}

func (c *Pwd) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Pwd) Spec() CommandSpec {
	return CommandSpec{
		Name:  "pwd",
		About: "Print the full filename of the current working directory.",
		Usage: "pwd [OPTION]...",
		Options: []OptionSpec{
			{Name: "logical", Short: 'L', Long: "logical", Help: "use PWD from environment, even if it contains symlinks"},
			{Name: "physical", Short: 'P', Long: "physical", Help: "avoid all symlinks"},
			{Name: "help", Long: "help", Help: "display this help and exit"},
			{Name: "version", Long: "version", Help: "output version information and exit"},
		},
		Parse: ParseConfig{
			InferLongOptions:      true,
			GroupShortOptions:     true,
			StopAtFirstPositional: true,
		},
		VersionRenderer: func(w io.Writer, _ CommandSpec) error {
			_, err := io.WriteString(w, pwdVersionText)
			return err
		},
	}
}

func (c *Pwd) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("help") {
		return RenderCommandHelp(inv.Stdout, &matches.Spec)
	}
	if matches.Has("version") {
		return RenderCommandVersion(inv.Stdout, &matches.Spec)
	}

	opts := parsePwdMatches(matches)
	cwd, err := resolvePwdOutput(ctx, inv, opts)
	if err != nil {
		return exitf(inv, 1, "pwd: failed to get current directory: %s", pwdErrorDetail(err))
	}
	if _, err := fmt.Fprintln(inv.Stdout, cwd); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func parsePwdMatches(matches *ParsedCommand) pwdOptions {
	return pwdOptions{
		logical:  matches.Has("logical"),
		physical: matches.Has("physical"),
	}
}

func resolvePwdOutput(ctx context.Context, inv *Invocation, opts pwdOptions) (string, error) {
	useLogical := opts.logical || (!opts.physical && inv != nil && inv.Env["POSIXLY_CORRECT"] != "")
	if opts.physical || !useLogical {
		return pwdPhysicalPath(ctx, inv)
	}
	return pwdLogicalPath(ctx, inv)
}

func pwdPhysicalPath(ctx context.Context, inv *Invocation) (string, error) {
	if inv == nil || inv.FS == nil {
		return "/", nil
	}
	return inv.FS.Realpath(ctx, ".")
}

func pwdLogicalPath(ctx context.Context, inv *Invocation) (string, error) {
	if inv != nil {
		if candidate, ok := inv.Env["PWD"]; ok && pwdLooksReasonable(ctx, inv, candidate) {
			return candidate, nil
		}
	}
	return pwdPhysicalPath(ctx, inv)
}

func pwdLooksReasonable(ctx context.Context, inv *Invocation, candidate string) bool {
	if !path.IsAbs(candidate) {
		return false
	}
	for piece := range strings.SplitSeq(candidate, "/") {
		if piece == "." || piece == ".." {
			return false
		}
	}
	return pwdMatchesCurrentDir(ctx, inv, candidate)
}

func pwdMatchesCurrentDir(ctx context.Context, inv *Invocation, candidate string) bool {
	if inv == nil || inv.FS == nil {
		return false
	}

	candidateInfo, candidateErr := inv.FS.Stat(ctx, candidate)
	currentInfo, currentErr := inv.FS.Stat(ctx, ".")
	if candidateErr == nil && currentErr == nil && os.SameFile(candidateInfo, currentInfo) {
		return true
	}

	candidateReal, candidateRealErr := inv.FS.Realpath(ctx, candidate)
	currentReal, currentRealErr := inv.FS.Realpath(ctx, ".")
	if candidateRealErr != nil || currentRealErr != nil {
		return false
	}
	return candidateReal == currentReal
}

func pwdErrorDetail(err error) string {
	var exitErr *ExitError
	if errors.As(err, &exitErr) && exitErr.Err != nil {
		err = exitErr.Err
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && pathErr.Err != nil {
		return pathErr.Err.Error()
	}
	if err == nil {
		return "unknown error"
	}
	return err.Error()
}

const pwdVersionText = `pwd (gbash)
`

var _ Command = (*Pwd)(nil)
