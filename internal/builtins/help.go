package builtins

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

const bashHelpRelease = "5.3.9"

type Help struct{}

type helpTopic struct {
	Synopsis string
	Summary  string
	Body     string
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

func (c *Help) Run(_ context.Context, inv *Invocation) error {
	short := false
	describe := false
	manpage := false
	args := inv.Args
	for len(args) > 0 {
		switch args[0] {
		case "-s":
			short = true
			args = args[1:]
		case "-d":
			describe = true
			args = args[1:]
		case "-m":
			manpage = true
			args = args[1:]
		case "--":
			args = args[1:]
			goto done
		default:
			goto done
		}
	}
done:
	if len(args) == 0 {
		_, _ = fmt.Fprintf(inv.Stdout, "%s\n%s", bashHelpVersionLine(inv), bashHelpListBody)
		return nil
	}

	exitCode := 0
	for i, arg := range args {
		topic, ok := builtinHelp[arg]
		if !ok {
			exitCode = 1
			_, _ = fmt.Fprintf(inv.Stderr, "help: no help topics match `%s'.  Try `help help' or `man -k %s' or `info %s'.\n", arg, arg, arg)
			continue
		}
		if i > 0 && !short && !describe {
			_, _ = io.WriteString(inv.Stdout, "\n")
		}
		switch {
		case short:
			_, _ = fmt.Fprintf(inv.Stdout, "%s: %s\n", arg, topic.Synopsis)
		case describe:
			_, _ = fmt.Fprintf(inv.Stdout, "%-15s %s\n", arg, topic.Summary)
		case manpage:
			body := strings.TrimSuffix(topic.Body, "\n")
			if body == "" {
				body = fmt.Sprintf("%s: %s\n    %s", arg, topic.Synopsis, topic.Summary)
			}
			_, _ = fmt.Fprintln(inv.Stdout, body)
		default:
			body := topic.Body
			if body == "" {
				body = fmt.Sprintf("%s: %s\n    %s\n", arg, topic.Synopsis, topic.Summary)
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
