package builtins

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ewhauser/gbash/internal/completionutil"
	"github.com/ewhauser/gbash/internal/shell/interp"
)

const bashHelpRelease = "5.3.9"

type Help struct{}

type helpMode uint8

const (
	helpModeDefault helpMode = iota
	helpModeShort
	helpModeManpage
	helpModeDescribe
)

type helpTopic struct {
	DisplayName string
	Synopsis    string
	Summary     string
	Body        string
}

var builtinHelp = map[string]helpTopic{
	"cd": {
		Synopsis: "cd [-L|[-P [-e]]] [-@] [dir]",
		Summary:  "Change the shell working directory.",
		Body: "cd: cd [-L|[-P [-e]]] [-@] [dir]\n" +
			"    Change the shell working directory.\n" +
			"    \n" +
			"    Change the current directory to DIR.  The default DIR is the value of the\n" +
			"    HOME shell variable. If DIR is \"-\", it is converted to $OLDPWD.\n" +
			"    \n" +
			"    Options:\n" +
			"      -L\tforce symbolic links to be followed: resolve symbolic\n" +
			"    \t\tlinks in DIR after processing instances of `..'\n" +
			"      -P\tuse the physical directory structure without following\n" +
			"    \t\tsymbolic links\n" +
			"      -e\tif the -P option is supplied, and the current working\n" +
			"    \t\tdirectory cannot be determined successfully, exit with\n" +
			"    \t\ta non-zero status\n" +
			"      -@\ton systems that support it, present a file with extended\n" +
			"    \t\tattributes as a directory containing the file attributes\n" +
			"    \n" +
			"    The default is to follow symbolic links, as if `-L' were specified.\n" +
			"    `..' is processed by removing the immediately previous pathname component\n" +
			"    back to a slash or the beginning of DIR.\n" +
			"    \n" +
			"    Exit Status:\n" +
			"    Returns 0 if the directory is changed, and if $PWD is set successfully when\n" +
			"    -P is used; non-zero otherwise.\n",
	},
	"complete": {
		Synopsis: "complete [-abcdefgjksuv] [-pr] [-DEI] [-o option] [-A action] [-G globpat] [-W wordlist] [-F function] [-C command] [-X filterpat] [-P prefix] [-S suffix] [name ...]",
		Summary:  "Specify how arguments are to be completed by Readline.",
	},
	"compgen": {
		Synopsis: "compgen [-V varname] [-abcdefgjksuv] [-o option] [-A action] [-G globpat] [-W wordlist] [-F function] [-C command] [-X filterpat] [-P prefix] [-S suffix] [word]",
		Summary:  "Display possible completions depending on the options.",
	},
	"compopt": {
		Synopsis: "compopt [-o|+o option] [-DEI] [name ...]",
		Summary:  "Modify or display completion options.",
	},
	"echo": {
		Synopsis: "echo [-neE] [arg ...]",
		Summary:  "Write arguments to the standard output.",
	},
	"export": {
		Synopsis: "export [-fn] [name[=value] ...] or export -p",
		Summary:  "Set export attribute for shell variables.",
	},
	"false": {
		Synopsis: "false",
		Summary:  "Return an unsuccessful result.",
	},
	"help": {
		Synopsis: "help [-dms] [pattern ...]",
		Summary:  "Display information about builtin commands.",
		Body: "help: help [-dms] [pattern ...]\n" +
			"    Display information about builtin commands.\n" +
			"    \n" +
			"    Displays brief summaries of builtin commands.  If PATTERN is\n" +
			"    specified, gives detailed help on all commands matching PATTERN,\n" +
			"    otherwise the list of help topics is printed.\n" +
			"    \n" +
			"    Options:\n" +
			"      -d\toutput short description for each topic\n" +
			"      -m\tdisplay usage in pseudo-manpage format\n" +
			"      -s\toutput only a short usage synopsis for each topic matching\n" +
			"    \t\tPATTERN\n" +
			"    \n" +
			"    Arguments:\n" +
			"      PATTERN\tPattern specifying a help topic\n" +
			"    \n" +
			"    Exit Status:\n" +
			"    Returns success unless PATTERN is not found or an invalid option is given.\n",
	},
	"history": {
		Synopsis: "history [-c] [-d offset] [n] or history -anrw [filename] or history -ps arg [arg ...]",
		Summary:  "Display or manipulate the history list.",
	},
	"pwd": {
		Synopsis: "pwd [-LP]",
		Summary:  "Print the name of the current working directory.",
		Body: "pwd: pwd [-LP]\n" +
			"    Print the name of the current working directory.\n" +
			"    \n" +
			"    Options:\n" +
			"      -L\tprint the value of $PWD if it names the current working\n" +
			"    \t\tdirectory\n" +
			"      -P\tprint the physical directory, without any symbolic links\n" +
			"    \n" +
			"    By default, `pwd' behaves as if `-L' were specified.\n" +
			"    \n" +
			"    Exit Status:\n" +
			"    Returns 0 unless an invalid option is given or the current directory\n" +
			"    cannot be read.\n",
	},
}

var builtinHelpFallback = map[string]helpTopic{
	"!":        bashHelpTopic("!: ! PIPELINE", "! - Execute PIPELINE, which can be a simple command, and negate PIPELINE's return status."),
	".":        bashHelpTopic(".: . [-p path] filename [arguments]", ". - Execute commands from a file in the current shell."),
	":":        bashHelpTopic(":: :", ": - Null command."),
	"[":        bashHelpTopic("[: [ arg... ]", "[ - Evaluate conditional expression."),
	"[[":       bashHelpTopic("[[ ... ]]: [[ expression ]]", "[[ ... ]] - Execute conditional command."),
	"((":       bashHelpTopic("(( ... )): (( expression ))", "(( ... )) - Evaluate arithmetic expression."),
	"{":        bashHelpTopic("{ ... }: { COMMANDS ; }", "{ ... } - Group commands as a unit."),
	"alias":    bashHelpTopic("alias: alias [-p] [name[=value] ... ]", "alias - Define or display aliases."),
	"bg":       bashHelpTopic("bg: bg [job_spec ...]", "bg - Move jobs to the background."),
	"bind":     bashHelpTopic("bind: bind [-lpsvPSVX] [-m keymap] [-f filename] [-q name] [-u name] [-r keyseq] [-x keyseq:shell-command] [keyseq:readline-function or readline-command]", "bind - Set Readline key bindings and variables."),
	"break":    bashHelpTopic("break: break [n]", "break - Exit for, while, or until loops."),
	"builtin":  bashHelpTopic("builtin: builtin [shell-builtin [arg ...]]", "builtin - Execute shell builtins."),
	"caller":   bashHelpTopic("caller: caller [expr]", "caller - Return the context of the current subroutine call."),
	"case":     bashHelpTopic("case: case WORD in [PATTERN [| PATTERN]...) COMMANDS ;;]... esac", "case - Execute commands based on pattern matching."),
	"command":  bashHelpTopic("command: command [-pVv] command [arg ...]", "command - Execute a simple command or display information about commands."),
	"continue": bashHelpTopic("continue: continue [n]", "continue - Resume for, while, or until loops."),
	"coproc":   bashHelpTopic("coproc: coproc [NAME] command [redirections]", "coproc - Create a coprocess named NAME."),
	"declare":  bashHelpTopic("declare: declare [-aAfFgiIlnrtux] [name[=value] ...] or declare -p [-aAfFilnrtux] [name ...]", "declare - Set variable values and attributes."),
	"dirs":     bashHelpTopic("dirs: dirs [-clpv] [+N] [-N]", "dirs - Display directory stack."),
	"disown":   bashHelpTopic("disown: disown [-h] [-ar] [jobspec ... | pid ...]", "disown - Remove jobs from current shell."),
	"enable":   bashHelpTopic("enable: enable [-a] [-dnps] [-f filename] [name ...]", "enable - Enable and disable shell builtins."),
	"eval":     bashHelpTopic("eval: eval [arg ...]", "eval - Execute arguments as a shell command."),
	"exec":     bashHelpTopic("exec: exec [-cl] [-a name] [command [argument ...]] [redirection ...]", "exec - Replace the shell with the given command."),
	"exit":     bashHelpTopic("exit: exit [n]", "exit - Exit the shell."),
	"fc":       bashHelpTopic("fc: fc [-e ename] [-lnr] [first] [last] or fc -s [pat=rep] [command]", "fc - Display or execute commands from the history list."),
	"fg":       bashHelpTopic("fg: fg [job_spec]", "fg - Move job to the foreground."),
	"for":      bashHelpTopic("for: for NAME [in WORDS ... ] ; do COMMANDS; done", "for - Execute commands for each member in a list."),
	"function": bashHelpTopic("function: function name { COMMANDS ; } or name () { COMMANDS ; }", "function - Define shell function."),
	"getopts":  bashHelpTopic("getopts: getopts optstring name [arg ...]", "getopts - Parse option arguments."),
	"hash":     bashHelpTopic("hash: hash [-lr] [-p pathname] [-dt] [name ...]", "hash - Remember or display program locations."),
	"if":       bashHelpTopic("if: if COMMANDS; then COMMANDS; [ elif COMMANDS; then COMMANDS; ]... [ else COMMANDS; ] fi", "if - Execute commands based on conditional."),
	"jobs":     bashHelpTopic("jobs: jobs [-lnprs] [jobspec ...] or jobs -x command [args]", "jobs - Display status of jobs."),
	"kill":     bashHelpTopic("kill: kill [-s sigspec | -n signum | -sigspec] pid | jobspec ... or kill -l [sigspec]", "kill - Send a signal to a job."),
	"let":      bashHelpTopic("let: let arg [arg ...]", "let - Evaluate arithmetic expressions."),
	"local":    bashHelpTopic("local: local [option] name[=value] ...", "local - Define local variables."),
	"logout":   bashHelpTopic("logout: logout [n]", "logout - Exit a login shell."),
	"mapfile":  bashHelpTopic("mapfile: mapfile [-d delim] [-n count] [-O origin] [-s count] [-t] [-u fd] [-C callback] [-c quantum] [array]", "mapfile - Read lines from the standard input into an indexed array variable."),
	"popd":     bashHelpTopic("popd: popd [-n] [+N | -N]", "popd - Remove directories from stack."),
	"printf":   bashHelpTopic("printf: printf [-v var] format [arguments]", "printf - Formats and prints ARGUMENTS under control of the FORMAT."),
	"pushd":    bashHelpTopic("pushd: pushd [-n] [+N | -N | dir]", "pushd - Add directories to stack."),
	"read":     bashHelpTopic("read: read [-Eers] [-a array] [-d delim] [-i text] [-n nchars] [-N nchars] [-p prompt] [-t timeout] [-u fd] [name ...]", "read - Read a line from the standard input and split it into fields."),
	"readarray": bashHelpTopic(
		"readarray: readarray [-d delim] [-n count] [-O origin] [-s count] [-t] [-u fd] [-C callback] [-c quantum] [array]",
		"readarray - Read lines from a file into an array variable.",
	),
	"readonly": bashHelpTopic("readonly: readonly [-aAf] [name[=value] ...] or readonly -p", "readonly - Mark shell variables as unchangeable."),
	"return":   bashHelpTopic("return: return [n]", "return - Return from a shell function."),
	"select":   bashHelpTopic("select: select NAME [in WORDS ... ;] do COMMANDS; done", "select - Select words from a list and execute commands."),
	"set":      bashHelpTopic("set: set [-abefhkmnptuvxBCEHPT] [-o option-name] [--] [-] [arg ...]", "set - Set or unset values of shell options and positional parameters."),
	"shift":    bashHelpTopic("shift: shift [n]", "shift - Shift positional parameters."),
	"shopt":    bashHelpTopic("shopt: shopt [-pqsu] [-o] [optname ...]", "shopt - Set and unset shell options."),
	"source":   bashHelpTopic("source: source [-p path] filename [arguments]", "source - Execute commands from a file in the current shell."),
	"suspend":  bashHelpTopic("suspend: suspend [-f]", "suspend - Suspend shell execution."),
	"test":     bashHelpTopic("test: test [expr]", "test - Evaluate conditional expression."),
	"time":     bashHelpTopic("time: time [-p] pipeline", "time - Report time consumed by pipeline's execution."),
	"times":    bashHelpTopic("times: times", "times - Display process times."),
	"trap":     bashHelpTopic("trap: trap [-Plp] [[action] signal_spec ...]", "trap - Trap signals and other events."),
	"true":     bashHelpTopic("true: true", "true - Return a successful result."),
	"type":     bashHelpTopic("type: type [-afptP] name [name ...]", "type - Display information about command type."),
	"typeset":  bashHelpTopic("typeset: typeset [-aAfFgiIlnrtux] name[=value] ... or typeset -p [-aAfFilnrtux] [name ...]", "typeset - Set variable values and attributes."),
	"ulimit":   bashHelpTopic("ulimit: ulimit [-SHabcdefiklmnpqrstuvxPRT] [limit]", "ulimit - Modify shell resource limits."),
	"umask":    bashHelpTopic("umask: umask [-p] [-S] [mode]", "umask - Display or set file mode mask."),
	"unalias":  bashHelpTopic("unalias: unalias [-a] name [name ...]", "unalias - Remove each NAME from the list of defined aliases."),
	"unset":    bashHelpTopic("unset: unset [-f] [-v] [-n] [name ...]", "unset - Unset values and attributes of shell variables and functions."),
	"until":    bashHelpTopic("until: until COMMANDS; do COMMANDS-2; done", "until - Execute commands as long as a test does not succeed."),
	"variables": bashHelpTopic(
		"variables: variables - Names and meanings of some shell variables",
		"variables - Common shell variable names and usage.",
	),
	"wait":  bashHelpTopic("wait: wait [-fn] [-p var] [id ...]", "wait - Wait for job completion and return exit status."),
	"while": bashHelpTopic("while: while COMMANDS; do COMMANDS-2; done", "while - Execute commands as long as a test succeeds."),
}

const bashHelpListBody = "These shell commands are defined internally.  Type `help' to see this list.\n" +
	"Type `help name' to find out more about the function `name'.\n" +
	"Use `info bash' to find out more about the shell in general.\n" +
	"Use `man -k' or `info' to find out more about commands not in this list.\n" +
	"\n" +
	"A star (*) next to a name means that the command is disabled.\n" +
	"\n" +
	" ! PIPELINE                              history [-c] [-d offset] [n] or hist>\n" +
	" job_spec [&]                            if COMMANDS; then COMMANDS; [ elif C>\n" +
	" (( expression ))                        jobs [-lnprs] [jobspec ...] or jobs >\n" +
	" . [-p path] filename [arguments]        kill [-s sigspec | -n signum | -sigs>\n" +
	" :                                       let arg [arg ...]\n" +
	" [ arg... ]                              local [option] name[=value] ...\n" +
	" [[ expression ]]                        logout [n]\n" +
	" alias [-p] [name[=value] ... ]          mapfile [-d delim] [-n count] [-O or>\n" +
	" bg [job_spec ...]                       popd [-n] [+N | -N]\n" +
	" bind [-lpsvPSVX] [-m keymap] [-f file>  printf [-v var] format [arguments]\n" +
	" break [n]                               pushd [-n] [+N | -N | dir]\n" +
	" builtin [shell-builtin [arg ...]]       pwd [-LP]\n" +
	" caller [expr]                           read [-Eers] [-a array] [-d delim] [>\n" +
	" case WORD in [PATTERN [| PATTERN]...)>  readarray [-d delim] [-n count] [-O >\n" +
	" cd [-L|[-P [-e]]] [-@] [dir]            readonly [-aAf] [name[=value] ...] o>\n" +
	" command [-pVv] command [arg ...]        return [n]\n" +
	" compgen [-V varname] [-abcdefgjksuv] >  select NAME [in WORDS ... ;] do COMM>\n" +
	" complete [-abcdefgjksuv] [-pr] [-DEI]>  set [-abefhkmnptuvxBCEHPT] [-o optio>\n" +
	" compopt [-o|+o option] [-DEI] [name .>  shift [n]\n" +
	" continue [n]                            shopt [-pqsu] [-o] [optname ...]\n" +
	" coproc [NAME] command [redirections]    source [-p path] filename [argument>\n" +
	" declare [-aAfFgiIlnrtux] [name[=value>  suspend [-f]\n" +
	" dirs [-clpv] [+N] [-N]                  test [expr]\n" +
	" disown [-h] [-ar] [jobspec ... | pid >  time [-p] pipeline\n" +
	" echo [-neE] [arg ...]                   times\n" +
	" enable [-a] [-dnps] [-f filename] [na>  trap [-Plp] [[action] signal_spec ..>\n" +
	" eval [arg ...]                          true\n" +
	" exec [-cl] [-a name] [command [argume>  type [-afptP] name [name ...]\n" +
	" exit [n]                                typeset [-aAfFgiIlnrtux] name[=value>\n" +
	" export [-fn] [name[=value] ...] or ex>  ulimit [-SHabcdefiklmnpqrstuvxPRT] [>\n" +
	" false                                   umask [-p] [-S] [mode]\n" +
	" fc [-e ename] [-lnr] [first] [last] o>  unalias [-a] name [name ...]\n" +
	" fg [job_spec]                           unset [-f] [-v] [-n] [name ...]\n" +
	" for NAME [in WORDS ... ] ; do COMMAND>  until COMMANDS; do COMMANDS-2; done\n" +
	" for (( exp1; exp2; exp3 )); do COMMAN>  variables - Names and meanings of so>\n" +
	" function name { COMMANDS ; } or name >  wait [-fn] [-p var] [id ...]\n" +
	" getopts optstring name [arg ...]        while COMMANDS; do COMMANDS-2; done\n" +
	" hash [-lr] [-p pathname] [-dt] [name >  { COMMANDS ; }\n" +
	" help [-dms] [pattern ...]\n"

func NewHelp() *Help {
	return &Help{}
}

func (c *Help) Name() string {
	return "help"
}

func (c *Help) Run(ctx context.Context, inv *Invocation) error {
	mode := helpModeDefault
	args := inv.Args
	for len(args) > 0 {
		arg := args[0]
		switch {
		case arg == "--":
			args = args[1:]
			goto done
		case len(arg) <= 1 || arg[0] != '-':
			goto done
		case strings.HasPrefix(arg, "--"):
			return exitf(inv, 2, "help: --: invalid option\nhelp: usage: help [-dms] [pattern ...]")
		default:
			for i := 1; i < len(arg); i++ {
				switch arg[i] {
				case 's':
					if mode < helpModeShort {
						mode = helpModeShort
					}
				case 'm':
					if mode < helpModeManpage {
						mode = helpModeManpage
					}
				case 'd':
					mode = helpModeDescribe
				default:
					return exitf(inv, 2, "help: -%c: invalid option\nhelp: usage: help [-dms] [pattern ...]", arg[i])
				}
			}
			args = args[1:]
		}
	}
done:
	if len(args) == 0 {
		_, _ = fmt.Fprintf(inv.Stdout, "%s\n%s", bashHelpVersionLine(inv), bashHelpListBodyForContext(ctx))
		return nil
	}

	exitCode := 0
	for i, arg := range args {
		topic, ok := lookupHelpTopicForContext(ctx, arg)
		if !ok {
			exitCode = 1
			_, _ = fmt.Fprintf(inv.Stderr, "help: no help topics match `%s'.  Try `help help' or `man -k %s' or `info %s'.\n", arg, arg, arg)
			continue
		}
		displayName := topic.displayName(arg)
		if i > 0 && mode != helpModeShort && mode != helpModeDescribe {
			_, _ = io.WriteString(inv.Stdout, "\n")
		}
		switch mode {
		case helpModeDescribe:
			_, _ = fmt.Fprintf(inv.Stdout, "%s - %s\n", displayName, topic.Summary)
		case helpModeManpage:
			body := strings.TrimSuffix(topic.Body, "\n")
			if body == "" {
				body = fmt.Sprintf("%s: %s\n    %s", displayName, topic.Synopsis, topic.Summary)
			}
			_, _ = fmt.Fprintln(inv.Stdout, body)
		case helpModeShort:
			_, _ = fmt.Fprintf(inv.Stdout, "%s: %s\n", displayName, topic.Synopsis)
		default:
			body := topic.Body
			if body == "" {
				body = fmt.Sprintf("%s: %s\n    %s\n", displayName, topic.Synopsis, topic.Summary)
			}
			_, _ = io.WriteString(inv.Stdout, body)
		}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func bashHelpVersionLine(inv *Invocation) string {
	if line, ok := bashHelpOracleVersionLine(inv); ok {
		return line
	}
	return fmt.Sprintf("GNU bash, version %s(1)-release (%s)", bashHelpRelease, bashHelpPlatform(inv))
}

func bashHelpListBodyForContext(ctx context.Context) string {
	hc, ok := interp.LookupHandlerContext(ctx)
	if !ok {
		return bashHelpListBody
	}
	active := make(map[string]struct{})
	for _, name := range hc.EnabledBuiltinNames("") {
		active[name] = struct{}{}
	}
	if len(active) == len(completionutil.BuiltinNames("")) {
		return bashHelpListBody
	}
	lines := strings.Split(bashHelpListBody, "\n")
	for i, line := range lines {
		left, sep, right, ok := splitHelpListColumns(line)
		if !ok {
			lines[i], _ = markInactiveHelpField(line, active)
			continue
		}
		var leftChanged, rightChanged bool
		left, leftChanged = markInactiveHelpField(left, active)
		right, rightChanged = markInactiveHelpField(right, active)
		if leftChanged && sep != "" {
			sep = sep[:len(sep)-1]
		}
		if rightChanged && sep != "" {
			sep = sep[1:]
		}
		lines[i] = left + sep + right
	}
	return strings.Join(lines, "\n")
}

func splitHelpListColumns(line string) (left, sep, right string, ok bool) {
	runStart := -1
	for i := 0; i < len(line); i++ {
		if line[i] != ' ' {
			runStart = -1
			continue
		}
		if runStart < 0 {
			runStart = i
			continue
		}
		j := i + 1
		for j < len(line) && line[j] == ' ' {
			j++
		}
		return line[:runStart], line[runStart:j], line[j:], true
	}
	return "", "", "", false
}

func markInactiveHelpField(field string, active map[string]struct{}) (string, bool) {
	trimmed := strings.TrimLeft(field, " ")
	if trimmed == "" {
		return field, false
	}
	name := strings.Fields(trimmed)[0]
	if !completionutil.IsBuiltinName(name) {
		return field, false
	}
	if _, ok := active[name]; ok {
		return field, false
	}
	return "*" + trimmed, true
}

func lookupHelpTopicForContext(ctx context.Context, name string) (helpTopic, bool) {
	if completionutil.IsBuiltinName(name) {
		if hc, ok := interp.LookupHandlerContext(ctx); ok && !hc.IsBuiltin(name) && !hc.IsBuiltinDisabled(name) {
			return helpTopic{}, false
		}
	}
	return lookupHelpTopic(name)
}

func lookupHelpTopic(name string) (helpTopic, bool) {
	if topic, ok := builtinHelp[name]; ok {
		return topic, true
	}
	topic, ok := builtinHelpFallback[name]
	return topic, ok
}

func bashHelpTopic(rawSynopsis, rawSummary string) helpTopic {
	displayName, synopsis, ok := strings.Cut(rawSynopsis, ": ")
	if !ok {
		panic(fmt.Sprintf("invalid bash help synopsis %q", rawSynopsis))
	}
	_, summary, ok := strings.Cut(rawSummary, " - ")
	if !ok {
		panic(fmt.Sprintf("invalid bash help summary %q", rawSummary))
	}
	return helpTopic{
		DisplayName: displayName,
		Synopsis:    synopsis,
		Summary:     summary,
	}
}

func (t helpTopic) displayName(name string) string {
	if t.DisplayName != "" {
		return t.DisplayName
	}
	return name
}

func bashHelpOracleVersionLine(inv *Invocation) (string, bool) {
	line := ""
	if inv != nil && inv.Env != nil {
		line = strings.TrimSpace(inv.Env["GBASH_CONFORMANCE_BASH_VERSION_LINE"])
	}
	if line == "" {
		line = strings.TrimSpace(os.Getenv("GBASH_CONFORMANCE_BASH_VERSION_LINE")) //nolint:forbidigo // help output mirrors the configured bash oracle when conformance is running.
	}
	if line == "" {
		return "", false
	}
	return line, line != ""
}

var _ Command = (*Help)(nil)
