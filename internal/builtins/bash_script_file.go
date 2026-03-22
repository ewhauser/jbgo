package builtins

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

func ValidateShellScriptFileData(path string, data []byte) error {
	firstLine, _, _ := bytes.Cut(data, []byte{'\n'})
	if bytes.IndexByte(firstLine, 0) < 0 {
		return nil
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("cannot execute binary file")
	}
	return fmt.Errorf("%s: %s: cannot execute binary file", path, path)
}
