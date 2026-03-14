package builtins

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	idDefaultUserName = "agent"
	idDefaultUID      = 1000
	idDefaultGID      = 1000
)

type ID struct{}

func NewID() *ID {
	return &ID{}
}

func (c *ID) Name() string {
	return "id"
}

func (c *ID) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *ID) Spec() CommandSpec {
	return CommandSpec{
		Name:  "id",
		About: "Print user and group information for each specified USER,\n  or (when USER omitted) for the current user.",
		Usage: "id [OPTION]... [USER]...",
		AfterHelp: "The id utility displays the user and group names and numeric IDs, of the\n" +
			"calling process, to the standard output. If the real and effective IDs are\n" +
			"different, both are displayed, otherwise only the real ID is displayed.\n\n" +
			"If a user (login name or user ID) is specified, the user and group IDs of\n" +
			"that user are displayed. In this case, the real and effective IDs are\n" +
			"assumed to be the same.",
		Options: []OptionSpec{
			{Name: "ignore", Short: 'a', Long: "ignore", Help: "ignore, for compatibility with other versions"},
			{Name: "audit", Short: 'A', Help: "Display the process audit user ID and other process audit properties,\n  which requires privilege (not available on Linux)."},
			{Name: "user", Short: 'u', Long: "user", Help: "Display only the effective user ID as a number."},
			{Name: "group", Short: 'g', Long: "group", Help: "Display only the effective group ID as a number"},
			{Name: "groups", Short: 'G', Long: "groups", Help: "Display only the different group IDs as white-space separated numbers,\n  in no particular order."},
			{Name: "human-readable", Short: 'p', Long: "human-readable", Help: "Make the output human-readable. Each display is on a separate line."},
			{Name: "name", Short: 'n', Long: "name", Help: "Display the name of the user or group ID for the -G, -g and -u options\n  instead of the number.\n  If any of the ID numbers cannot be mapped into\n  names, the number will be displayed as usual."},
			{Name: "password", Short: 'P', Long: "password", Help: "Display the id as a password file entry."},
			{Name: "real", Short: 'r', Long: "real", Help: "Display the real ID for the -G, -g and -u options instead of\n  the effective ID."},
			{Name: "zero", Short: 'z', Long: "zero", Help: "delimit entries with NUL characters, not whitespace;\n  not permitted in default format"},
			{Name: "context", Short: 'Z', Long: "context", Help: "print only the security context of the process (not enabled)"},
		},
		Args: []ArgSpec{
			{Name: "user", ValueName: "USER", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:  true,
			GroupShortOptions: true,
			AutoHelp:          true,
			AutoVersion:       true,
		},
	}
}

func (c *ID) RunParsed(_ context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := parseIDMatches(inv, matches)
	if err != nil {
		return err
	}

	current := idCurrentIdentity(inv)
	delimiter := " "
	lineEnding := "\n"
	if opts.zero {
		delimiter = "\x00"
		lineEnding = "\x00"
	}

	if opts.context {
		if len(opts.users) > 0 {
			return exitf(inv, 1, "id: cannot print security context when user specified")
		}
		contextValue := strings.TrimSpace(inv.Env["GBASH_SECURITY_CONTEXT"])
		if contextValue == "" {
			return exitf(inv, 1, "id: --context (-Z) works only on an SELinux/SMACK-enabled kernel")
		}
		_, err := fmt.Fprint(inv.Stdout, contextValue, lineEnding)
		if err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		return nil
	}

	var hadError bool
	targets := opts.users
	if len(targets) == 0 {
		targets = []string{""}
	}

	for idx, rawUser := range targets {
		identity, ok := idLookupIdentity(&current, rawUser)
		if !ok {
			hadError = true
			_, _ = fmt.Fprintf(inv.Stderr, "id: %s: no such user\n", rawUser)
			continue
		}

		output, err := idFormatOutput(&identity, opts, delimiter, len(opts.users) > 1)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprint(inv.Stdout, output, lineEnding); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		if idx == len(targets)-1 {
			continue
		}
	}

	if hadError {
		return &ExitError{Code: 1}
	}
	return nil
}

func parseIDMatches(inv *Invocation, matches *ParsedCommand) (idOptions, error) {
	opts := idOptions{
		ignore:        matches.Has("ignore"),
		audit:         matches.Has("audit"),
		context:       matches.Has("context"),
		userOnly:      matches.Has("user"),
		groupOnly:     matches.Has("group"),
		groupsOnly:    matches.Has("groups"),
		humanReadable: matches.Has("human-readable"),
		nameOnly:      matches.Has("name"),
		passwordStyle: matches.Has("password"),
		realOnly:      matches.Has("real"),
		zero:          matches.Has("zero"),
		users:         matches.Args("user"),
	}

	defaultFormat := !opts.userOnly && !opts.groupOnly && !opts.groupsOnly
	if (opts.nameOnly || opts.realOnly) && defaultFormat && !opts.context {
		return idOptions{}, exitf(inv, 1, "id: printing only names or real IDs requires -u, -g, or -G")
	}
	if opts.zero && defaultFormat && !opts.context {
		return idOptions{}, exitf(inv, 1, "id: option --zero not permitted in default format")
	}
	if opts.context && len(opts.users) > 0 {
		return idOptions{}, exitf(inv, 1, "id: cannot print security context when user specified")
	}
	if opts.userOnly && opts.groupOnly {
		return idOptions{}, exitf(inv, 1, "id: cannot print \"only\" of more than one choice")
	}
	if opts.groupsOnly && (opts.userOnly || opts.groupOnly || opts.context || opts.humanReadable || opts.passwordStyle || opts.audit) {
		return idOptions{}, exitf(inv, 1, "id: cannot print \"only\" of more than one choice")
	}
	if opts.context && (opts.userOnly || opts.groupOnly) {
		return idOptions{}, exitf(inv, 1, "id: cannot print \"only\" of more than one choice")
	}
	if opts.passwordStyle && opts.humanReadable {
		return idOptions{}, exitf(inv, 1, "id: the argument '-P' cannot be used with '-p'")
	}
	if opts.audit && (opts.groupOnly || opts.userOnly || opts.humanReadable || opts.passwordStyle || opts.groupsOnly || opts.zero) {
		return idOptions{}, exitf(inv, 1, "id: cannot print \"only\" of more than one choice")
	}
	return opts, nil
}

type idOptions struct {
	ignore        bool
	audit         bool
	context       bool
	userOnly      bool
	groupOnly     bool
	groupsOnly    bool
	humanReadable bool
	nameOnly      bool
	passwordStyle bool
	realOnly      bool
	zero          bool
	users         []string
}

type idIdentity struct {
	userName string
	uid      uint32
	euid     uint32
	group    idGroup
	egid     uint32
	groups   []idGroup
	homeDir  string
	shell    string
}

type idGroup struct {
	id   uint32
	name string
}

func idCurrentIdentity(inv *Invocation) idIdentity {
	userName := strings.TrimSpace(inv.Env["USER"])
	if userName == "" {
		userName = strings.TrimSpace(inv.Env["LOGNAME"])
	}
	if userName == "" {
		userName = idDefaultUserName
	}

	uid := idUintEnv(inv.Env, "UID", idDefaultUID)
	euid := idUintEnv(inv.Env, "EUID", uid)
	gid := idUintEnv(inv.Env, "GID", idDefaultGID)
	egid := idUintEnv(inv.Env, "EGID", gid)

	groupName := strings.TrimSpace(inv.Env["GROUP"])
	if groupName == "" {
		groupName = userName
	}

	groups := idGroupsFromEnv(inv.Env, gid, groupName)
	if len(groups) == 0 {
		groups = []idGroup{{id: gid, name: groupName}}
	}

	homeDir := strings.TrimSpace(inv.Env["HOME"])
	if homeDir == "" {
		homeDir = "/home/agent"
	}
	shellPath := strings.TrimSpace(inv.Env["SHELL"])
	if shellPath == "" {
		shellPath = "/bin/sh"
	}

	return idIdentity{
		userName: userName,
		uid:      uid,
		euid:     euid,
		group: idGroup{
			id:   gid,
			name: groupName,
		},
		egid:    egid,
		groups:  groups,
		homeDir: homeDir,
		shell:   shellPath,
	}
}

func idUintEnv(env map[string]string, key string, fallback uint32) uint32 {
	raw := strings.TrimSpace(env[key])
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return fallback
	}
	return uint32(value)
}

func idGroupsFromEnv(env map[string]string, primaryID uint32, primaryName string) []idGroup {
	raw := strings.TrimSpace(env["GROUPS"])
	if raw == "" {
		return []idGroup{{id: primaryID, name: primaryName}}
	}

	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})

	groups := make([]idGroup, 0, len(fields)+1)
	seen := map[uint32]struct{}{}
	appendGroup := func(id uint32, name string) {
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		groups = append(groups, idGroup{id: id, name: name})
	}

	appendGroup(primaryID, primaryName)
	for _, field := range fields {
		value, err := strconv.ParseUint(field, 10, 32)
		if err != nil {
			continue
		}
		id := uint32(value)
		name := strconv.FormatUint(value, 10)
		if id == primaryID {
			name = primaryName
		}
		appendGroup(id, name)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].id < groups[j].id })
	if len(groups) > 1 && groups[0].id != primaryID {
		for i := range groups {
			if groups[i].id == primaryID {
				groups[0], groups[i] = groups[i], groups[0]
				break
			}
		}
	}
	return groups
}

func idLookupIdentity(current *idIdentity, user string) (idIdentity, bool) {
	if current == nil {
		return idIdentity{}, false
	}
	if user == "" {
		return *current, true
	}
	if user == current.userName || user == strconv.FormatUint(uint64(current.uid), 10) {
		return *current, true
	}
	return idIdentity{}, false
}

func idFormatOutput(identity *idIdentity, opts idOptions, delimiter string, multiUser bool) (string, error) {
	if identity == nil {
		return "", nil
	}
	if opts.audit {
		return "", nil
	}
	if opts.passwordStyle {
		return fmt.Sprintf("%s:x:%d:%d::%s:%s", identity.userName, identity.uid, identity.group.id, identity.homeDir, identity.shell), nil
	}
	if opts.humanReadable {
		return idPretty(identity), nil
	}
	if opts.groupOnly {
		idValue := identity.group.id
		if !opts.realOnly {
			idValue = identity.egid
		}
		if opts.nameOnly {
			return identity.group.name, nil
		}
		return strconv.FormatUint(uint64(idValue), 10), nil
	}
	if opts.userOnly {
		idValue := identity.uid
		if !opts.realOnly {
			idValue = identity.euid
		}
		if opts.nameOnly {
			return identity.userName, nil
		}
		return strconv.FormatUint(uint64(idValue), 10), nil
	}
	if opts.groupsOnly {
		parts := make([]string, 0, len(identity.groups))
		for _, group := range identity.groups {
			if opts.nameOnly {
				parts = append(parts, group.name)
				continue
			}
			parts = append(parts, strconv.FormatUint(uint64(group.id), 10))
		}
		out := strings.Join(parts, delimiter)
		if opts.zero && multiUser {
			out += "\x00"
		}
		return out, nil
	}

	parts := []string{
		fmt.Sprintf("uid=%d(%s)", identity.uid, identity.userName),
		fmt.Sprintf("gid=%d(%s)", identity.group.id, identity.group.name),
	}
	if identity.euid != identity.uid {
		parts = append(parts, fmt.Sprintf("euid=%d(%s)", identity.euid, identity.userName))
	}
	if identity.egid != identity.group.id {
		parts = append(parts, fmt.Sprintf("egid=%d(%s)", identity.egid, identity.group.name))
	}

	groupParts := make([]string, 0, len(identity.groups))
	for _, group := range identity.groups {
		groupParts = append(groupParts, fmt.Sprintf("%d(%s)", group.id, group.name))
	}
	parts = append(parts, "groups="+strings.Join(groupParts, ","))
	return strings.Join(parts, " "), nil
}

func idPretty(identity *idIdentity) string {
	if identity == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "uid\t%s\n", identity.userName)
	if identity.euid != identity.uid {
		fmt.Fprintf(&b, "euid\t%s\n", identity.userName)
	}
	if identity.egid != identity.group.id {
		fmt.Fprintf(&b, "rgid\t%s\n", identity.group.name)
	}
	b.WriteString("groups\t")
	names := make([]string, 0, len(identity.groups))
	for _, group := range identity.groups {
		names = append(names, group.name)
	}
	b.WriteString(strings.Join(names, " "))
	return b.String()
}

var _ Command = (*ID)(nil)
var _ SpecProvider = (*ID)(nil)
var _ ParsedRunner = (*ID)(nil)
