package builtins

import (
	"context"
	"fmt"
	"slices"
)

type Help struct{}

type helpTopic struct {
	Synopsis string
	Body     string
}

var builtinHelp = map[string]helpTopic{
	"cd": {
		Synopsis: "cd [dir]",
		Body:     "Change the virtual current directory.",
	},
	"echo": {
		Synopsis: "echo [arg ...]",
		Body:     "Write arguments to standard output.",
	},
	"export": {
		Synopsis: "export NAME[=VALUE] ...",
		Body:     "Set shell variables for child commands.",
	},
	"help": {
		Synopsis: "help [-s] [pattern]",
		Body:     "Display shell builtin help.",
	},
	"pwd": {
		Synopsis: "pwd [-L|-P]",
		Body:     "Print the current working directory, honoring logical and physical modes.",
	},
}

func NewHelp() *Help {
	return &Help{}
}

func (c *Help) Name() string {
	return "help"
}

func (c *Help) Run(_ context.Context, inv *Invocation) error {
	short := false
	args := inv.Args
	for len(args) > 0 {
		switch args[0] {
		case "-s":
			short = true
			args = args[1:]
		case "--":
			args = args[1:]
			goto done
		default:
			goto done
		}
	}
done:
	if len(args) == 0 {
		_, _ = fmt.Fprintln(inv.Stdout, "gbash shell builtins:")
		names := make([]string, 0, len(builtinHelp))
		for name := range builtinHelp {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			_, _ = fmt.Fprintln(inv.Stdout, name)
		}
		return nil
	}
	topic, ok := builtinHelp[args[0]]
	if !ok {
		return exitf(inv, 1, "help: no help topics match %q", args[0])
	}
	if short {
		_, _ = fmt.Fprintf(inv.Stdout, "%s: %s\n", args[0], topic.Synopsis)
		return nil
	}
	_, _ = fmt.Fprintf(inv.Stdout, "%s\n\n%s\n", topic.Synopsis, topic.Body)
	return nil
}

var _ Command = (*Help)(nil)
