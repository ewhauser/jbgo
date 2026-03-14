package compatrun

import (
	"context"

	"github.com/ewhauser/gbash/commands"
)

var compatNotImplementedCommands = []string{
	"chcon",
	"chroot",
	"mkfifo",
	"mknod",
	"nice",
	"runcon",
	"shred",
	"stdbuf",
	"stty",
	"sync",
}

func DefaultRegistry() *commands.Registry {
	registry := commands.DefaultRegistry()
	for _, name := range compatNotImplementedCommands {
		commandName := name
		_ = registry.Register(commands.DefineCommand(commandName, func(_ context.Context, inv *commands.Invocation) error {
			return commands.Exitf(inv, 1, "%s: not implemented", commandName)
		}))
	}
	return registry
}
