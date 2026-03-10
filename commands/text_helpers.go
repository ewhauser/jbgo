package commands

import (
	"bytes"
	"fmt"
	"io"
)

func textLines(data []byte) []string {
	raw := splitLines(data)
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		lines = append(lines, string(bytes.TrimSuffix(line, []byte{'\n'})))
	}
	return lines
}

func writeTextLines(w io.Writer, lines []string) error {
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}
