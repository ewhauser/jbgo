package commands

import (
	"context"
	"io"
)

type Cat struct{}

func NewCat() *Cat {
	return &Cat{}
}

func (c *Cat) Name() string {
	return "cat"
}

func (c *Cat) Run(ctx context.Context, inv *Invocation) error {
	if len(inv.Args) == 0 {
		_, err := io.Copy(inv.Stdout, inv.Stdin)
		if err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		return nil
	}

	for _, name := range inv.Args {
		file, _, err := openRead(ctx, inv, name)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(inv.Stdout, file)
		closeErr := file.Close()
		if copyErr != nil {
			return &ExitError{Code: 1, Err: copyErr}
		}
		if closeErr != nil {
			return &ExitError{Code: 1, Err: closeErr}
		}
	}

	return nil
}

var _ Command = (*Cat)(nil)
