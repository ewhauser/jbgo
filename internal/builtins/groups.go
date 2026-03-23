package builtins

import (
	"context"
	"fmt"
	"strings"
)

type Groups struct{}

func NewGroups() *Groups {
	return &Groups{}
}

func (c *Groups) Name() string {
	return "groups"
}

func (c *Groups) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Groups) Spec() CommandSpec {
	return CommandSpec{
		Name:  c.Name(),
		About: "Print group memberships for each USERNAME or, if no USERNAME is specified, for the current process",
		Usage: "groups [OPTION]... [USERNAME]...",
		Options: []OptionSpec{
			{
				Name:  "version",
				Short: 'V',
				Long:  "version",
				Help:  "output version information and exit",
			},
		},
		Args: []ArgSpec{
			{Name: "user", ValueName: "USERNAME", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions: true,
			AutoHelp:         true,
		},
	}
}

func (c *Groups) RunParsed(_ context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("version") {
		return RenderSimpleVersion(inv.Stdout, c.Name())
	}

	current := idCurrentIdentity(inv)
	users := matches.Args("user")

	if len(users) == 0 {
		names := groupNames(current.groups)
		if _, err := fmt.Fprintln(inv.Stdout, strings.Join(names, " ")); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		return nil
	}

	var hadError bool
	for _, user := range users {
		// GNU groups only accepts login names, not numeric UIDs or empty strings.
		if user == "" || user != current.userName {
			hadError = true
			_, _ = fmt.Fprintf(inv.Stderr, "groups: %s: no such user\n", quoteGNUOperand(user))
			continue
		}
		names := groupNames(current.groups)
		if _, err := fmt.Fprintf(inv.Stdout, "%s : %s\n", user, strings.Join(names, " ")); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}

	if hadError {
		return &ExitError{Code: 1}
	}
	return nil
}

func groupNames(groups []idGroup) []string {
	names := make([]string, len(groups))
	for i, g := range groups {
		names[i] = g.name
	}
	return names
}

var _ Command = (*Groups)(nil)
var _ SpecProvider = (*Groups)(nil)
var _ ParsedRunner = (*Groups)(nil)
