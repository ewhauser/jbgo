package commands

import (
	"context"
	"fmt"
	"strings"
)

type Paste struct{}

type pasteOptions struct {
	serial    bool
	delimiter []rune
}

type pasteInput struct {
	lines []string
	index *int
}

func NewPaste() *Paste {
	return &Paste{}
}

func (c *Paste) Name() string {
	return "paste"
}

func (c *Paste) Run(ctx context.Context, inv *Invocation) error {
	opts, names, err := parsePasteArgs(inv)
	if err != nil {
		return err
	}
	inputs, err := loadPasteInputs(ctx, inv, names)
	if err != nil {
		return err
	}
	if opts.serial {
		return writePasteSerial(inv, inputs, opts)
	}
	return writePasteParallel(inv, inputs, opts)
}

func parsePasteArgs(inv *Invocation) (pasteOptions, []string, error) {
	args := inv.Args
	opts := pasteOptions{delimiter: []rune{'\t'}}
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
		case arg == "-s":
			opts.serial = true
		case arg == "--serial":
			opts.serial = true
		case arg == "-d":
			if len(args) < 2 {
				return pasteOptions{}, nil, exitf(inv, 1, "paste: option requires an argument -- 'd'")
			}
			opts.delimiter = []rune(args[1])
			args = args[2:]
			continue
		case arg == "--delimiters":
			if len(args) < 2 {
				return pasteOptions{}, nil, exitf(inv, 1, "paste: option requires an argument -- delimiters")
			}
			opts.delimiter = []rune(args[1])
			args = args[2:]
			continue
		case strings.HasPrefix(arg, "-d") && len(arg) > 2:
			opts.delimiter = []rune(arg[2:])
		case strings.HasPrefix(arg, "--delimiters="):
			opts.delimiter = []rune(strings.TrimPrefix(arg, "--delimiters="))
		default:
			return pasteOptions{}, nil, exitf(inv, 1, "paste: unsupported flag %s", arg)
		}
		args = args[1:]
	}
	if len(opts.delimiter) == 0 {
		opts.delimiter = []rune{'\t'}
	}
	if len(args) == 0 {
		args = []string{"-"}
	}
	return opts, args, nil
}

func loadPasteInputs(ctx context.Context, inv *Invocation, names []string) ([]pasteInput, error) {
	var (
		stdinLines []string
		stdinIndex int
		stdinReady bool
		inputs     []pasteInput
	)
	for _, name := range names {
		if name == "-" {
			if !stdinReady {
				data, err := readAllStdin(inv)
				if err != nil {
					return nil, err
				}
				stdinLines = textLines(data)
				stdinReady = true
			}
			inputs = append(inputs, pasteInput{
				lines: stdinLines,
				index: &stdinIndex,
			})
			continue
		}
		data, _, err := readAllFile(ctx, inv, name)
		if err != nil {
			return nil, err
		}
		index := 0
		inputs = append(inputs, pasteInput{
			lines: textLines(data),
			index: &index,
		})
	}
	return inputs, nil
}

func writePasteParallel(inv *Invocation, inputs []pasteInput, opts pasteOptions) error {
	for {
		row := make([]string, len(inputs))
		hasData := false
		for i := range inputs {
			if *inputs[i].index < len(inputs[i].lines) {
				row[i] = inputs[i].lines[*inputs[i].index]
				*inputs[i].index++
				hasData = true
			}
		}
		if !hasData {
			return nil
		}
		if _, err := fmt.Fprintln(inv.Stdout, pasteJoin(row, opts.delimiter)); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
}

func writePasteSerial(inv *Invocation, inputs []pasteInput, opts pasteOptions) error {
	for _, input := range inputs {
		row := make([]string, 0, len(input.lines))
		for *input.index < len(input.lines) {
			row = append(row, input.lines[*input.index])
			*input.index++
		}
		if _, err := fmt.Fprintln(inv.Stdout, pasteJoin(row, opts.delimiter)); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func pasteJoin(parts []string, delimiters []rune) string {
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	for i, part := range parts {
		if i > 0 {
			b.WriteRune(delimiters[(i-1)%len(delimiters)])
		}
		b.WriteString(part)
	}
	return b.String()
}

var _ Command = (*Paste)(nil)
