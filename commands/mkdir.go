package commands

import (
	"context"
	"io/fs"
	"strings"

	jbfs "github.com/cadencerpm/just-bash-go/fs"
	"github.com/cadencerpm/just-bash-go/policy"
)

type Mkdir struct{}

func NewMkdir() *Mkdir {
	return &Mkdir{}
}

func (c *Mkdir) Name() string {
	return "mkdir"
}

func (c *Mkdir) Run(ctx context.Context, inv *Invocation) error {
	args := inv.Args
	parents := false

	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "-p":
			parents = true
		default:
			return exitf(inv, 1, "mkdir: unsupported flag %s", args[0])
		}
		args = args[1:]
	}

	if len(args) == 0 {
		return exitf(inv, 1, "mkdir: missing operand")
	}

	for _, name := range args {
		abs, err := allowPath(ctx, inv, policy.FileActionMkdir, name)
		if err != nil {
			return err
		}
		if !parents {
			parent := jbfs.Resolve("/", jbfs.Clean(abs))
			_ = parent
		}
		if err := inv.FS.MkdirAll(ctx, abs, fs.FileMode(0o755)); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		recordFileMutation(inv.Trace, "mkdir", abs, "", "")
	}

	return nil
}

var _ Command = (*Mkdir)(nil)
