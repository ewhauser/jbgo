package commands

import (
	"fmt"
	"slices"
	"sync"
)

type CommandRegistry interface {
	Register(cmd Command) error
	Lookup(name string) (Command, bool)
	Names() []string
}

type Registry struct {
	mu       sync.RWMutex
	commands map[string]Command
}

func NewRegistry(cmds ...Command) *Registry {
	registry := &Registry{
		commands: make(map[string]Command),
	}
	for _, cmd := range cmds {
		_ = registry.Register(cmd)
	}
	return registry
}

func (r *Registry) Register(cmd Command) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := cmd.Name()
	if _, exists := r.commands[name]; exists {
		return fmt.Errorf("command %q already registered", name)
	}
	r.commands[name] = cmd
	return nil
}

func (r *Registry) Lookup(name string) (Command, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cmd, ok := r.commands[name]
	return cmd, ok
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.commands))
	for name := range r.commands {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func DefaultRegistry() *Registry {
	return NewRegistry(
		NewCDResolve(),
		NewEcho(),
		NewPwd(),
		NewTouch(),
		NewCat(),
		NewCP(),
		NewMV(),
		NewLN(),
		NewLS(),
		NewRmdir(),
		NewReadlink(),
		NewStat(),
		NewBasename(),
		NewDirname(),
		NewTree(),
		NewDU(),
		NewFile(),
		NewFind(),
		NewGrep(),
		NewRG(),
		NewAWK(),
		NewHead(),
		NewTail(),
		NewWC(),
		NewSort(),
		NewUniq(),
		NewCut(),
		NewSed(),
		NewPrintf(),
		NewTee(),
		NewEnv(),
		NewPrintEnv(),
		NewTrue(),
		NewFalse(),
		NewWhich(),
		NewHelp(),
		NewDate(),
		NewSleep(),
		NewTimeout(),
		NewXArgs(),
		NewBash(),
		NewSh(),
		NewComm(),
		NewPaste(),
		NewTR(),
		NewRev(),
		NewNL(),
		NewJoin(),
		NewSplit(),
		NewTac(),
		NewDiff(),
		NewBase64(),
		NewChmod(),
		NewJQ(),
		NewYQ(),
		NewSQLite3(),
		NewMkdir(),
		NewRM(),
	)
}

var _ CommandRegistry = (*Registry)(nil)
