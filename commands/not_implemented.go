package commands

import "context"

// Reserve uutils/coreutils command names that the runtime doesn't implement yet so
// shell resolution and compatibility harnesses get a deterministic error
// instead of "command not found".
type NotImplemented struct {
	name string
}

func NewNotImplemented(name string) *NotImplemented {
	return &NotImplemented{name: name}
}

func (c *NotImplemented) Name() string {
	return c.name
}

func (c *NotImplemented) Run(_ context.Context, inv *Invocation) error {
	return Exitf(inv, 1, "%s: not implemented", c.name)
}

var gnuCoreutilsNotImplementedNames = []string{
	"arch",
	"basenc",
	"chcon",
	"chgrp",
	"chroot",
	"cksum",
	"csplit",
	"dd",
	"df",
	"dircolors",
	"expand",
	"factor",
	"fmt",
	"fold",
	"groups",
	"hostid",
	"hostname",
	"install",
	"kill",
	"logname",
	"mkfifo",
	"mknod",
	"mktemp",
	"nice",
	"nohup",
	"nproc",
	"numfmt",
	"pathchk",
	"pinky",
	"pr",
	"ptx",
	"realpath",
	"runcon",
	"shred",
	"shuf",
	"stdbuf",
	"stty",
	"sum",
	"sync",
	"truncate",
	"tsort",
	"tty",
	"uname",
	"unexpand",
	"unlink",
	"users",
	"vdir",
	"who",
}

func gnuCoreutilsNotImplementedCommands() []Command {
	cmds := make([]Command, 0, len(gnuCoreutilsNotImplementedNames))
	for _, name := range gnuCoreutilsNotImplementedNames {
		cmds = append(cmds, NewNotImplemented(name))
	}
	return cmds
}

var _ Command = (*NotImplemented)(nil)
