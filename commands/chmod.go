package commands

import (
	"context"
	"fmt"
	stdfs "io/fs"
	"strconv"
	"strings"

	"github.com/cadencerpm/just-bash-go/policy"
)

type Chmod struct{}

func NewChmod() *Chmod {
	return &Chmod{}
}

func (c *Chmod) Name() string {
	return "chmod"
}

func (c *Chmod) Run(ctx context.Context, inv *Invocation) error {
	args := inv.Args
	recursive := false
	verbose := false

	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "-R", "-r":
			recursive = true
		case "-v":
			verbose = true
		case "-Rv", "-vR", "-rv", "-vr":
			recursive = true
			verbose = true
		default:
			return exitf(inv, 1, "chmod: unsupported flag %s", args[0])
		}
		args = args[1:]
	}
	if len(args) < 2 {
		return exitf(inv, 1, "chmod: missing operand")
	}

	modeSpec := args[0]
	targets := args[1:]
	for _, target := range targets {
		if recursive {
			if err := walkPathTree(ctx, inv, target, func(abs string, info stdfs.FileInfo, _ int) error {
				return applyModeSpec(ctx, inv, abs, info, modeSpec, verbose)
			}); err != nil {
				return err
			}
			continue
		}
		info, abs, err := lstatPath(ctx, inv, target)
		if err != nil {
			return err
		}
		if err := applyModeSpec(ctx, inv, abs, info, modeSpec, verbose); err != nil {
			return err
		}
	}
	return nil
}

func applyModeSpec(ctx context.Context, inv *Invocation, abs string, info stdfs.FileInfo, modeSpec string, verbose bool) error {
	if _, err := allowPath(ctx, inv, policy.FileActionWrite, abs); err != nil {
		return err
	}
	mode, err := computeChmodMode(info.Mode(), modeSpec)
	if err != nil {
		return exitf(inv, 1, "chmod: invalid mode: %s", modeSpec)
	}
	if err := inv.FS.Chmod(ctx, abs, mode); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	recordFileMutation(inv.Trace, "chmod", abs, abs, abs)
	if verbose {
		if _, err := fmt.Fprintf(inv.Stdout, "mode of %q changed to %s\n", abs, formatModeOctal(mode)); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func computeChmodMode(current stdfs.FileMode, spec string) (stdfs.FileMode, error) {
	if value, err := strconv.ParseUint(spec, 8, 32); err == nil {
		base := current &^ (stdfs.ModePerm | stdfs.ModeSetuid | stdfs.ModeSetgid | stdfs.ModeSticky)
		return base | stdfs.FileMode(value), nil
	}

	mode := current
	for _, clause := range strings.Split(spec, ",") {
		if clause == "" {
			return 0, fmt.Errorf("empty clause")
		}
		idx := strings.IndexAny(clause, "+-=")
		if idx <= 0 || idx == len(clause)-1 {
			return 0, fmt.Errorf("invalid clause")
		}
		whoPart := clause[:idx]
		op := clause[idx]
		permPart := clause[idx+1:]
		whoMask, specialMask := chmodWhoMasks(whoPart)
		if whoMask == 0 && specialMask == 0 {
			return 0, fmt.Errorf("invalid subject")
		}
		permMask, specialPerm, err := chmodPermMasks(permPart, current)
		if err != nil {
			return 0, err
		}

		switch op {
		case '+':
			mode |= permMask & whoMask
			mode |= specialPerm & specialMask
		case '-':
			mode &^= permMask & whoMask
			mode &^= specialPerm & specialMask
		case '=':
			mode &^= whoMask
			mode &^= specialMask
			mode |= permMask & whoMask
			mode |= specialPerm & specialMask
		default:
			return 0, fmt.Errorf("invalid operator")
		}
		current = mode
	}
	return mode, nil
}

func chmodWhoMasks(who string) (permMask stdfs.FileMode, specialMask stdfs.FileMode) {
	if who == "" {
		who = "a"
	}
	for _, ch := range who {
		switch ch {
		case 'u':
			permMask |= 0o700
			specialMask |= stdfs.ModeSetuid
		case 'g':
			permMask |= 0o070
			specialMask |= stdfs.ModeSetgid
		case 'o':
			permMask |= 0o007
			specialMask |= stdfs.ModeSticky
		case 'a':
			permMask |= 0o777
			specialMask |= stdfs.ModeSetuid | stdfs.ModeSetgid | stdfs.ModeSticky
		default:
			return 0, 0
		}
	}
	return permMask, specialMask
}

func chmodPermMasks(perms string, current stdfs.FileMode) (permMask stdfs.FileMode, specialMask stdfs.FileMode, err error) {
	for _, ch := range perms {
		switch ch {
		case 'r':
			permMask |= 0o444
		case 'w':
			permMask |= 0o222
		case 'x':
			permMask |= 0o111
		case 'X':
			if current.IsDir() || current&0o111 != 0 {
				permMask |= 0o111
			}
		case 's':
			specialMask |= stdfs.ModeSetuid | stdfs.ModeSetgid
		case 't':
			specialMask |= stdfs.ModeSticky
		default:
			return 0, 0, fmt.Errorf("invalid permission")
		}
	}
	return permMask, specialMask, nil
}

var _ Command = (*Chmod)(nil)
