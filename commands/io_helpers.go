package commands

import (
	"context"
	"io"
)

func readAllFile(ctx context.Context, inv *Invocation, name string) (data []byte, abs string, err error) {
	file, abs, err := openRead(ctx, inv, name)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()

	data, err = io.ReadAll(file)
	if err != nil {
		return nil, "", &ExitError{Code: 1, Err: err}
	}
	return data, abs, nil
}

func readAllStdin(inv *Invocation) ([]byte, error) {
	data, err := io.ReadAll(inv.Stdin)
	if err != nil {
		return nil, &ExitError{Code: 1, Err: err}
	}
	return data, nil
}
