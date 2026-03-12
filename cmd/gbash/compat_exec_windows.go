//go:build windows

package main

import (
	"context"
	"fmt"
	"io"
)

func runCompatInvocation(context.Context, string, compatInvocation, io.Reader, io.Writer, io.Writer) (int, error) {
	return 1, fmt.Errorf("GNU compatibility mode is unsupported on Windows")
}
