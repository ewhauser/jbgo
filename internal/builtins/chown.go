package builtins

import (
	"context"
)

type Chown struct{}

func NewChown() *Chown {
	return &Chown{}
}

func (c *Chown) Name() string {
	return "chown"
}

func (c *Chown) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Chown) Spec() CommandSpec {
	return CommandSpec{
		Name:  "chown",
		About: "Change the owner and/or group of each FILE to OWNER and/or GROUP.",
		Usage: "chown [OPTION]... [OWNER][:[GROUP]] FILE...\n  or:  chown [OPTION]... --reference=RFILE FILE...",
		Options: []OptionSpec{
			{Name: "help", Long: "help", Help: "display this help and exit"},
			{Name: "changes", Short: 'c', Long: "changes", Help: "like verbose but report only when a change is made"},
			{Name: "from", Long: "from", ValueName: "CURRENT_OWNER:CURRENT_GROUP", Arity: OptionRequiredValue, Help: "change the owner and/or group of each file only if its current owner and/or group match those specified here"},
			{Name: "preserve-root", Long: "preserve-root", Help: "fail to operate recursively on '/'"},
			{Name: "no-preserve-root", Long: "no-preserve-root", Help: "do not treat '/' specially"},
			{Name: "quiet", Short: 'f', Long: "quiet", Aliases: []string{"silent"}, HelpAliases: []string{"silent"}, Help: "suppress most error messages"},
			{Name: "recursive", Short: 'R', Long: "recursive", Help: "operate on files and directories recursively"},
			{Name: "reference", Long: "reference", ValueName: "RFILE", Arity: OptionRequiredValue, Help: "use RFILE's owner and group rather than specifying OWNER:GROUP values"},
			{Name: "verbose", Short: 'v', Long: "verbose", Help: "output a diagnostic for every file processed"},
			{Name: "traverse-first", Short: 'H', Help: "if a command line argument is a symbolic link to a directory, traverse it"},
			{Name: "traverse-all", Short: 'L', Help: "traverse every symbolic link to a directory encountered"},
			{Name: "traverse-none", Short: 'P', Help: "do not traverse any symbolic links (default)"},
			{Name: "dereference", Long: "dereference", Help: "affect the referent of each symbolic link rather than the symbolic link itself"},
			{Name: "no-dereference", Short: 'h', Long: "no-dereference", Help: "affect symbolic links instead of any referenced file"},
		},
		Args: []ArgSpec{
			{Name: "owner", ValueName: "OWNER", Help: "new owner name or numeric ID, optionally with :GROUP"},
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

func (c *Chown) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	spec := c.Spec()
	if matches.Has("help") {
		return RenderCommandHelp(inv.Stdout, &spec)
	}

	opts, err := parseChownMatches(inv, matches)
	if err != nil {
		return err
	}

	db := loadPermissionIdentityDB(ctx, inv)
	if opts.fromSet {
		opts.filter, err = parsePermissionFilterSpec(inv, db, opts.fromSpec)
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
	fromSpec     string
	fromSet      bool
	ownerSpec    string
	reference    string
	referenceSet bool
	files        []string
	uid          *uint32
	gid          *uint32
	filter       permissionIfFrom
	verbosity    permissionVerbosity
	walk         permissionWalkOptions
}

func parseChownMatches(inv *Invocation, matches *ParsedCommand) (chownOptions, error) {
	opts := chownOptions{
		fromSpec:     matches.Value("from"),
		fromSet:      matches.Has("from"),
		reference:    matches.Value("reference"),
		referenceSet: matches.Has("reference"),
		verbosity: permissionVerbosity{
			level: permissionVerbosityNormal,
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

	walk, err := normalizePermissionWalkOptionsForCommand(inv, "chown", recursive, dereference, traverse, preserveRoot)
	if err != nil {
		return chownOptions{}, err
	}
	opts.walk = walk

	positionals := matches.Positionals()
	if opts.referenceSet {
		if len(positionals) == 0 {
			return chownOptions{}, exitf(inv, 1, "chown: missing operand after %s", opts.reference)
		}
		opts.files = positionals
		return opts, nil
	}

	if len(positionals) < 2 {
		return chownOptions{}, exitf(inv, 1, "chown: missing operand")
	}
	opts.ownerSpec = positionals[0]
	opts.files = positionals[1:]
	return opts, nil
}

var _ Command = (*Chown)(nil)
var _ SpecProvider = (*Chown)(nil)
var _ ParsedRunner = (*Chown)(nil)
