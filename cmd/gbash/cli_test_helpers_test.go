package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	rootcli "github.com/ewhauser/gbash/cli"
)

const continuationPrompt = "> "

func runCLI(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, stdinTTY bool) (int, error) {
	return runCLIWithConfig(ctx, newCLIConfig(), args, stdin, stdout, stderr, stdinTTY)
}

func runCLIWithConfig(ctx context.Context, cfg rootcli.Config, args []string, stdin io.Reader, stdout, stderr io.Writer, stdinTTY bool) (int, error) {
	cfg.TTYDetector = func(io.Reader) bool { return stdinTTY }
	systemTempRoots, err := testSystemTempRoots(args)
	if err != nil {
		return 0, err
	}
	if len(systemTempRoots) > 0 {
		cfg.SystemTempRoots = func() []string { return append([]string(nil), systemTempRoots...) }
	}
	return rootcli.Run(ctx, cfg, "gbash", args, stdin, stdout, stderr)
}

func testSystemTempRoots(args []string) ([]string, error) {
	readWriteRoot := readWriteRootArg(args)
	if readWriteRoot == "" {
		return nil, nil
	}

	root, err := filepath.Abs(readWriteRoot)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}

	tempDir, err := filepath.Abs(os.TempDir())
	if err != nil {
		return nil, err
	}
	tempDir, err = filepath.EvalSymlinks(tempDir)
	if err != nil {
		return nil, err
	}
	if root == tempDir || strings.HasPrefix(root, tempDir+string(os.PathSeparator)) {
		return []string{tempDir}, nil
	}
	return nil, nil
}

func readWriteRootArg(args []string) string {
	for i := range args {
		if args[i] == "--readwrite-root" && i+1 < len(args) {
			return args[i+1]
		}
		if value, ok := strings.CutPrefix(args[i], "--readwrite-root="); ok {
			return value
		}
	}
	return ""
}
