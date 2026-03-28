package builtins

import (
	"io"
	stdfs "io/fs"
	"strings"

	"github.com/ewhauser/gbash/internal/commandutil"
	"golang.org/x/term"
)

func invInputLooksLikeTTY(inv *Invocation) bool {
	if inv == nil {
		return false
	}
	return inputLooksLikeTTY(inv.Stdin)
}

func inputLooksLikeTTY(reader io.Reader) bool {
	reader = resolveTTYReader(reader)
	if reader == nil {
		return false
	}

	if meta, ok := reader.(commandutil.RedirectMetadata); ok {
		redirectPath := strings.TrimSpace(meta.RedirectPath())
		if redirectPath != "" {
			if _, ok := ttyRecognizedPath(redirectPath); ok {
				return true
			}
		}
	}

	if fd, ok := reader.(interface{ Fd() uintptr }); ok {
		descriptor := fd.Fd()
		return descriptor != 0 && term.IsTerminal(int(descriptor))
	}

	if statter, ok := reader.(interface {
		Stat() (stdfs.FileInfo, error)
	}); ok {
		if info, err := statter.Stat(); err == nil && info.Mode()&stdfs.ModeCharDevice != 0 {
			return true
		}
	}

	return false
}

func resolveTTYReader(reader io.Reader) io.Reader {
	type underlyingReader interface {
		UnderlyingReader() io.Reader
	}
	for reader != nil {
		unwrapper, ok := reader.(underlyingReader)
		if !ok {
			return reader
		}
		next := unwrapper.UnderlyingReader()
		if next == nil || next == reader {
			return reader
		}
		reader = next
	}
	return nil
}
