package commands

import (
	"context"
	"fmt"
	"strings"
)

type Chown struct{}

func NewChown() *Chown {
	return &Chown{}
}

func (c *Chown) Name() string {
	return "chown"
}

func (c *Chown) Run(ctx context.Context, inv *Invocation) error {
	opts, err := parseChownArgs(inv)
	if err != nil {
		return err
	}
	switch opts.mode {
	case "help":
		_, _ = fmt.Fprintln(inv.Stdout, "usage: chown [OPTION]... [OWNER][:[GROUP]] FILE...")
		return nil
	case "version":
		_, _ = fmt.Fprintln(inv.Stdout, "chown (gbash)")
		return nil
	}

	db := loadPermissionIdentityDB(ctx, inv)
	if opts.fromSpec != "" {
		opts.filter, err = parsePermissionFilterSpec(inv, db, opts.fromSpec)
		if err != nil {
			return err
		}
	}

	if opts.reference != "" {
		info, _, err := statPath(ctx, inv, opts.reference)
		if err != nil {
			return err
		}
		owner := permissionLookupOwnership(db, info)
		opts.uid = &owner.uid
		opts.gid = &owner.gid
	} else {
		opts.uid, opts.gid, err = parsePermissionOwnerSpec(inv, db, opts.ownerSpec)
		if err != nil {
			return err
		}
	}
	return runPermissionApply(ctx, inv, db, &permissionApplyOptions{
		commandName: c.Name(),
		files:       opts.files,
		uid:         opts.uid,
		gid:         opts.gid,
		filter:      opts.filter,
		verbosity:   opts.verbosity,
		walk:        opts.walk,
	})
}

type chownOptions struct {
	mode      string
	fromSpec  string
	ownerSpec string
	reference string
	files     []string
	uid       *uint32
	gid       *uint32
	filter    permissionIfFrom
	verbosity permissionVerbosity
	walk      permissionWalkOptions
}

func parseChownArgs(inv *Invocation) (chownOptions, error) {
	args := append([]string(nil), inv.Args...)
	opts := chownOptions{
		verbosity: permissionVerbosity{level: permissionVerbosityNormal},
	}
	recursive := false
	preserveRoot := false
	traverse := permissionTraverseNone
	var dereference *bool
	parsingOptions := true
	operands := make([]string, 0, len(args))

	for len(args) > 0 {
		arg := args[0]
		args = args[1:]

		if parsingOptions && arg == "--" {
			parsingOptions = false
			continue
		}
		if !parsingOptions || !strings.HasPrefix(arg, "-") || arg == "-" {
			operands = append(operands, arg)
			parsingOptions = false
			continue
		}

		switch {
		case arg == "--help":
			opts.mode = "help"
			return opts, nil
		case arg == "--version":
			opts.mode = "version"
			return opts, nil
		case arg == "--changes":
			opts.verbosity.level = permissionVerbosityChanges
		case arg == "--quiet" || arg == "--silent":
			opts.verbosity.level = permissionVerbositySilent
		case arg == "--verbose":
			opts.verbosity.level = permissionVerbosityVerbose
		case arg == "--recursive":
			recursive = true
		case arg == "--preserve-root":
			preserveRoot = true
		case arg == "--no-preserve-root":
			preserveRoot = false
		case arg == "--dereference":
			value := true
			dereference = &value
		case arg == "--no-dereference":
			value := false
			dereference = &value
		case arg == "--from":
			if len(args) == 0 {
				return chownOptions{}, exitf(inv, 1, "chown: option requires an argument -- from")
			}
			opts.fromSpec = args[0]
			args = args[1:]
		case strings.HasPrefix(arg, "--from="):
			opts.fromSpec = arg[len("--from="):]
		case arg == "--reference":
			if len(args) == 0 {
				return chownOptions{}, exitf(inv, 1, "chown: option requires an argument -- reference")
			}
			opts.reference = args[0]
			args = args[1:]
		case strings.HasPrefix(arg, "--reference="):
			opts.reference = arg[len("--reference="):]
		default:
			if strings.HasPrefix(arg, "--") {
				return chownOptions{}, exitf(inv, 1, "chown: unrecognized option '%s'", arg)
			}
			if err := parseChownShortFlags(inv, arg, &opts, &recursive, &traverse, &dereference); err != nil {
				return chownOptions{}, err
			}
		}
	}

	walk, err := normalizePermissionWalkOptionsForCommand(inv, "chown", recursive, dereference, traverse, preserveRoot)
	if err != nil {
		return chownOptions{}, err
	}
	opts.walk = walk
	if opts.reference != "" {
		if len(operands) == 0 {
			return chownOptions{}, exitf(inv, 1, "chown: missing operand after %s", opts.reference)
		}
		opts.files = operands
		return opts, nil
	}
	if len(operands) < 2 {
		return chownOptions{}, exitf(inv, 1, "chown: missing operand")
	}
	opts.ownerSpec = operands[0]
	opts.files = operands[1:]
	return opts, nil
}

func parseChownShortFlags(inv *Invocation, arg string, opts *chownOptions, recursive *bool, traverse *permissionTraverseSymlinks, dereference **bool) error {
	for _, flag := range arg[1:] {
		switch flag {
		case 'c':
			opts.verbosity.level = permissionVerbosityChanges
		case 'f', 'q':
			opts.verbosity.level = permissionVerbositySilent
		case 'v':
			opts.verbosity.level = permissionVerbosityVerbose
		case 'R':
			*recursive = true
		case 'H':
			*traverse = permissionTraverseFirst
		case 'L':
			*traverse = permissionTraverseAll
		case 'P':
			*traverse = permissionTraverseNone
		case 'h':
			value := false
			*dereference = &value
		default:
			return exitf(inv, 1, "chown: invalid option -- %c", flag)
		}
	}
	return nil
}

var _ Command = (*Chown)(nil)
