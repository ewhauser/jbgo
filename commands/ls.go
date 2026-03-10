package commands

import (
	"context"
	"fmt"
	"path"
)

type LS struct{}

func NewLS() *LS {
	return &LS{}
}

func (c *LS) Name() string {
	return "ls"
}

func (c *LS) Run(ctx context.Context, inv *Invocation) error {
	targets := inv.Args
	if len(targets) == 0 {
		targets = []string{"."}
	}

	for _, target := range targets {
		info, abs, err := statPath(ctx, inv, target)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			if _, err := fmt.Fprintln(inv.Stdout, path.Base(abs)); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			continue
		}

		entries, _, err := readDir(ctx, inv, target)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if _, err := fmt.Fprintln(inv.Stdout, entry.Name()); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
	}

	return nil
}

var _ Command = (*LS)(nil)
