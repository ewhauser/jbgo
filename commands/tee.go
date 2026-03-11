package commands

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cadencerpm/just-bash-go/policy"
)

type Tee struct{}

func NewTee() *Tee {
	return &Tee{}
}

func (c *Tee) Name() string {
	return "tee"
}

func (c *Tee) Run(ctx context.Context, inv *Invocation) error {
	if len(inv.Args) > 0 && inv.Args[0] == "--help" {
		_, _ = fmt.Fprintln(inv.Stdout, "usage: tee [-a] [FILE...]")
		_, _ = fmt.Fprintln(inv.Stdout, "copy stdin to stdout and to each FILE")
		return nil
	}
	appendMode, files, err := parseTeeArgs(inv)
	if err != nil {
		return err
	}
	data, err := readAllStdin(inv)
	if err != nil {
		return err
	}
	if _, err := inv.Stdout.Write(data); err != nil {
		return &ExitError{Code: 1, Err: err}
	}

	flag := os.O_CREATE | os.O_WRONLY
	if appendMode {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}

	for _, name := range files {
		abs, err := allowPath(ctx, inv, policy.FileActionWrite, name)
		if err != nil {
			return err
		}
		if err := ensureParentDirExists(ctx, inv, abs); err != nil {
			return err
		}
		file, err := inv.FS.OpenFile(ctx, abs, flag, 0o644)
		if err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		if _, err := file.Write(data); err != nil {
			_ = file.Close()
			return &ExitError{Code: 1, Err: err}
		}
		_ = file.Close()
		recordFileMutation(inv.Trace, map[bool]string{true: "append", false: "write"}[appendMode], abs, abs, abs)
	}
	return nil
}

func parseTeeArgs(inv *Invocation) (appendMode bool, files []string, err error) {
	args := inv.Args
	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			args = args[1:]
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			break
		}
		switch arg {
		case "-a", "--append":
			appendMode = true
		default:
			return false, nil, exitf(inv, 1, "tee: unsupported flag %s", arg)
		}
		args = args[1:]
	}
	return appendMode, args, nil
}

var _ Command = (*Tee)(nil)
