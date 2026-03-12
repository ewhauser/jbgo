package commands

import (
	"bytes"
)

func textLines(data []byte) []string {
	raw := splitLines(data)
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		lines = append(lines, string(bytes.TrimSuffix(line, []byte{'\n'})))
	}
	return lines
}
