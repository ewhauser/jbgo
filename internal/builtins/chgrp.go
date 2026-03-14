package builtins

import (
	"context"
	"strconv"
	"strings"
)

type Chgrp struct{}

func NewChgrp() *Chgrp {
	return &Chgrp{}
}

func (c *Chgrp) Name() string {
	return "chgrp"
}

func (c *Chgrp) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Chgrp) Spec() CommandSpec {
	return CommandSpec{
		Name:      "chgrp",
		About:     "Change the group of each FILE to GROUP.",
		Usage:     "chgrp [OPTION]... GROUP FILE...",
		AfterHelp: "  or:  chgrp [OPTION]... --reference=RFILE FILE...",
		Options: []OptionSpec{
			{Name: "help", Long: "help", Help: "display this help and exit"},
			{Name: "changes", Short: 'c', Long: "changes", Help: "like verbose but report only when a change is made"},
			{Name: "quiet", Short: 'f', Long: "quiet", Aliases: []string{"silent"}, HelpAliases: []string{"silent"}, Help: "suppress most error messages"},
			{Name: "verbose", Short: 'v', Long: "verbose", Help: "output a diagnostic for every file processed"},
			{Name: "preserve-root", Long: "preserve-root", Help: "fail to operate recursively on '/'"},
			{Name: "no-preserve-root", Long: "no-preserve-root", Help: "do not treat '/' specially"},
			{Name: "reference", Long: "reference", ValueName: "RFILE", Arity: OptionRequiredValue, Help: "use RFILE's group rather than specifying GROUP"},
			{Name: "from", Long: "from", ValueName: "GROUP", Arity: OptionRequiredValue, Help: "change only if current group matches GROUP"},
			{Name: "recursive", Short: 'R', Long: "recursive", Help: "operate on files and directories recursively"},
			{Name: "traverse-first", Short: 'H', Help: "if a command line argument is a symbolic link to a directory, traverse it"},
			{Name: "traverse-all", Short: 'L', Help: "traverse every symbolic link to a directory encountered"},
			{Name: "traverse-none", Short: 'P', Help: "do not traverse any symbolic links (default)"},
			{Name: "dereference", Long: "dereference", Help: "affect the referent of each symbolic link rather than the symbolic link itself"},
			{Name: "no-dereference", Short: 'h', Long: "no-dereference", Help: "affect symbolic links instead of any referenced file"},
		},
		Args: []ArgSpec{
			{Name: "group", ValueName: "GROUP", Help: "new group name or numeric ID"},
			{Name: "file", ValueName: "FILE", Repeatable: true, Help: "files to change"},
		},
		Parse: ParseConfig{
			InferLongOptions:      true,
			GroupShortOptions:     true,
			LongOptionValueEquals: true,
			AutoVersion:           true,
		},
	}
}

func (c *Chgrp) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	spec := c.Spec()
	if matches.Has("help") {
		return RenderCommandHelp(inv.Stdout, &spec)
	}

	opts, err := parseChgrpMatches(inv, matches)
	if err != nil {
		return err
	}
	return runChgrp(ctx, inv, &opts)
}

type chgrpOptions struct {
	groupSpec    string
	fromSpec     string
	fromSet      bool
	reference    string
	referenceSet bool
	files        []string
	gid          *uint32
	filter       permissionIfFrom
	verbosity    permissionVerbosity
	walk         permissionWalkOptions
}

func parseChgrpMatches(inv *Invocation, matches *ParsedCommand) (chgrpOptions, error) {
	opts := chgrpOptions{
		fromSpec:     matches.Value("from"),
		fromSet:      matches.Has("from"),
		reference:    matches.Value("reference"),
		referenceSet: matches.Has("reference"),
		verbosity: permissionVerbosity{
			level:      permissionVerbosityNormal,
			groupsOnly: true,
		},
	}

	recursive := false
	preserveRoot := false
	traverse := permissionTraverseNone
	var dereference *bool

	for _, name := range matches.OptionOrder() {
		switch name {
		case "changes":
			opts.verbosity.level = permissionVerbosityChanges
		case "quiet":
			opts.verbosity.level = permissionVerbositySilent
		case "verbose":
			opts.verbosity.level = permissionVerbosityVerbose
		case "recursive":
			recursive = true
		case "preserve-root":
			preserveRoot = true
		case "no-preserve-root":
			preserveRoot = false
		case "traverse-first":
			traverse = permissionTraverseFirst
		case "traverse-all":
			traverse = permissionTraverseAll
		case "traverse-none":
			traverse = permissionTraverseNone
		case "dereference":
			value := true
			dereference = &value
		case "no-dereference":
			value := false
			dereference = &value
		}
	}

	walk, err := normalizePermissionWalkOptionsForCommand(inv, "chgrp", recursive, dereference, traverse, preserveRoot)
	if err != nil {
		return chgrpOptions{}, err
	}
	opts.walk = walk

	positionals := matches.Positionals()
	if opts.referenceSet {
		if len(positionals) == 0 {
			return chgrpOptions{}, exitf(inv, 1, "chgrp: missing operand after %s", opts.reference)
		}
		opts.files = positionals
		return opts, nil
	}

	if len(positionals) < 2 {
		return chgrpOptions{}, exitf(inv, 1, "chgrp: missing operand")
	}
	opts.groupSpec = positionals[0]
	opts.files = positionals[1:]
	return opts, nil
}

func runChgrp(ctx context.Context, inv *Invocation, opts *chgrpOptions) error {
	db := loadPermissionIdentityDB(ctx, inv)
	var err error

	if opts.fromSet {
		opts.filter, err = parseChgrpFilterSpec(inv, db, opts.fromSpec)
		if err != nil {
			return err
		}
	}

	if opts.referenceSet {
		info, _, err := statPath(ctx, inv, opts.reference)
		if err != nil {
			return err
		}
		owner := permissionLookupOwnership(db, info)
		opts.gid = &owner.gid
	} else {
		opts.gid, err = parseChgrpTargetGroupSpec(inv, db, opts.groupSpec)
		if err != nil {
			return err
		}
	}

	return runPermissionApply(ctx, inv, db, &permissionApplyOptions{
		commandName: "chgrp",
		files:       opts.files,
		gid:         opts.gid,
		filter:      opts.filter,
		verbosity:   opts.verbosity,
		walk:        opts.walk,
	})
}

func parseChgrpTargetGroupSpec(inv *Invocation, db *permissionIdentityDB, spec string) (*uint32, error) {
	if spec == "" {
		return nil, nil
	}
	gid, err := parseChgrpGroupID(inv, db, spec, "group")
	if err != nil {
		return nil, err
	}
	return &gid, nil
}

func parseChgrpFilterSpec(inv *Invocation, db *permissionIdentityDB, spec string) (permissionIfFrom, error) {
	if spec == "" {
		return permissionIfFrom{}, nil
	}
	gid, err := parseChgrpGroupID(inv, db, spec, "user")
	if err != nil {
		return permissionIfFrom{}, err
	}
	return permissionIfFrom{
		setGID: true,
		gid:    gid,
	}, nil
}

func parseChgrpGroupID(inv *Invocation, db *permissionIdentityDB, value, invalidLabel string) (uint32, error) {
	group := value
	numericOnly := false
	if trimmed, ok := strings.CutPrefix(group, ":"); ok {
		group = trimmed
		numericOnly = true
	}

	if !numericOnly {
		if db != nil {
			if gid, ok := db.groupsByName[group]; ok {
				return gid, nil
			}
		}
	}

	gid, err := strconv.ParseUint(group, 10, 32)
	if err != nil {
		return 0, exitf(inv, 1, "chgrp: invalid %s: %s", invalidLabel, value)
	}
	return uint32(gid), nil
}

var _ Command = (*Chgrp)(nil)
var _ SpecProvider = (*Chgrp)(nil)
var _ ParsedRunner = (*Chgrp)(nil)
