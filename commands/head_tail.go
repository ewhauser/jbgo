package commands

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

type headTailOptions struct {
	lines    int
	bytes    int
	hasBytes bool
	fromLine bool
	quiet    bool
	verbose  bool
	files    []string
}

func parseHeadTailArgs(inv *Invocation, cmdName string, allowFromLine bool) (headTailOptions, error) {
	args := inv.Args
	opts := headTailOptions{lines: 10}

	for len(args) > 0 {
		arg := args[0]
		switch {
		case arg == "-n" || arg == "--lines":
			if len(args) < 2 {
				return headTailOptions{}, exitf(inv, 1, "%s: missing argument to -n", cmdName)
			}
			count, fromLine, err := parseHeadTailCount(args[1], allowFromLine)
			if err != nil {
				return headTailOptions{}, exitf(inv, 1, "%s: invalid number of lines", cmdName)
			}
			opts.lines = count
			opts.fromLine = fromLine
			args = args[2:]
		case strings.HasPrefix(arg, "--lines="):
			count, err := strconv.Atoi(strings.TrimPrefix(arg, "--lines="))
			if err != nil || count < 0 {
				return headTailOptions{}, exitf(inv, 1, "%s: invalid number of lines", cmdName)
			}
			opts.lines = count
			opts.fromLine = false
			args = args[1:]
		case arg == "-c" || arg == "--bytes":
			if len(args) < 2 {
				return headTailOptions{}, exitf(inv, 1, "%s: missing argument to -c", cmdName)
			}
			count, err := strconv.Atoi(args[1])
			if err != nil || count < 0 {
				return headTailOptions{}, exitf(inv, 1, "%s: invalid number of bytes", cmdName)
			}
			opts.bytes = count
			opts.hasBytes = true
			args = args[2:]
		case strings.HasPrefix(arg, "--bytes="):
			count, err := strconv.Atoi(strings.TrimPrefix(arg, "--bytes="))
			if err != nil || count < 0 {
				return headTailOptions{}, exitf(inv, 1, "%s: invalid number of bytes", cmdName)
			}
			opts.bytes = count
			opts.hasBytes = true
			args = args[1:]
		case strings.HasPrefix(arg, "-n"):
			count, fromLine, err := parseHeadTailCount(strings.TrimPrefix(arg, "-n"), allowFromLine)
			if err != nil {
				return headTailOptions{}, exitf(inv, 1, "%s: invalid number of lines", cmdName)
			}
			opts.lines = count
			opts.fromLine = fromLine
			args = args[1:]
		case strings.HasPrefix(arg, "-c"):
			count, err := strconv.Atoi(strings.TrimPrefix(arg, "-c"))
			if err != nil || count < 0 {
				return headTailOptions{}, exitf(inv, 1, "%s: invalid number of bytes", cmdName)
			}
			opts.bytes = count
			opts.hasBytes = true
			args = args[1:]
		case arg == "-q" || arg == "--quiet" || arg == "--silent":
			opts.quiet = true
			args = args[1:]
		case arg == "-v" || arg == "--verbose":
			opts.verbose = true
			args = args[1:]
		case len(arg) > 1 && arg[0] == '-' && arg[1] >= '0' && arg[1] <= '9':
			count, err := strconv.Atoi(arg[1:])
			if err != nil {
				return headTailOptions{}, exitf(inv, 1, "%s: invalid number of lines", cmdName)
			}
			opts.lines = count
			args = args[1:]
		case strings.HasPrefix(arg, "-"):
			return headTailOptions{}, exitf(inv, 1, "%s: unsupported flag %s", cmdName, arg)
		default:
			opts.files = append(opts.files, arg)
			args = args[1:]
		}
	}

	return opts, nil
}

func parseHeadTailCount(value string, allowFromLine bool) (count int, fromLine bool, err error) {
	fromLine = false
	if allowFromLine && strings.HasPrefix(value, "+") {
		fromLine = true
		value = strings.TrimPrefix(value, "+")
	}
	count, err = strconv.Atoi(value)
	if err != nil || count < 0 {
		return 0, false, fmt.Errorf("invalid count")
	}
	return count, fromLine, nil
}

func splitLines(data []byte) [][]byte {
	if len(data) == 0 {
		return nil
	}
	lines := bytes.SplitAfter(data, []byte{'\n'})
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func firstLines(data []byte, count int) []byte {
	if count <= 0 {
		return nil
	}
	lines := splitLines(data)
	if count > len(lines) {
		count = len(lines)
	}
	return bytes.Join(lines[:count], nil)
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

func firstBytes(data []byte, count int) []byte {
	if count <= 0 {
		return nil
	}
	if count > len(data) {
		count = len(data)
	}
	return append([]byte(nil), data[:count]...)
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
