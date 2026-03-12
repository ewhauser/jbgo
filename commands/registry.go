package commands

import (
	"context"
	"slices"
	"sync"
)

type LazyCommandLoader func() (Command, error)

type CommandRegistry interface {
	Register(cmd Command) error
	RegisterLazy(name string, loader LazyCommandLoader) error
	Lookup(name string) (Command, bool)
	Names() []string
}

type Registry struct {
	mu       sync.RWMutex
	commands map[string]Command
}

type lazyCommand struct {
	name   string
	loader LazyCommandLoader
	once   sync.Once
	cmd    Command
	err    error
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
	if cmd == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.commands[cmd.Name()] = cmd
	return nil
}

func (r *Registry) RegisterLazy(name string, loader LazyCommandLoader) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.commands[name] = &lazyCommand{
		name:   name,
		loader: loader,
	}
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

func (c *lazyCommand) Name() string {
	return c.name
}

func (c *lazyCommand) Run(ctx context.Context, inv *Invocation) error {
	cmd, err := c.load()
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return cmd.Run(ctx, inv)
}

func (c *lazyCommand) load() (Command, error) {
	c.once.Do(func() {
		if c.loader == nil {
			c.err = nil
			c.cmd = DefineCommand(c.name, nil)
			return
		}
		cmd, err := c.loader()
		if err != nil {
			c.err = err
			return
		}
		if cmd == nil {
			c.err = &ExitError{Code: 1}
			return
		}
		c.cmd = cmd
	})
	if c.err != nil {
		return nil, c.err
	}
	return c.cmd, nil
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
		NewColumn(),
		NewPaste(),
		NewTR(),
		NewRev(),
		NewNL(),
		NewJoin(),
		NewSplit(),
		NewTac(),
		NewDiff(),
		NewBase32(),
		NewBase64(),
		NewTar(),
		NewGzip(),
		NewGunzip(),
		NewZCat(),
		NewChmod(),
		NewJQ(),
		NewYQ(),
		NewSQLite3(),
		NewMkdir(),
		NewRM(),
		NewExpr(),
		NewSHA256Sum(),
	)
}

var _ CommandRegistry = (*Registry)(nil)
