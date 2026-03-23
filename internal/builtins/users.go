package builtins

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type Users struct{}

func NewUsers() *Users {
	return &Users{}
}

func (c *Users) Name() string {
	return "users"
}

func (c *Users) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Users) Spec() CommandSpec {
	return CommandSpec{
		Name:  c.Name(),
		About: "Print the user names of users currently logged in to the current host",
		Usage: "users [OPTION]... [FILE]",
		AfterHelp: fmt.Sprintf(
			"Output who is currently logged in according to FILE.\nIf FILE is not specified, use %s.",
			whoDefaultFile(),
		),
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE"},
		},
		Options: []OptionSpec{
			{
				Name:  "version",
				Short: 'V',
				Long:  "version",
				Help:  "output version information and exit",
			},
		},
		Parse: ParseConfig{
			InferLongOptions: true,
			AutoHelp:         true,
		},
	}
}

func (c *Users) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("version") {
		return RenderSimpleVersion(inv.Stdout, c.Name())
	}

	file := whoDefaultFile()
	if f := matches.Arg("file"); f != "" {
		file = f
	}

	records, err := whoReadRecords(ctx, inv, file)
	if err != nil {
		return err
	}

	users := make([]string, 0, len(records))
	for _, record := range records {
		if record.isUserProcess() {
			users = append(users, record.user)
		}
	}

	if len(users) > 0 {
		sort.Strings(users)
		if _, err := fmt.Fprintln(inv.Stdout, strings.Join(users, " ")); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}

	return nil
}

var _ Command = (*Users)(nil)
var _ SpecProvider = (*Users)(nil)
var _ ParsedRunner = (*Users)(nil)
