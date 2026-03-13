package commands

import (
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"strings"
)

type Stat struct{}

func NewStat() *Stat {
	return &Stat{}
}

func (c *Stat) Name() string {
	return "stat"
}

func (c *Stat) Run(ctx context.Context, inv *Invocation) error {
	args := inv.Args
	format := ""

	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "-c":
			if len(args) < 2 {
				return exitf(inv, 1, "stat: option requires an argument -- c")
			}
			format = args[1]
			args = args[2:]
		default:
			return exitf(inv, 1, "stat: unsupported flag %s", args[0])
		}
	}

	if len(args) == 0 {
		return exitf(inv, 1, "stat: missing operand")
	}

	exitCode := 0
	for _, name := range args {
		info, abs, err := lstatPath(ctx, inv, name)
		if err != nil {
			var exitErr *ExitError
			if errors.As(err, &exitErr) && errors.Is(exitErr.Err, stdfs.ErrNotExist) {
				_, _ = fmt.Fprintf(inv.Stderr, "stat: cannot stat %q: No such file or directory\n", name)
				exitCode = 1
				continue
			}
			return err
		}

		var output string
		if format == "" {
			output = defaultStatOutput(abs, info)
		} else {
			rendered, err := renderStatFormat(ctx, inv, abs, info, format)
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			output = rendered + "\n"
		}
		if _, err := fmt.Fprint(inv.Stdout, output); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}

	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func defaultStatOutput(abs string, info stdfs.FileInfo) string {
	return fmt.Sprintf(
		"  File: %s\n  Size: %d\n  Type: %s\n  Mode: (%s/%s)\n",
		abs,
		info.Size(),
		fileTypeName(info),
		formatModeOctal(info.Mode()),
		formatModeLong(info.Mode()),
	)
}

func renderStatFormat(ctx context.Context, inv *Invocation, abs string, info stdfs.FileInfo, format string) (string, error) {
	identities := loadPermissionIdentityDB(ctx, inv)
	owner := permissionLookupOwnership(identities, info)
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i == len(format)-1 {
			b.WriteByte(format[i])
			continue
		}
		i++
		switch format[i] {
		case '%':
			b.WriteByte('%')
		case 'n':
			b.WriteString(abs)
		case 'N':
			if info.Mode()&stdfs.ModeSymlink != 0 {
				target, err := inv.FS.Readlink(ctx, abs)
				if err != nil {
					return "", err
				}
				fmt.Fprintf(&b, "%q -> %q", abs, target)
			} else {
				fmt.Fprintf(&b, "%q", abs)
			}
		case 's':
			fmt.Fprintf(&b, "%d", info.Size())
		case 'd':
			fmt.Fprintf(&b, "%d", statDevice(info))
		case 'F':
			b.WriteString(fileTypeName(info))
		case 'i':
			fmt.Fprintf(&b, "%d", statInode(info))
		case 'a':
			b.WriteString(formatModeOctal(info.Mode()))
		case 'A':
			b.WriteString(formatModeLong(info.Mode()))
		case 'u':
			fmt.Fprintf(&b, "%d", owner.uid)
		case 'g':
			fmt.Fprintf(&b, "%d", owner.gid)
		case 'U':
			b.WriteString(permissionNameOrID(owner.user, owner.uid))
		case 'G':
			b.WriteString(permissionNameOrID(owner.group, owner.gid))
		default:
			return "", fmt.Errorf("unsupported format sequence %%%c", format[i])
		}
	}
	return b.String(), nil
}

func statDevice(info stdfs.FileInfo) uint64 {
	dev, _, ok := testDeviceAndInode(info)
	if !ok {
		return 0
	}
	return dev
}

func statInode(info stdfs.FileInfo) uint64 {
	_, ino, ok := testDeviceAndInode(info)
	if !ok {
		return 0
	}
	return ino
}

var _ Command = (*Stat)(nil)
