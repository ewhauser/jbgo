package builtins

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

func ValidateShellScriptFileData(path string, data []byte) error {
	if bytes.IndexByte(data, 0) < 0 {
		return nil
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("cannot execute binary file")
	}
	return fmt.Errorf("%s: %s: cannot execute binary file", path, path)
}
