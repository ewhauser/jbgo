package builtins

import (
	"context"
	"fmt"
	"io"
	"strconv"
)

func parseBaseEncWrap(name, value string, inv *Invocation) (int, error) {
	wrap, err := strconv.Atoi(value)
	if err != nil || wrap < 0 {
		return 0, exitf(inv, 1, "%s: invalid wrap size: '%s'", name, value)
	}
	return wrap, nil
}

func readSingleBaseEncInput(ctx context.Context, inv *Invocation, name string, args []string) ([]byte, error) {
	if len(args) > 1 {
		return nil, exitf(inv, 1, "%s: extra operand '%s'\nTry '%s --help' for more information.", name, args[1], name)
	}

	inputs, err := readNamedInputs(ctx, inv, args)
	if err != nil {
		return nil, err
	}

	var data []byte
	for _, input := range inputs {
		data = append(data, input.Data...)
	}
	return data, nil
}

func writeBaseEncOutput(w io.Writer, encoded string, wrap int) error {
	if encoded == "" {
		return nil
	}
	if wrap == 0 {
		_, err := io.WriteString(w, encoded)
		return err
	}

	for len(encoded) > wrap {
		if _, err := io.WriteString(w, encoded[:wrap]); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
		encoded = encoded[wrap:]
	}
	_, err := fmt.Fprintf(w, "%s\n", encoded)
	return err
}
