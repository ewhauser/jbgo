package builtins

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"
)

func isDecimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func parseHeadTailCount(value string, allowFromLine bool) (count int, fromLine bool, err error) {
	fromLine = false
	if allowFromLine && strings.HasPrefix(value, "+") {
		fromLine = true
		value = strings.TrimPrefix(value, "+")
	}
	count, err = parseHeadTailNumber(value)
	return count, fromLine, err
}

func parseHeadTailNumber(value string) (int, error) {
	if value == "" {
		return 0, fmt.Errorf("invalid count")
	}

	multiplier := int64(1)
	for _, suffix := range []struct {
		token      string
		multiplier int64
	}{
		{"E", 1 << 60},
		{"P", 1 << 50},
		{"T", 1 << 40},
		{"G", 1 << 30},
		{"M", 1 << 20},
		{"K", 1 << 10},
		{"b", 512},
	} {
		if before, ok := strings.CutSuffix(value, suffix.token); ok {
			value = before
			multiplier = suffix.multiplier
			break
		}
	}

	count, err := strconv.ParseInt(value, 10, 64)
	if err != nil || count < 0 {
		return 0, fmt.Errorf("invalid count")
	}
	if count > math.MaxInt64/multiplier {
		return 0, fmt.Errorf("invalid count")
	}
	total := count * multiplier
	if total > int64(math.MaxInt) {
		return 0, fmt.Errorf("invalid count")
	}
	return int(total), nil
}

func splitLines(data []byte) [][]byte {
	if len(data) == 0 {
		return [][]byte{}
	}
	lines := bytes.SplitAfter(data, []byte{'\n'})
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func lastLines(data []byte, count int) []byte {
	if count <= 0 {
		return nil
	}
	lines := splitLines(data)
	if count > len(lines) {
		count = len(lines)
	}
	return bytes.Join(lines[len(lines)-count:], nil)
}

func linesFrom(data []byte, startLine int) []byte {
	if startLine <= 1 {
		return data
	}
	lines := splitLines(data)
	if startLine > len(lines) {
		return nil
	}
	return bytes.Join(lines[startLine-1:], nil)
}

func lastBytes(data []byte, count int) []byte {
	if count <= 0 {
		return nil
	}
	if count > len(data) {
		count = len(data)
	}
	return append([]byte(nil), data[len(data)-count:]...)
}

func bytesFrom(data []byte, start int) []byte {
	if start <= 1 {
		return append([]byte(nil), data...)
	}
	if start > len(data) {
		return nil
	}
	return append([]byte(nil), data[start-1:]...)
}
