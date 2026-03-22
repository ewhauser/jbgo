package builtins

import (
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
)

type underlyingWriter interface {
	UnderlyingWriter() io.Writer
}

func shellWriteErrorDiagnostic(name string, err error) (string, bool) {
	if runtime.GOOS == "darwin" {
		var pathErr *os.PathError
		if errors.As(err, &pathErr) && pathErr != nil && pathErr.Path != "" && pathErr.Err != nil {
			text := pathErr.Err.Error()
			if text == "" {
				return "", false
			}
			return pathErr.Path + ": " + strings.ToUpper(text[:1]) + text[1:], true
		}
	}
	text := shellWriteErrorText(err)
	if text == "" || name == "" {
		return "", false
	}
	return name + ": write error: " + text, true
}

func shellWriteErrorText(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && pathErr != nil && pathErr.Err != nil {
		return capitalizeErrorText(pathErr.Err.Error())
	}
	if err == nil {
		return ""
	}
	return capitalizeErrorText(err.Error())
}

func capitalizeErrorText(text string) string {
	if text == "" {
		return ""
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

func resolveUnderlyingWriter(w io.Writer) io.Writer {
	for w != nil {
		uw, ok := w.(underlyingWriter)
		if !ok {
			return w
		}
		next := uw.UnderlyingWriter()
		if next == nil || next == w {
			return w
		}
		w = next
	}
	return nil
}
