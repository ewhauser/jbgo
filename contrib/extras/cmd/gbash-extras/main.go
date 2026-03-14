package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ewhauser/gbash/cli"
)

func main() {
	cfg := newCLIConfig()
	exitCode, err := cli.Run(context.Background(), cfg, os.Args[0], os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%s: %v\n", cfg.Name, err)
	}
	os.Exit(exitCode)
}
