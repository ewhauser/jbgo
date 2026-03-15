package builtins

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	completionSpecsEnvVar = "GBASH_COMPLETE_SPECS"
	completionDefaultKey  = "__default__"
)

var validCompleteOptions = map[string]struct{}{
	"bashdefault": {},
	"default":     {},
	"dirnames":    {},
	"filenames":   {},
	"noquote":     {},
	"nosort":      {},
	"nospace":     {},
	"plusdirs":    {},
}

type completionSpec struct {
	Wordlist  *string  `json:"wordlist,omitempty"`
	Function  *string  `json:"function,omitempty"`
	Command   *string  `json:"command,omitempty"`
	Options   []string `json:"options,omitempty"`
	Actions   []string `json:"actions,omitempty"`
	IsDefault bool     `json:"isDefault,omitempty"`
}

type Complete struct{}

func NewComplete() *Complete {
	return &Complete{}
}

func (c *Complete) Name() string {
	return "complete"
}

func (c *Complete) Run(_ context.Context, inv *Invocation) error {
	if inv == nil {
		return nil
	}

	specs := parseCompletionSpecs(inv)
	args := inv.Args

	printMode := false
	removeMode := false
	isDefault := false
	wordlist, hasWordlist := "", false
	functionName, hasFunctionName := "", false
	commandText, hasCommandText := "", false
	options := make([]string, 0)
	actions := make([]string, 0)
	commands := make([]string, 0)

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-p":
			printMode = true
		case "-r":
			removeMode = true
		case "-D":
			isDefault = true
		case "-W":
			i++
			if i >= len(args) {
				return exitf(inv, 2, "complete: -W: option requires an argument")
			}
			wordlist, hasWordlist = args[i], true
		case "-F":
			i++
			if i >= len(args) {
				return exitf(inv, 2, "complete: -F: option requires an argument")
			}
			functionName, hasFunctionName = args[i], true
		case "-o":
			i++
			if i >= len(args) {
				return exitf(inv, 2, "complete: -o: option requires an argument")
			}
			opt := args[i]
			if _, ok := validCompleteOptions[opt]; !ok {
				return exitf(inv, 2, "complete: %s: invalid option name", opt)
			}
			options = append(options, opt)
		case "-A":
			i++
			if i >= len(args) {
				return exitf(inv, 2, "complete: -A: option requires an argument")
			}
			actions = append(actions, args[i])
		case "-C":
			i++
			if i >= len(args) {
				return exitf(inv, 2, "complete: -C: option requires an argument")
			}
			commandText, hasCommandText = args[i], true
		case "-G", "-P", "-S", "-X":
			i++
			if i >= len(args) {
				return exitf(inv, 2, "complete: %s: option requires an argument", arg)
			}
		case "--":
			commands = append(commands, args[i+1:]...)
			i = len(args)
		default:
			if !strings.HasPrefix(arg, "-") {
				commands = append(commands, arg)
			}
		}
	}

	if removeMode {
		if len(commands) == 0 {
			clear(specs)
		} else {
			for _, cmd := range commands {
				delete(specs, cmd)
			}
		}
		return persistCompletionSpecs(inv, specs)
	}

	if printMode {
		return printCompletionSpecs(inv, specs, commands)
	}

	if len(args) == 0 || (len(commands) == 0 &&
		!hasWordlist &&
		!hasFunctionName &&
		!hasCommandText &&
		len(options) == 0 &&
		len(actions) == 0 &&
		!isDefault) {
		return printCompletionSpecs(inv, specs, nil)
	}

	if hasFunctionName && len(commands) == 0 && !isDefault {
		return exitf(inv, 2, "complete: -F: option requires a command name")
	}

	buildSpec := func(defaultSpec bool) completionSpec {
		spec := completionSpec{IsDefault: defaultSpec}
		if hasWordlist {
			wordlistCopy := wordlist
			spec.Wordlist = &wordlistCopy
		}
		if hasFunctionName {
			functionCopy := functionName
			spec.Function = &functionCopy
		}
		if hasCommandText {
			commandCopy := commandText
			spec.Command = &commandCopy
		}
		if len(options) > 0 {
			spec.Options = append([]string(nil), options...)
		}
		if len(actions) > 0 {
			spec.Actions = append([]string(nil), actions...)
		}
		return spec
	}

	if isDefault {
		specs[completionDefaultKey] = buildSpec(true)
		return persistCompletionSpecs(inv, specs)
	}

	for _, cmd := range commands {
		specs[cmd] = buildSpec(false)
	}
	return persistCompletionSpecs(inv, specs)
}

func printCompletionSpecs(inv *Invocation, specs map[string]completionSpec, commands []string) error {
	if len(specs) == 0 {
		if len(commands) > 0 {
			return exitf(inv, 1, "complete: %s: no completion specification", commands[0])
		}
		return nil
	}

	var targets []string
	if len(commands) > 0 {
		targets = append(targets, commands...)
	} else {
		targets = make([]string, 0, len(specs))
		for name := range specs {
			if name == completionDefaultKey {
				continue
			}
			targets = append(targets, name)
		}
		sort.Strings(targets)
	}

	var output strings.Builder
	for _, name := range targets {
		spec, ok := specs[name]
		if !ok {
			if output.Len() > 0 {
				if _, err := fmt.Fprint(inv.Stdout, output.String()); err != nil {
					return &ExitError{Code: 1, Err: err}
				}
			}
			return exitf(inv, 1, "complete: %s: no completion specification", name)
		}
		output.WriteString(renderCompletionSpec(name, &spec))
		output.WriteByte('\n')
	}

	if output.Len() == 0 {
		return nil
	}
	if _, err := fmt.Fprint(inv.Stdout, output.String()); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func renderCompletionSpec(name string, spec *completionSpec) string {
	var b strings.Builder
	b.WriteString("complete")
	if spec == nil {
		b.WriteByte(' ')
		b.WriteString(name)
		return b.String()
	}
	for _, opt := range spec.Options {
		b.WriteString(" -o ")
		b.WriteString(opt)
	}
	for _, action := range spec.Actions {
		b.WriteString(" -A ")
		b.WriteString(action)
	}
	if spec.Wordlist != nil {
		wordlist := *spec.Wordlist
		b.WriteString(" -W ")
		if needsQuotedWordlist(wordlist) {
			b.WriteString(shellSingleQuote(wordlist))
		} else {
			b.WriteString(wordlist)
		}
	}
	if spec.Function != nil {
		b.WriteString(" -F ")
		b.WriteString(*spec.Function)
	}
	if spec.Command != nil {
		b.WriteString(" -C ")
		b.WriteString(*spec.Command)
	}
	if spec.IsDefault {
		b.WriteString(" -D")
	}
	b.WriteByte(' ')
	b.WriteString(name)
	return b.String()
}

func needsQuotedWordlist(value string) bool {
	return value == "" || strings.ContainsAny(value, " '")
}

func parseCompletionSpecs(inv *Invocation) map[string]completionSpec {
	if inv == nil || len(inv.Env) == 0 {
		return make(map[string]completionSpec)
	}
	raw := strings.TrimSpace(inv.Env[completionSpecsEnvVar])
	if raw == "" {
		return make(map[string]completionSpec)
	}
	specs := make(map[string]completionSpec)
	if err := json.Unmarshal([]byte(raw), &specs); err != nil {
		return make(map[string]completionSpec)
	}
	return specs
}

func persistCompletionSpecs(inv *Invocation, specs map[string]completionSpec) error {
	if inv == nil {
		return nil
	}
	if inv.Env == nil {
		inv.Env = make(map[string]string)
	}
	if len(specs) == 0 {
		delete(inv.Env, completionSpecsEnvVar)
		return nil
	}
	raw, err := json.Marshal(specs)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	inv.Env[completionSpecsEnvVar] = string(raw)
	return nil
}

var _ Command = (*Complete)(nil)
