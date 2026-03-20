package builtins

import (
	"context"
	"fmt"

	"github.com/ewhauser/gbash/internal/completionutil"
)

type Compgen struct{}

func NewCompgen() *Compgen {
	return &Compgen{}
}

func (c *Compgen) Name() string {
	return "compgen"
}

func (c *Compgen) Run(ctx context.Context, inv *Invocation) error {
	cfg, err := completionutil.ParseCompgenArgs(inv.Args)
	if err != nil {
		return exitf(inv, 2, err.Error())
	}
	writeCompgenWarnings(inv.Stderr, cfg)
	lines, status, err := completionutil.GenerateCompgen(completionBackend(ctx, inv), cfg)
	if err != nil {
		if status == 0 {
			status = 2
		}
		return exitf(inv, status, err.Error())
	}
	for _, line := range lines {
		if _, writeErr := fmt.Fprintln(inv.Stdout, line); writeErr != nil {
			return &ExitError{Code: 1, Err: writeErr}
		}
	}
	if status != 0 {
		return &ExitError{Code: status}
	}
	return nil
}

var _ Command = (*Compgen)(nil)

func writeCompgenWarnings(stderr any, cfg *completionutil.CompgenConfig) {
	writer, ok := stderr.(interface{ Write([]byte) (int, error) })
	if !ok || cfg == nil {
		return
	}
	if cfg.HasFunction {
		_, _ = writer.Write([]byte("compgen: warning: -F option may not work as you expect\n"))
	}
	if cfg.HasCommand {
		_, _ = writer.Write([]byte("compgen: warning: -C option may not work as you expect\n"))
	}
}
