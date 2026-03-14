package builtins

import (
	"bytes"
	"context"
	"fmt"
	"io"
	stdfs "io/fs"
)

type Sum struct{}

func NewSum() *Sum {
	return &Sum{}
}

func (c *Sum) Name() string {
	return "sum"
}

func (c *Sum) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Sum) Spec() CommandSpec {
	return CommandSpec{
		Name:  c.Name(),
		About: "Checksum and count the blocks in a file.\n\nWith no FILE, or when FILE is -, read standard input.",
		Usage: "sum [OPTION]... [FILE]...",
		Options: []OptionSpec{
			{Name: "bsd", Short: 'r', Help: "use the BSD sum algorithm, use 1K blocks (default)"},
			{Name: "sysv", Short: 's', Long: "sysv", Help: "use System V sum algorithm, use 512 bytes blocks"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:  true,
			GroupShortOptions: true,
			AutoHelp:          true,
			AutoVersion:       true,
		},
	}
}

func (c *Sum) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	files := matches.Args("file")
	if len(files) == 0 {
		files = []string{"-"}
	}

	useSysV := matches.Has("sysv")
	printNames := len(files) > 1 || files[0] != "-"
	failedOpen := false

	for _, name := range files {
		reader, closeFn, err := sumOpenInput(ctx, inv, name)
		if err != nil {
			reportSumOpenError(inv, name, err)
			failedOpen = true
			continue
		}

		blocks, checksum, err := sumChecksum(reader, useSysV)
		closeErr := closeFn()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			reportSumReadError(inv, name, err)
			return &ExitError{Code: 1}
		}

		if err := writeSumLine(inv.Stdout, name, printNames, useSysV, blocks, checksum); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}

	if failedOpen {
		return &ExitError{Code: 1}
	}
	return nil
}

func sumOpenInput(ctx context.Context, inv *Invocation, name string) (io.Reader, func() error, error) {
	if name == "-" {
		reader := inv.Stdin
		if reader == nil {
			reader = bytes.NewReader(nil)
		}
		handle := io.NopCloser(reader)
		return handle, handle.Close, nil
	}

	info, _, err := statPath(ctx, inv, name)
	if err != nil {
		return nil, nil, err
	}
	if info.IsDir() {
		return nil, nil, &stdfs.PathError{Op: "read", Path: name, Err: stdfs.ErrInvalid}
	}

	handle, _, err := openRead(ctx, inv, name)
	if err != nil {
		return nil, nil, err
	}
	return handle, handle.Close, nil
}

func sumChecksum(reader io.Reader, useSysV bool) (blocks uint64, checksum uint16, err error) {
	if useSysV {
		return sumChecksumSysV(reader)
	}
	return sumChecksumBSD(reader)
}

func sumChecksumBSD(reader io.Reader) (blocks uint64, checksum uint16, err error) {
	var (
		buf       [4096]byte
		bytesRead uint64
	)

	for {
		n, err := reader.Read(buf[:])
		if n > 0 {
			bytesRead += uint64(n)
			for _, b := range buf[:n] {
				checksum = checksum>>1 | checksum<<15
				checksum += uint16(b)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, 0, err
		}
	}

	return sumBlockCount(bytesRead, 1024), checksum, nil
}

func sumChecksumSysV(reader io.Reader) (blocks uint64, checksum uint16, err error) {
	var (
		buf       [4096]byte
		bytesRead uint64
		total     uint32
	)

	for {
		n, err := reader.Read(buf[:])
		if n > 0 {
			bytesRead += uint64(n)
			for _, b := range buf[:n] {
				total += uint32(b)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, 0, err
		}
	}

	total = (total & 0xffff) + (total >> 16)
	total = (total & 0xffff) + (total >> 16)
	return sumBlockCount(bytesRead, 512), uint16(total), nil
}

func sumBlockCount(size, blockSize uint64) uint64 {
	if size == 0 {
		return 0
	}
	return (size + blockSize - 1) / blockSize
}

func writeSumLine(w io.Writer, name string, printName, useSysV bool, blocks uint64, checksum uint16) error {
	if useSysV {
		if printName {
			_, err := fmt.Fprintf(w, "%d %d %s\n", checksum, blocks, name)
			return err
		}
		_, err := fmt.Fprintf(w, "%d %d\n", checksum, blocks)
		return err
	}

	if printName {
		_, err := fmt.Fprintf(w, "%05d %5d %s\n", checksum, blocks, name)
		return err
	}
	_, err := fmt.Fprintf(w, "%05d %5d\n", checksum, blocks)
	return err
}

func reportSumOpenError(inv *Invocation, name string, err error) {
	_, _ = fmt.Fprintf(inv.Stderr, "sum: %s: %s\n", name, checksumOpenErrorText(err))
}

func reportSumReadError(inv *Invocation, name string, err error) {
	_, _ = fmt.Fprintf(inv.Stderr, "sum: %s: %s\n", name, err.Error())
}

var _ Command = (*Sum)(nil)
var _ SpecProvider = (*Sum)(nil)
var _ ParsedRunner = (*Sum)(nil)
