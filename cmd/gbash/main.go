package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	exitCode, err := runCLI(context.Background(), os.Args[0], os.Args[1:], os.Stdin, os.Stdout, os.Stderr, stdinIsTTY(os.Stdin))
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "gbash: %v\n", err)
	}
	os.Exit(exitCode)
}
