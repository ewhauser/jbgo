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
	"complete": {
		Synopsis: "complete [-pr] [-D] [-o option] [-A action] [-W wordlist] [-F function] [-C command] [name ...]",
		Body:     "Set, print, or remove programmable completion specifications.",
	},
	"compgen": {
		Synopsis: "compgen [-abcdefjkve] [-o option] [-A action] [-W wordlist] [-F function] [-C command] [-X filterpat] [-P prefix] [-S suffix] [word]",
		Body:     "Generate completion matches directly.",
	},
	"compopt": {
		Synopsis: "compopt [-o option] [+o option] [name ...]",
		Body:     "Modify programmable completion options for a command or completion scope.",
	},
	"echo": {
		Synopsis: "echo [arg ...]",
		Body:     "Write arguments to standard output.",
	},
	"export": {
		Synopsis: "export NAME[=VALUE] ...",
		Body:     "Set shell variables for child commands.",
	},
	"false": {
		Synopsis: "false",
		Body:     "Return a non-zero exit status.",
	},
	"help": {
		Synopsis: "help [-s] [pattern]",
		Body:     "Display shell builtin help.",
	},
	"history": {
		Synopsis: "history [-c] [n]",
		Body:     "Display or clear the current shell history.",
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
