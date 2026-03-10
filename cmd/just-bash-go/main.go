package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	exitCode, err := runCLI(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr, stdinIsTTY(os.Stdin))
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "just-bash-go: %v\n", err)
	}
	os.Exit(exitCode)
}
