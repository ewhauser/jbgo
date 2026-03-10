package commands

import (
	"bytes"
	"context"
	"fmt"
	stdfs "io/fs"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"
)

type FileCmd struct{}

func NewFile() *FileCmd {
	return &FileCmd{}
}

func (c *FileCmd) Name() string {
	return "file"
}

func (c *FileCmd) Run(ctx context.Context, inv *Invocation) error {
	args := inv.Args
	brief := false
	mimeOnly := false

	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "-b":
			brief = true
		case "-i":
			mimeOnly = true
		case "-bi", "-ib":
			brief = true
			mimeOnly = true
		case "-L", "--dereference":
			// Accepted for compatibility; current implementation reports the link object.
		default:
			return exitf(inv, 1, "file: unsupported flag %s", args[0])
		}
		args = args[1:]
	}

	if len(args) == 0 {
		return exitf(inv, 1, "file: missing operand")
	}

	exitCode := 0
	for _, name := range args {
		info, abs, err := lstatPath(ctx, inv, name)
		if err != nil {
			_, _ = fmt.Fprintf(inv.Stdout, "%s: cannot open `%s' (No such file or directory)\n", name, name)
			exitCode = 1
			continue
		}

		description, mime, err := detectFileType(ctx, inv, abs, info)
		if err != nil {
			return err
		}
		output := description
		if mimeOnly {
			output = mime
		}
		if brief {
			_, err = fmt.Fprintln(inv.Stdout, output)
		} else {
			_, err = fmt.Fprintf(inv.Stdout, "%s: %s\n", abs, output)
		}
		if err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func detectFileType(ctx context.Context, inv *Invocation, abs string, info stdfs.FileInfo) (description, mime string, err error) {
	switch {
	case info.Mode()&stdfs.ModeSymlink != 0:
		return "symbolic link", "inode/symlink", nil
	case info.IsDir():
		return "directory", "inode/directory", nil
	}

	data, _, err := readAllFile(ctx, inv, abs)
	if err != nil {
		return "", "", err
	}
	if len(data) == 0 {
		return "empty", "application/x-empty", nil
	}

	if kind, mt, ok := detectMagicFileType(data); ok {
		return kind, mt, nil
	}
	if script, mt, ok := detectShebang(data); ok {
		return script, mt, nil
	}

	mime = http.DetectContentType(data)
	switch ext := strings.ToLower(path.Ext(abs)); ext {
	case ".ts":
		return "TypeScript source, ASCII text", "text/x-typescript", nil
	case ".js":
		return "JavaScript source, ASCII text", "text/javascript", nil
	case ".json":
		return "JSON text data", "application/json", nil
	case ".md":
		return "Markdown text", "text/markdown", nil
	}

	if isASCIIText(data) {
		if bytes.Contains(data, []byte("\r\n")) {
			return "ASCII text, with CRLF line terminators", "text/plain", nil
		}
		return "ASCII text", "text/plain", nil
	}
	if utf8.Valid(data) {
		return "UTF-8 Unicode text", "text/plain", nil
	}
	return "data", mime, nil
}

func detectMagicFileType(data []byte) (description, mime string, ok bool) {
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}):
		return "PNG image data", "image/png", true
	case len(data) >= 6 && (bytes.Equal(data[:6], []byte("GIF87a")) || bytes.Equal(data[:6], []byte("GIF89a"))):
		return "GIF image data", "image/gif", true
	case len(data) >= 4 && bytes.Equal(data[:4], []byte("%PDF")):
		return "PDF document", "application/pdf", true
	case len(data) >= 4 && bytes.Equal(data[:4], []byte("PK\x03\x04")):
		return "Zip archive data", "application/zip", true
	case len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b:
		return "gzip compressed data", "application/gzip", true
	default:
		return "", "", false
	}
}

func detectShebang(data []byte) (description, mime string, ok bool) {
	if !bytes.HasPrefix(data, []byte("#!")) {
		return "", "", false
	}
	line := string(bytes.SplitN(data, []byte{'\n'}, 2)[0])
	switch {
	case strings.Contains(line, "python"):
		return "Python script, ASCII text executable", "text/x-python", true
	case strings.Contains(line, "sh"), strings.Contains(line, "bash"):
		return "POSIX shell script, ASCII text executable", "text/x-shellscript", true
	default:
		return "script text executable", "text/plain", true
	}
}

func isASCIIText(data []byte) bool {
	for _, b := range data {
		switch {
		case b == '\n' || b == '\r' || b == '\t':
		case b >= 0x20 && b <= 0x7e:
		default:
			return false
		}
	}
	return true
}

var _ Command = (*FileCmd)(nil)
