package builtins

import (
	"bytes"
)

func textLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	lines := make([]string, 0, bytes.Count(data, []byte{'\n'})+1)
	visitTextLines(data, func(line []byte, _ int) bool {
		lines = append(lines, string(line))
		return true
	})
	return lines
}

func visitTextLines(data []byte, fn func(line []byte, lineNumber int) bool) {
	if len(data) == 0 {
		return
	}

	start := 0
	lineNumber := 1
	for start <= len(data) {
		idx := bytes.IndexByte(data[start:], '\n')
		if idx < 0 {
			_ = fn(data[start:], lineNumber)
			return
		}

		if !fn(data[start:start+idx], lineNumber) {
			return
		}
		start += idx + 1
		lineNumber++
		if start == len(data) {
			return
		}
	}
}
