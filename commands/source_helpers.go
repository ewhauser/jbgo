package commands

import (
	"context"
	"errors"
	"path"
)

type namedInput struct {
	Name      string
	Abs       string
	Data      []byte
	FromStdin bool
}

func readNamedInputs(ctx context.Context, inv *Invocation, names []string, defaultStdin bool) ([]namedInput, error) {
	if len(names) == 0 {
		if !defaultStdin {
			return nil, nil
		}
		data, err := readAllStdin(ctx, inv)
		if err != nil {
			return nil, err
		}
		return []namedInput{{
			Name:      "-",
			Abs:       "-",
			Data:      data,
			FromStdin: true,
		}}, nil
	}

	var (
		inputs      []namedInput
		stdinData   []byte
		stdinLoaded bool
	)
	for _, name := range names {
		if name == "-" {
			if !stdinLoaded {
				data, err := readAllStdin(ctx, inv)
				if err != nil {
					return nil, err
				}
				stdinData = data
				stdinLoaded = true
			}
			inputs = append(inputs, namedInput{
				Name:      "-",
				Abs:       "-",
				Data:      stdinData,
				FromStdin: true,
			})
			continue
		}
		data, abs, err := readAllFile(ctx, inv, name)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, namedInput{
			Name: path.Base(abs),
			Abs:  abs,
			Data: data,
		})
	}
	return inputs, nil
}

func readTwoInputs(ctx context.Context, inv *Invocation, leftName, rightName string) (leftData, rightData []byte, err error) {
	if leftName == "-" && rightName == "-" {
		return nil, nil, &ExitError{
			Code: 1,
			Err:  errors.New("only one input may be read from stdin"),
		}
	}

	var stdinData []byte
	stdinLoaded := false
	load := func(name string) ([]byte, error) {
		if name != "-" {
			data, _, err := readAllFile(ctx, inv, name)
			return data, err
		}
		if !stdinLoaded {
			data, err := readAllStdin(ctx, inv)
			if err != nil {
				return nil, err
			}
			stdinData = data
			stdinLoaded = true
		}
		return stdinData, nil
	}

	leftData, err = load(leftName)
	if err != nil {
		return nil, nil, err
	}
	rightData, err = load(rightName)
	if err != nil {
		return nil, nil, err
	}
	return leftData, rightData, nil
}
