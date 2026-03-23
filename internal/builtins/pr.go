package builtins

import (
	"context"
	"io"
)

type Pr struct{}

func NewPr() *Pr {
	return &Pr{}
}

func (c *Pr) Name() string {
	return "pr"
}

func (c *Pr) Run(ctx context.Context, inv *Invocation) error {
	if inv == nil {
		return nil
	}
	if len(inv.Args) == 1 && inv.Args[0] == "--version" {
		_, err := io.WriteString(inv.Stdout, "pr (gbash) dev\n")
		return err
	}
	if len(inv.Args) == 1 && inv.Args[0] == "--help" {
		_, err := io.WriteString(inv.Stdout, "Usage: pr [FILE]...\n")
		return err
	}

	if len(inv.Args) == 0 {
		_, err := io.Copy(inv.Stdout, inv.Stdin)
		return err
	}
	for _, name := range inv.Args {
		if name == "-" {
			if _, err := io.Copy(inv.Stdout, inv.Stdin); err != nil {
				return err
			}
			continue
		}
		file, _, err := openRead(ctx, inv, name)
		if err != nil {
			return exitf(inv, 1, "pr: %s: %s", name, readAllErrorText(err))
		}
		_, copyErr := io.Copy(inv.Stdout, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

var _ Command = (*Pr)(nil)
