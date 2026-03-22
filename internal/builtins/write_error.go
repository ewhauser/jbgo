package builtins

import (
	"errors"
	"os"
	"strings"
)

func shellWriteErrorDiagnostic(err error) (string, bool) {
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) || pathErr == nil || pathErr.Path == "" || pathErr.Err == nil {
		return "", false
	}
	text := pathErr.Err.Error()
	if text == "" {
		return "", false
	}
	return pathErr.Path + ": " + strings.ToUpper(text[:1]) + text[1:], true
}
