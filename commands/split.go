package commands

import (
	"context"
	"fmt"
	stdfs "io/fs"
	"strconv"
	"strings"

	gbfs "github.com/ewhauser/gbash/fs"
)

type Split struct{}

type splitOptions struct {
	lines            int
	bytes            int
	chunks           int
	numeric          bool
	suffixLen        int
	additionalSuffix string
}

func NewSplit() *Split {
	return &Split{}
}

func (c *Split) Name() string {
	return "split"
}

func (c *Split) Run(ctx context.Context, inv *Invocation) error {
	opts, inputName, prefix, err := parseSplitArgs(inv)
	if err != nil {
		return err
	}

	var data []byte
	if inputName == "-" {
		data, err = readAllStdin(inv)
		if err != nil {
			return err
		}
	} else {
		data, _, err = readAllFile(ctx, inv, inputName)
		if err != nil {
			return err
		}
	}

	chunks := splitData(data, opts)
	for i, chunk := range chunks {
		target := gbfs.Resolve(inv.Cwd, prefix+splitSuffix(i, opts))
		if opts.additionalSuffix != "" {
			target += opts.additionalSuffix
		}
		if err := writeFileContents(ctx, inv, target, chunk, stdfs.FileMode(0o644)); err != nil {
			return err
		}
	}
	return nil
}

func parseSplitArgs(inv *Invocation) (opts splitOptions, inputName, prefix string, err error) {
	args := inv.Args
	opts = splitOptions{
		lines:     1000,
		suffixLen: 2,
	}
	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			args = args[1:]
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			break
		}
		switch {
		case arg == "-l":
			value, rest, err := parseSplitInt(inv, "l", args[1:])
			if err != nil {
				return splitOptions{}, "", "", err
			}
			opts.lines = value
			opts.bytes = 0
			args = rest
			continue
		case strings.HasPrefix(arg, "-l") && len(arg) > 2:
			value, err := strconv.Atoi(arg[2:])
			if err != nil || value <= 0 {
				return splitOptions{}, "", "", exitf(inv, 1, "split: invalid line count %q", arg[2:])
			}
			opts.lines = value
			opts.bytes = 0
		case arg == "-b":
			if len(args) < 2 {
				return splitOptions{}, "", "", exitf(inv, 1, "split: option requires an argument -- 'b'")
			}
			value, err := parseSplitSize(args[1])
			if err != nil {
				return splitOptions{}, "", "", exitf(inv, 1, "split: invalid byte count %q", args[1])
			}
			opts.bytes = value
			opts.lines = 0
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-b") && len(arg) > 2:
			value, err := parseSplitSize(arg[2:])
			if err != nil {
				return splitOptions{}, "", "", exitf(inv, 1, "split: invalid byte count %q", arg[2:])
			}
			opts.bytes = value
			opts.lines = 0
		case arg == "-d":
			opts.numeric = true
		case arg == "-n":
			value, rest, err := parseSplitInt(inv, "n", args[1:])
			if err != nil {
				return splitOptions{}, "", "", err
			}
			if value <= 0 {
				return splitOptions{}, "", "", exitf(inv, 1, "split: invalid chunk count %d", value)
			}
			opts.chunks = value
			opts.lines = 0
			opts.bytes = 0
			args = rest
			continue
		case strings.HasPrefix(arg, "-n") && len(arg) > 2:
			value, err := strconv.Atoi(arg[2:])
			if err != nil || value <= 0 {
				return splitOptions{}, "", "", exitf(inv, 1, "split: invalid chunk count %q", arg[2:])
			}
			opts.chunks = value
			opts.lines = 0
			opts.bytes = 0
		case arg == "-a":
			value, rest, err := parseSplitInt(inv, "a", args[1:])
			if err != nil {
				return splitOptions{}, "", "", err
			}
			if value <= 0 {
				return splitOptions{}, "", "", exitf(inv, 1, "split: invalid suffix length %d", value)
			}
			opts.suffixLen = value
			args = rest
			continue
		case arg == "--additional-suffix":
			if len(args) < 2 {
				return splitOptions{}, "", "", exitf(inv, 1, "split: option requires an argument -- additional-suffix")
			}
			opts.additionalSuffix = args[1]
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "--additional-suffix="):
			opts.additionalSuffix = strings.TrimPrefix(arg, "--additional-suffix=")
		case strings.HasPrefix(arg, "-a") && len(arg) > 2:
			value, err := strconv.Atoi(arg[2:])
			if err != nil || value <= 0 {
				return splitOptions{}, "", "", exitf(inv, 1, "split: invalid suffix length %q", arg[2:])
			}
			opts.suffixLen = value
		default:
			return splitOptions{}, "", "", exitf(inv, 1, "split: unsupported flag %s", arg)
		}
		args = args[1:]
	}

	inputName = "-"
	prefix = "x"
	switch len(args) {
	case 0:
	case 1:
		inputName = args[0]
	case 2:
		inputName = args[0]
		prefix = args[1]
	default:
		return splitOptions{}, "", "", exitf(inv, 1, "split: too many operands")
	}
	return opts, inputName, prefix, nil
}

func parseSplitInt(inv *Invocation, flag string, args []string) (value int, rest []string, err error) {
	if len(args) == 0 {
		return 0, nil, exitf(inv, 1, "split: option requires an argument -- '%s'", flag)
	}
	value, err = strconv.Atoi(args[0])
	if err != nil {
		return 0, nil, exitf(inv, 1, "split: invalid numeric value %q", args[0])
	}
	return value, args[1:], nil
}

func parseSplitSize(value string) (int, error) {
	if value == "" {
		return 0, fmt.Errorf("empty size")
	}
	multiplier := 1
	last := value[len(value)-1]
	switch last {
	case 'k', 'K':
		multiplier = 1024
		value = value[:len(value)-1]
	case 'm', 'M':
		multiplier = 1024 * 1024
		value = value[:len(value)-1]
	}
	base, err := strconv.Atoi(value)
	if err != nil || base <= 0 {
		return 0, fmt.Errorf("invalid size")
	}
	return base * multiplier, nil
}

func splitData(data []byte, opts splitOptions) [][]byte {
	if len(data) == 0 {
		return nil
	}
	if opts.chunks > 0 {
		var chunks [][]byte
		chunkSize := (len(data) + opts.chunks - 1) / opts.chunks
		for start := 0; start < len(data); start += chunkSize {
			end := min(start+chunkSize, len(data))
			chunk := append([]byte(nil), data[start:end]...)
			chunks = append(chunks, chunk)
		}
		return chunks
	}
	if opts.bytes > 0 {
		var chunks [][]byte
		for start := 0; start < len(data); start += opts.bytes {
			end := min(start+opts.bytes, len(data))
			chunk := append([]byte(nil), data[start:end]...)
			chunks = append(chunks, chunk)
		}
		return chunks
	}

	lines := splitLines(data)
	var chunks [][]byte
	for start := 0; start < len(lines); start += opts.lines {
		end := min(start+opts.lines, len(lines))
		chunks = append(chunks, append([]byte(nil), bytesJoin(lines[start:end])...))
	}
	return chunks
}

func bytesJoin(parts [][]byte) []byte {
	total := 0
	for _, part := range parts {
		total += len(part)
	}
	out := make([]byte, 0, total)
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

func splitSuffix(index int, opts splitOptions) string {
	if opts.numeric {
		return fmt.Sprintf("%0*d", opts.suffixLen, index)
	}
	value := index
	digits := make([]byte, opts.suffixLen)
	for i := opts.suffixLen - 1; i >= 0; i-- {
		digits[i] = byte('a' + (value % 26))
		value /= 26
	}
	return string(digits)
}

var _ Command = (*Split)(nil)
