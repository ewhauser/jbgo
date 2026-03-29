package shell

import (
	"errors"
	"io"

	"github.com/ewhauser/gbash/internal/shellvariantprofile"
	"github.com/ewhauser/gbash/shell/syntax"
	"github.com/ewhauser/gbash/shellvariant"
)

func formatParseError(err error, variant shellvariant.ShellVariant) string {
	var parseErr syntax.ParseError
	if !errors.As(err, &parseErr) {
		if err == nil {
			return ""
		}
		return err.Error()
	}
	if shellvariantprofile.Resolve(variant).UsesBashDiagnostics {
		return parseErr.BashError()
	}
	return parseErr.Error()
}

func writeCompilationError(stderr io.Writer, variant shellvariant.ShellVariant, err error) {
	if stderr == nil || err == nil {
		return
	}
	_, _ = io.WriteString(stderr, formatParseError(err, variant))
	_, _ = io.WriteString(stderr, "\n")
}
