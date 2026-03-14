package builtins

import (
	"context"
	"io"
	"strings"
)

func readAllFile(ctx context.Context, inv *Invocation, name string) (data []byte, abs string, err error) {
	file, abs, err := openRead(ctx, inv, name)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = file.Close() }()

	data, err = io.ReadAll(file)
	if err != nil {
		return nil, "", &ExitError{Code: 1, Err: err}
	}
	return data, abs, nil
}

func readAllStdin(ctx context.Context, inv *Invocation) ([]byte, error) {
	stdin := io.Reader(strings.NewReader(""))
	if inv != nil && inv.Stdin != nil {
		stdin = inv.Stdin
	}
	data, err := io.ReadAll(ReaderWithContext(ctx, stdin))
	if err != nil {
		return nil, &ExitError{Code: 1, Err: err}
	}
	return data, nil
}
