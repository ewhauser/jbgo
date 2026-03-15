package builtins

import (
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"os"
	"path"
	"strconv"
	"strings"
)

type Chmod struct{}

const (
	chmodUmaskEnvKey  = "GBASH_UMASK"
	chmodDefaultUmask = 0o022
)

type chmodOptions struct {
	modeSpec        string
	reference       string
	referenceSet    bool
	files           []string
	changes         bool
	quiet           bool
	verbose         bool
	recursive       bool
	preserveRoot    bool
	traverse        permissionTraverseSymlinks
	dereference     *bool
	referenceMode   stdfs.FileMode
	referenceLoaded bool
}

func NewChmod() *Chmod {
	return &Chmod{}
}

func (c *Chmod) Name() string {
	return "chmod"
}

func (c *Chmod) Run(ctx context.Context, inv *Invocation) error {
	spec := c.Spec()
	opts, action, err := parseChmodInvocation(inv, &spec)
	if err != nil {
		return err
	}
	switch action {
	case "help":
		return RenderCommandHelp(inv.Stdout, &spec)
	case "version":
		return RenderCommandVersion(inv.Stdout, &spec)
	default:
		return runChmod(ctx, inv, &opts)
	}
}

func (c *Chmod) NormalizeInvocation(inv *Invocation) *Invocation {
	return inv
}

func (c *Chmod) Spec() CommandSpec {
	return CommandSpec{
		Name:      "chmod",
		About:     "Change the mode of each FILE to MODE.",
		Usage:     "chmod [OPTION]... MODE[,MODE]... FILE...\n  or:  chmod [OPTION]... OCTAL-MODE FILE...\n  or:  chmod [OPTION]... --reference=RFILE FILE...",
		AfterHelp: "Each MODE is of the form [ugoa]*([-+=]([rwxXst]*|[ugo]))+ or an octal number.",
		Options: []OptionSpec{
			{Name: "help", Long: "help", Help: "display this help and exit"},
			{Name: "changes", Short: 'c', Long: "changes", Help: "like verbose but report only when a change is made"},
			{Name: "quiet", Short: 'f', Long: "quiet", Aliases: []string{"silent"}, HelpAliases: []string{"silent"}, Help: "suppress most error messages"},
			{Name: "verbose", Short: 'v', Long: "verbose", Help: "output a diagnostic for every file processed"},
			{Name: "preserve-root", Long: "preserve-root", Help: "fail to operate recursively on '/'"},
			{Name: "no-preserve-root", Long: "no-preserve-root", Help: "do not treat '/' specially"},
			{Name: "reference", Long: "reference", ValueName: "RFILE", Arity: OptionRequiredValue, Help: "use RFILE's mode instead of MODE values"},
			{Name: "recursive", Short: 'R', Long: "recursive", Help: "change files and directories recursively"},
			{Name: "traverse-first", Short: 'H', Help: "if a command line argument is a symbolic link to a directory, traverse it"},
			{Name: "traverse-all", Short: 'L', Help: "traverse every symbolic link to a directory encountered"},
			{Name: "traverse-none", Short: 'P', Help: "do not traverse any symbolic links (default)"},
			{Name: "dereference", Long: "dereference", Aliases: []string{"deref"}, Help: "affect the referent of each symbolic link"},
			{Name: "no-dereference", Short: 'h', Long: "no-dereference", Help: "affect symbolic links instead of referenced files"},
		},
		Args: []ArgSpec{
			{Name: "mode", ValueName: "MODE", Help: "mode to apply"},
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

func (c *Chmod) runParsedShared(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	spec := c.Spec()
	if matches.Has("help") {
		return RenderCommandHelp(inv.Stdout, &spec)
	}
	opts, err := parseChmodMatches(inv, matches)
	if err != nil {
		return err
	}
	return runChmod(ctx, inv, &opts)
}

func isChmodNegativeMode(arg string) bool {
	if len(arg) < 2 || arg[0] != '-' {
		return false
	}
	switch arg[1] {
	case 'r', 'w', 'x', 'X', 's', 't', 'u', 'g', 'o', '0', '1', '2', '3', '4', '5', '6', '7':
		return true
	default:
		return false
	}
}

func parseChmodInvocation(inv *Invocation, spec *CommandSpec) (chmodOptions, string, error) {
	opts := chmodOptions{traverse: permissionTraverseFirst}
	args := append([]string(nil), inv.Args...)
	operandsOnly := false
	modeNegative := false
	var preMode []string
	for len(args) > 0 {
		arg := args[0]
		if !operandsOnly && arg == "--" {
			args = args[1:]
			if opts.modeSpec == "" {
				if len(preMode) == 0 {
					operandsOnly = true
					continue
				}
				break
			}
			opts.files = append(opts.files, args...)
			args = nil
			break
		}
		if !operandsOnly {
			if strings.HasPrefix(arg, "--") && arg != "--" {
				action, remaining, err := parseChmodLongOption(inv, spec, &opts, args)
				if err == nil || action != "" {
					if action != "" {
						return chmodOptions{}, action, nil
					}
					args = remaining
					continue
				}
				if opts.modeSpec == "" {
					return chmodOptions{}, "", err
				}
			}
			if strings.HasPrefix(arg, "-") && arg != "-" && !isChmodNegativeMode(arg) {
				remaining, err := parseChmodShortOptions(inv, &opts, args)
				if err == nil {
					args = remaining
					continue
				}
				if opts.modeSpec == "" {
					return chmodOptions{}, "", err
				}
			}
		}

		if opts.modeSpec == "" {
			if opts.referenceSet {
				break
			}
			if !operandsOnly && !isChmodNegativeMode(arg) {
				preMode = append(preMode, arg)
				args = args[1:]
				continue
			}
			opts.files = append(opts.files, preMode...)
			preMode = nil
			if !operandsOnly && isChmodNegativeMode(arg) {
				opts.modeSpec = arg
				modeNegative = true
				args = args[1:]
				continue
			}
			opts.modeSpec = arg
			args = args[1:]
			continue
		}

		if !operandsOnly && modeNegative && len(opts.files) == 0 && isChmodNegativeMode(arg) {
			opts.modeSpec += "," + arg
			args = args[1:]
			continue
		}

		opts.files = append(opts.files, arg)
		args = args[1:]
	}
	if opts.referenceSet {
		if len(opts.files) == 0 && len(preMode) > 0 {
			opts.files = append(opts.files, preMode...)
		}
		if len(opts.files) == 0 {
			return chmodOptions{}, "", exitf(inv, 1, "chmod: missing operand after %s", quoteGNUOperand(opts.reference))
		}
		return opts, "", nil
	}
	if opts.modeSpec == "" && len(preMode) > 0 {
		opts.modeSpec = preMode[0]
		opts.files = append(opts.files, preMode[1:]...)
	}
	if opts.modeSpec == "" || len(opts.files) == 0 {
		return chmodOptions{}, "", exitf(inv, 1, "chmod: missing operand")
	}
	return opts, "", nil
}

func parseChmodLongOption(inv *Invocation, spec *CommandSpec, opts *chmodOptions, args []string) (string, []string, error) {
	raw := args[0]
	name := strings.TrimPrefix(raw, "--")
	value := ""
	hasValue := false
	if spec != nil && spec.Parse.LongOptionValueEquals {
		name, value, hasValue = strings.Cut(name, "=")
	}
	switch matched := matchChmodLongOption(spec, name); matched {
	case "help":
		if hasValue {
			return "", nil, exitf(inv, 1, "chmod: option '--help' doesn't allow an argument\nTry 'chmod --help' for more information.")
		}
		return "help", args[1:], nil
	case "version":
		if hasValue {
			return "", nil, exitf(inv, 1, "chmod: option '--version' doesn't allow an argument\nTry 'chmod --help' for more information.")
		}
		return "version", args[1:], nil
	case "changes":
		opts.changes = true
	case "quiet":
		opts.quiet = true
	case "verbose":
		opts.verbose = true
	case "preserve-root":
		opts.preserveRoot = true
	case "no-preserve-root":
		opts.preserveRoot = false
	case "reference":
		if !hasValue {
			if len(args) < 2 {
				return "", nil, exitf(inv, 1, "chmod: option '--reference' requires an argument\nTry 'chmod --help' for more information.")
			}
			value = args[1]
			args = args[1:]
		}
		opts.reference = value
		opts.referenceSet = true
	case "recursive":
		opts.recursive = true
	case "dereference":
		v := true
		opts.dereference = &v
	case "no-dereference":
		v := false
		opts.dereference = &v
	default:
		return "", nil, exitf(inv, 1, "chmod: unrecognized option '%s'\nTry 'chmod --help' for more information.", raw)
	}
	return "", args[1:], nil
}

func parseChmodShortOptions(inv *Invocation, opts *chmodOptions, args []string) ([]string, error) {
	raw := args[0]
	for _, ch := range raw[1:] {
		switch ch {
		case 'c':
			opts.changes = true
		case 'f':
			opts.quiet = true
		case 'v':
			opts.verbose = true
		case 'R':
			opts.recursive = true
		case 'H':
			opts.traverse = permissionTraverseFirst
		case 'L':
			opts.traverse = permissionTraverseAll
		case 'P':
			opts.traverse = permissionTraverseNone
		case 'h':
			v := false
			opts.dereference = &v
		default:
			return nil, exitf(inv, 1, "chmod: invalid option -- '%c'\nTry 'chmod --help' for more information.", ch)
		}
	}
	return args[1:], nil
}

func matchChmodLongOption(spec *CommandSpec, name string) string {
	if spec == nil {
		return ""
	}
	var match string
	for i := range spec.Options {
		opt := spec.Options[i]
		names := append([]string{}, opt.Long)
		names = append(names, opt.Aliases...)
		for _, candidate := range names {
			if candidate == "" {
				continue
			}
			if candidate == name {
				return opt.Name
			}
			if spec.Parse.InferLongOptions && strings.HasPrefix(candidate, name) {
				if match == "" {
					match = opt.Name
					continue
				}
				if match != opt.Name {
					return ""
				}
			}
		}
	}
	if spec.Parse.AutoVersion && strings.HasPrefix("version", name) {
		if match != "" && match != "version" {
			return ""
		}
		return "version"
	}
	if spec.Parse.AutoHelp && strings.HasPrefix("help", name) {
		if match != "" && match != "help" {
			return ""
		}
		return "help"
	}
	return match
}

func parseChmodMatches(inv *Invocation, matches *ParsedCommand) (chmodOptions, error) {
	opts := chmodOptions{
		reference:    matches.Value("reference"),
		referenceSet: matches.Has("reference"),
		traverse:     permissionTraverseFirst,
	}
	for _, name := range matches.OptionOrder() {
		switch name {
		case "changes":
			opts.changes = true
		case "quiet":
			opts.quiet = true
		case "verbose":
			opts.verbose = true
		case "recursive":
			opts.recursive = true
		case "preserve-root":
			opts.preserveRoot = true
		case "no-preserve-root":
			opts.preserveRoot = false
		case "traverse-first":
			opts.traverse = permissionTraverseFirst
		case "traverse-all":
			opts.traverse = permissionTraverseAll
		case "traverse-none":
			opts.traverse = permissionTraverseNone
		case "dereference":
			v := true
			opts.dereference = &v
		case "no-dereference":
			v := false
			opts.dereference = &v
		}
	}

	positionals := matches.Positionals()
	if opts.referenceSet {
		if len(positionals) == 0 {
			return chmodOptions{}, exitf(inv, 1, "chmod: missing operand after %s", quoteGNUOperand(opts.reference))
		}
		opts.files = positionals
		return opts, nil
	}
	if len(positionals) < 2 {
		return chmodOptions{}, exitf(inv, 1, "chmod: missing operand")
	}
	opts.modeSpec = positionals[0]
	opts.files = positionals[1:]
	return opts, nil
}

func runChmod(ctx context.Context, inv *Invocation, opts *chmodOptions) error {
	walk, err := normalizePermissionWalkOptionsForCommand(inv, "chmod", opts.recursive, opts.dereference, opts.traverse, opts.preserveRoot)
	if err != nil {
		return err
	}

	if opts.referenceSet {
		info, _, err := statPath(ctx, inv, opts.reference)
		if err != nil {
			return err
		}
		opts.referenceMode = info.Mode()
		opts.referenceLoaded = true
	}

	hadError := false
	for _, target := range opts.files {
		if walk.dereference {
			abs := inv.FS.Resolve(target)
			if linfo, err := inv.FS.Lstat(ctx, abs); err == nil && linfo.Mode()&stdfs.ModeSymlink != 0 {
				if _, err := inv.FS.Stat(ctx, abs); errors.Is(err, stdfs.ErrNotExist) {
					hadError = true
					if !opts.quiet {
						_, _ = fmt.Fprintln(inv.Stderr, fmt.Sprintf("chmod: cannot operate on dangling symlink %s", quoteGNUOperand(target)))
					}
					continue
				}
			}
		}
		err := walkPermissionTarget(ctx, inv, target, walk, func(visit permissionVisit) error {
			linfo, err := inv.FS.Lstat(ctx, visit.Abs)
			if err != nil {
				return err
			}
			if !visit.Follow && linfo.Mode()&stdfs.ModeSymlink != 0 {
				return nil
			}

			before := visit.Info.Mode()
			after, naive, err := chmodDesiredMode(inv, before, opts, visit.Info.IsDir())
			if err != nil {
				return err
			}
			reportPartial := after != naive && chmodReportsPartialImplicitRemoval(opts.modeSpec)
			if reportPartial {
				hadError = true
			}
			if after == before {
				if reportPartial && !opts.quiet {
					_, _ = fmt.Fprintf(inv.Stderr, "chmod: %s: new permissions are %s, not %s\n", chmodDisplayPath(target, visit.Abs), chmodPermissionString(after), chmodPermissionString(naive))
				}
				if opts.verbose {
					if _, err := fmt.Fprintf(inv.Stdout, "mode of %s retained as %s (%s)\n", quoteGNUOperand(chmodDisplayPath(target, visit.Abs)), formatModeOctal(after), chmodPermissionString(after)); err != nil {
						return &ExitError{Code: 1, Err: err}
					}
				}
				return nil
			}
			if err := inv.FS.Chmod(ctx, visit.Abs, after); err != nil {
				return err
			}
			if reportPartial && !opts.quiet {
				_, _ = fmt.Fprintf(inv.Stderr, "chmod: %s: new permissions are %s, not %s\n", chmodDisplayPath(target, visit.Abs), chmodPermissionString(after), chmodPermissionString(naive))
			}
			if opts.verbose || opts.changes {
				if _, err := fmt.Fprintf(inv.Stdout, "mode of %s changed from %s (%s) to %s (%s)\n", quoteGNUOperand(chmodDisplayPath(target, visit.Abs)), formatModeOctal(before), chmodPermissionString(before), formatModeOctal(naive), chmodPermissionString(naive)); err != nil {
					return &ExitError{Code: 1, Err: err}
				}
			}
			return nil
		})
		if err == nil {
			continue
		}
		hadError = true
		if opts.quiet {
			continue
		}
		_, _ = fmt.Fprintln(inv.Stderr, chmodErrorString(inv, target, err, walk))
	}
	if hadError {
		return &ExitError{Code: 1}
	}
	return nil
}

func chmodDesiredMode(inv *Invocation, current stdfs.FileMode, opts *chmodOptions, isDir bool) (mode, naive stdfs.FileMode, err error) {
	if opts.referenceLoaded {
		return opts.referenceMode, opts.referenceMode, nil
	}
	mode, err = computeChmodModeWithUmask(current, opts.modeSpec, chmodCurrentUmask(inv), isDir)
	if err != nil {
		return 0, 0, err
	}
	naive, err = computeChmodModeWithUmask(current, opts.modeSpec, 0, isDir)
	if err != nil {
		return 0, 0, err
	}
	return mode, naive, nil
}

func computeChmodMode(inv *Invocation, current stdfs.FileMode, modeSpec string) (stdfs.FileMode, error) {
	return computeChmodModeWithUmask(current, modeSpec, chmodCurrentUmask(inv), current.IsDir())
}

func computeChmodModeWithUmask(current stdfs.FileMode, modeSpec string, umask stdfs.FileMode, isDir bool) (stdfs.FileMode, error) {
	if modeSpec == "--" {
		return current, nil
	}
	mode := current
	for clause := range strings.SplitSeq(modeSpec, ",") {
		if clause == "" {
			return 0, fmt.Errorf("empty mode")
		}
		newMode, _, err := applyChmodClause(mode, mode, clause, umask, isDir)
		if err != nil {
			return 0, err
		}
		mode = newMode
	}
	return mode, nil
}

func applyChmodClause(current, naive stdfs.FileMode, clause string, umask stdfs.FileMode, isDir bool) (next, nextNaive stdfs.FileMode, err error) {
	if clause == "" {
		return 0, 0, fmt.Errorf("empty clause")
	}
	if isSignedOctalClause(clause) {
		op := clause[0]
		value, err := strconv.ParseUint(clause[1:], 8, 32)
		if err != nil {
			return 0, 0, err
		}
		mask := stdfs.FileMode(value)
		switch op {
		case '+':
			return current | mask, naive | mask, nil
		case '-':
			return current &^ mask, naive &^ mask, nil
		case '=':
			base := current &^ (stdfs.ModePerm | stdfs.ModeSetuid | stdfs.ModeSetgid | stdfs.ModeSticky)
			return base | mask, base | mask, nil
		}
	}
	if isPlainOctalClause(clause) {
		value, err := strconv.ParseUint(clause, 8, 32)
		if err != nil {
			return 0, 0, err
		}
		base := current &^ (stdfs.ModePerm | stdfs.ModeSetuid | stdfs.ModeSetgid | stdfs.ModeSticky)
		mode := base | stdfs.FileMode(value)
		return mode, mode, nil
	}

	idx := strings.IndexAny(clause, "+-=")
	if idx == -1 {
		return 0, 0, fmt.Errorf("invalid clause")
	}
	whoPart := clause[:idx]
	op := clause[idx]
	permPart := clause[idx+1:]
	if permPart == "" && op != '=' {
		return 0, 0, fmt.Errorf("invalid clause")
	}

	whoMask, specialMask, whoExplicit, err := chmodWhoMasks(whoPart)
	if err != nil {
		return 0, 0, err
	}
	permMask, specialPerm, err := chmodPermMasks(permPart, current, whoMask, whoExplicit, umask, isDir)
	if err != nil {
		return 0, 0, err
	}
	naivePermMask, naiveSpecialPerm, err := chmodPermMasks(permPart, naive, whoMask, whoExplicit, 0, isDir)
	if err != nil {
		return 0, 0, err
	}

	next = current
	nextNaive = naive
	switch op {
	case '+':
		next |= permMask
		next |= specialPerm & specialMask
		nextNaive |= naivePermMask
		nextNaive |= naiveSpecialPerm & specialMask
	case '-':
		next &^= permMask
		next &^= specialPerm & specialMask
		nextNaive &^= naivePermMask
		nextNaive &^= naiveSpecialPerm & specialMask
	case '=':
		next &^= whoMask
		next &^= specialMask
		next |= permMask
		next |= specialPerm & specialMask
		nextNaive &^= whoMask
		nextNaive &^= specialMask
		nextNaive |= naivePermMask
		nextNaive |= naiveSpecialPerm & specialMask
	default:
		return 0, 0, fmt.Errorf("invalid operator")
	}
	return next, nextNaive, nil
}

func isPlainOctalClause(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '7' {
			return false
		}
	}
	return true
}

func isSignedOctalClause(value string) bool {
	if len(value) < 2 {
		return false
	}
	switch value[0] {
	case '+', '-', '=':
	default:
		return false
	}
	return isPlainOctalClause(value[1:])
}

func chmodWhoMasks(who string) (permMask, specialMask stdfs.FileMode, explicit bool, err error) {
	if who == "" {
		return 0o777, stdfs.ModeSetuid | stdfs.ModeSetgid | stdfs.ModeSticky, false, nil
	}
	explicit = true
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
			return 0, 0, false, fmt.Errorf("invalid subject")
		}
	}
	return permMask, specialMask, explicit, nil
}

func chmodPermMasks(perms string, current, whoMask stdfs.FileMode, whoExplicit bool, umask stdfs.FileMode, isDir bool) (permMask, specialMask stdfs.FileMode, err error) {
	hasCopy := false
	for _, ch := range perms {
		switch ch {
		case 'r':
			if hasCopy {
				return 0, 0, fmt.Errorf("invalid permission")
			}
			permMask |= 0o444 & whoMask
		case 'w':
			if hasCopy {
				return 0, 0, fmt.Errorf("invalid permission")
			}
			permMask |= 0o222 & whoMask
		case 'x':
			if hasCopy {
				return 0, 0, fmt.Errorf("invalid permission")
			}
			permMask |= 0o111 & whoMask
		case 'X':
			if hasCopy {
				return 0, 0, fmt.Errorf("invalid permission")
			}
			if isDir || current&0o111 != 0 {
				permMask |= 0o111 & whoMask
			}
		case 's':
			if hasCopy {
				return 0, 0, fmt.Errorf("invalid permission")
			}
			specialMask |= chmodSpecialMaskForWho(whoMask, whoExplicit)
		case 't':
			if hasCopy {
				return 0, 0, fmt.Errorf("invalid permission")
			}
			specialMask |= stdfs.ModeSticky
		case 'u', 'g', 'o':
			if hasCopy || len(perms) != 1 {
				return 0, 0, fmt.Errorf("invalid permission")
			}
			hasCopy = true
			permMask |= chmodCopyMask(current, ch, whoMask)
		default:
			return 0, 0, fmt.Errorf("invalid permission")
		}
	}
	if !whoExplicit {
		permMask &^= umask & 0o777
	}
	return permMask, specialMask, nil
}

func chmodSpecialMaskForWho(whoMask stdfs.FileMode, explicit bool) stdfs.FileMode {
	if !explicit {
		return stdfs.ModeSetuid | stdfs.ModeSetgid
	}
	var out stdfs.FileMode
	if whoMask&0o700 != 0 {
		out |= stdfs.ModeSetuid
	}
	if whoMask&0o070 != 0 {
		out |= stdfs.ModeSetgid
	}
	return out
}

func chmodCopyMask(current stdfs.FileMode, source rune, whoMask stdfs.FileMode) stdfs.FileMode {
	var src stdfs.FileMode
	switch source {
	case 'u':
		src = (current & 0o700) >> 6
	case 'g':
		src = (current & 0o070) >> 3
	case 'o':
		src = current & 0o007
	}
	var out stdfs.FileMode
	if whoMask&0o700 != 0 {
		out |= (src << 6) & 0o700
	}
	if whoMask&0o070 != 0 {
		out |= (src << 3) & 0o070
	}
	if whoMask&0o007 != 0 {
		out |= src & 0o007
	}
	return out
}

func chmodCurrentUmask(inv *Invocation) stdfs.FileMode {
	env := map[string]string(nil)
	if inv != nil {
		env = inv.Env
	}
	raw := ""
	if env != nil {
		raw = strings.TrimSpace(env[chmodUmaskEnvKey])
	}
	if raw == "" {
		return chmodDefaultUmask
	}
	value, err := strconv.ParseUint(raw, 8, 32)
	if err != nil {
		return chmodDefaultUmask
	}
	return stdfs.FileMode(value) & 0o777
}

func chmodReportsPartialImplicitRemoval(modeSpec string) bool {
	for clause := range strings.SplitSeq(modeSpec, ",") {
		idx := strings.IndexAny(clause, "+-=")
		if idx == 0 && len(clause) > 1 && clause[0] == '-' {
			return true
		}
	}
	return false
}

func chmodDisplayPath(original, abs string) string {
	if original != "" {
		return original
	}
	return abs
}

func chmodPermissionString(mode stdfs.FileMode) string {
	value := formatModeLong(mode)
	if value != "" {
		return value[1:]
	}
	return value
}

func chmodErrorString(inv *Invocation, target string, err error, walk permissionWalkOptions) string {
	if target == "" {
		target = "."
	}
	message := err.Error()
	if strings.Contains(message, "it is dangerous to operate recursively on") {
		return message
	}
	if strings.Contains(message, "dangling symlink") {
		return fmt.Sprintf("chmod: cannot operate on dangling symlink %s", quoteGNUOperand(target))
	}
	if errors.Is(err, stdfs.ErrNotExist) {
		return fmt.Sprintf("chmod: cannot access %s: No such file or directory", quoteGNUOperand(target))
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && errors.Is(pathErr.Err, stdfs.ErrPermission) {
		return fmt.Sprintf("chmod: %s: Permission denied", quoteGNUOperand(chmodPermissionDeniedPath(inv, target, pathErr.Path)))
	}
	if strings.Contains(strings.ToLower(message), "permission denied") {
		return fmt.Sprintf("chmod: %s: Permission denied", quoteGNUOperand(target))
	}
	if strings.Contains(message, "invalid") {
		return fmt.Sprintf("chmod: invalid mode: %s", target)
	}
	if strings.Contains(message, "unsupported") {
		return "chmod: " + message
	}
	if strings.Contains(message, "missing operand") {
		return "chmod: missing operand"
	}
	_ = walk
	return "chmod: " + message
}

func chmodPermissionDeniedPath(inv *Invocation, fallback, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || inv == nil {
		return fallback
	}
	cwd := path.Clean(strings.TrimSpace(inv.Cwd))
	if cwd == "." || cwd == "" {
		return raw
	}
	if raw == cwd {
		return "."
	}
	if strings.HasPrefix(raw, cwd+"/") {
		return strings.TrimPrefix(raw, cwd+"/")
	}
	return raw
}

var _ Command = (*Chmod)(nil)
var _ SpecProvider = (*Chmod)(nil)
var _ ParseInvocationNormalizer = (*Chmod)(nil)
