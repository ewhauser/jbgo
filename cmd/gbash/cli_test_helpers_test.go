package main

import (
	"context"
	"io"

	rootcli "github.com/ewhauser/gbash/cli"
)

const continuationPrompt = "> "

func runCLI(ctx context.Context, argv0 string, args []string, stdin io.Reader, stdout, stderr io.Writer, stdinTTY bool) (int, error) {
	return runCLIWithConfig(ctx, newCLIConfig(), argv0, args, stdin, stdout, stderr, stdinTTY)
}

func runCLIWithConfig(ctx context.Context, cfg rootcli.Config, argv0 string, args []string, stdin io.Reader, stdout, stderr io.Writer, stdinTTY bool) (int, error) {
	cfg.TTYDetector = func(io.Reader) bool { return stdinTTY }
	return rootcli.Run(ctx, cfg, argv0, args, stdin, stdout, stderr)
}
