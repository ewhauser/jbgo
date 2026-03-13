package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/commands"
	"github.com/klauspost/compress/zstd"
)

func main() {
	ctx := context.Background()

	registry := commands.DefaultRegistry()
	must(registry.Register(newZstdCommand("zstd")))
	must(registry.RegisterLazy("zstd-lazy", func() (commands.Command, error) {
		return newZstdCommand("zstd-lazy"), nil
	}))

	script, err := io.ReadAll(os.Stdin)
	if err != nil {
		fail(fmt.Errorf("read script: %w", err))
	}
	if strings.TrimSpace(string(script)) == "" {
		_, _ = fmt.Fprintln(os.Stderr, "usage: go run ./custom-zstd < ./custom-zstd/demo.sh")
		os.Exit(2)
	}

	rt, err := gbash.New(gbash.WithRegistry(registry))
	if err != nil {
		fail(fmt.Errorf("create runtime: %w", err))
	}

	result, err := rt.Run(ctx, &gbash.ExecutionRequest{
		Name:   "custom-zstd",
		Script: string(script),
	})
	if err != nil {
		fail(fmt.Errorf("run script: %w", err))
	}
	if result.Stdout != "" {
		_, _ = os.Stdout.WriteString(result.Stdout)
	}
	if result.Stderr != "" {
		_, _ = os.Stderr.WriteString(result.Stderr)
	}
	os.Exit(result.ExitCode)
}

func newZstdCommand(name string) commands.Command {
	return commands.DefineCommand(name, func(ctx context.Context, inv *commands.Invocation) error {
		opts, err := parseZstdArgs(name, inv)
		if err != nil {
			return err
		}
		if opts == nil {
			return nil
		}
		if opts.about {
			names := inv.GetRegisteredCommands()
			_, err := fmt.Fprintf(inv.Stdout, "name=%s cwd=%s registered=%t total=%d max-file-bytes=%d\n",
				name,
				inv.Cwd,
				slices.Contains(names, name),
				len(names),
				inv.Limits.MaxFileBytes,
			)
			return err
		}
		return runZstd(ctx, name, inv, opts)
	})
}

type zstdOptions struct {
	decompress bool
	output     string
	input      string
	about      bool
}

func parseZstdArgs(name string, inv *commands.Invocation) (*zstdOptions, error) {
	opts := &zstdOptions{}
	args := inv.Args
	for len(args) > 0 {
		arg := args[0]
		switch arg {
		case "--":
			args = args[1:]
			goto done
		case "-d", "--decompress":
			opts.decompress = true
			args = args[1:]
		case "-o", "--output":
			if len(args) < 2 {
				return nil, commands.Exitf(inv, 1, "%s: missing value for %s", name, arg)
			}
			opts.output = args[1]
			args = args[2:]
		case "--about":
			opts.about = true
			args = args[1:]
		case "-h", "--help":
			_, _ = fmt.Fprintf(inv.Stdout, "usage: %s [-d] [-o FILE] [FILE]\n", name)
			_, _ = fmt.Fprintf(inv.Stdout, "       %s --about\n", name)
			return nil, nil
		default:
			if strings.HasPrefix(arg, "-") && arg != "-" {
				return nil, commands.Exitf(inv, 1, "%s: unsupported flag %s", name, arg)
			}
			goto done
		}
	}

done:
	if len(args) > 1 {
		return nil, commands.Exitf(inv, 1, "%s: expected at most one input file", name)
	}
	if len(args) == 1 {
		opts.input = args[0]
	}
	return opts, nil
}

func runZstd(ctx context.Context, name string, inv *commands.Invocation, opts *zstdOptions) error {
	input, closeInput, err := openZstdInput(ctx, inv, opts.input)
	if err != nil {
		return err
	}
	defer closeInput()

	output, closeOutput, err := openZstdOutput(ctx, inv, opts.output)
	if err != nil {
		return err
	}
	defer closeOutput()

	if opts.decompress {
		decoder, err := zstd.NewReader(input)
		if err != nil {
			return commands.Exitf(inv, 1, "%s: %v", name, err)
		}
		defer decoder.Close()
		if _, err := io.Copy(output, decoder); err != nil {
			return commands.Exitf(inv, 1, "%s: %v", name, err)
		}
		return nil
	}

	encoder, err := zstd.NewWriter(output)
	if err != nil {
		return commands.Exitf(inv, 1, "%s: %v", name, err)
	}
	if _, err := io.Copy(encoder, input); err != nil {
		_ = encoder.Close()
		return commands.Exitf(inv, 1, "%s: %v", name, err)
	}
	if err := encoder.Close(); err != nil {
		return commands.Exitf(inv, 1, "%s: %v", name, err)
	}
	return nil
}

func openZstdInput(ctx context.Context, inv *commands.Invocation, name string) (io.Reader, func(), error) {
	if name == "" || name == "-" {
		return inv.Stdin, func() {}, nil
	}
	file, err := inv.FS.Open(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	return file, func() { _ = file.Close() }, nil
}

func openZstdOutput(ctx context.Context, inv *commands.Invocation, name string) (io.Writer, func(), error) {
	if name == "" || name == "-" {
		return inv.Stdout, func() {}, nil
	}
	parent := path.Dir(inv.FS.Resolve(name))
	if parent != "." && parent != "/" {
		if err := inv.FS.MkdirAll(ctx, parent, 0o755); err != nil {
			return nil, nil, err
		}
	}
	file, err := inv.FS.OpenFile(ctx, name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, nil, err
	}
	return file, func() { _ = file.Close() }, nil
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func fail(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
