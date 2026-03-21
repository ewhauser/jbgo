// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ewhauser/gbash/internal/completionutil"
	"github.com/ewhauser/gbash/internal/printfutil"
	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/syntax"
	"github.com/ewhauser/gbash/internal/shellstate"
)

// TODO: given the categories below, perhaps this should be more like:
//
//   func IsBuiltin(lang syntax.LangVariant, name string) bool
//
// or perhaps some API that also lets the user iterate through the builtins?
//
// Also, should we move this to the syntax package too?
// It's not a syntactical property strictly speaking,
// but it's also odd to require importing the interp package for it.

// IsBuiltin returns true if the given word is a POSIX Shell
// or Bash builtin.
func IsBuiltin(name string) bool {
	return completionutil.IsBuiltinName(name)
}

// TODO: atoi is duplicated in the expand package.

// atoi is like [strconv.ParseInt](s, 10, 64), but it ignores errors and trims whitespace.
func atoi(s string) int64 {
	s = strings.TrimSpace(s)
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

type errBuiltinExitStatus exitStatus

func (e errBuiltinExitStatus) Error() string {
	return fmt.Sprintf("builtin exit status %d", e.code)
}

func (r *Runner) builtin(ctx context.Context, pos syntax.Pos, name string, args []string) (exit exitStatus) {
	failf := func(code uint8, format string, args ...any) exitStatus {
		r.errf(format, args...)
		exit.code = code
		return exit
	}
	switch name {
	case ":", "true":
	case "false":
		exit.code = 1
	case "exit":
		switch len(args) {
		case 0:
			exit = r.lastExit
		case 1:
			n, err := strconv.Atoi(args[0])
			if err != nil {
				return failf(2, "invalid exit status code: %q\n", args[0])
			}
			exit.code = uint8(n)
		default:
			return failf(1, "exit cannot take multiple arguments\n")
		}
		exit.exiting = true
	case "set":
		if len(args) == 0 {
			r.printSetVars()
			return exit
		}
		if err := r.setParams(args...); err != nil {
			return failf(2, "set: %v\n", err)
		}
		r.updateExpandOpts()
	case "shift":
		n := 1
		switch len(args) {
		case 0:
		case 1:
			n2, err := strconv.Atoi(args[0])
			if err != nil {
				exit = failf(2, "shift: %s: numeric argument required\n", args[0])
				exit.exiting = true
				return exit
			}
			n = n2
		default:
			exit = failf(1, "shift: too many arguments\n")
			exit.exiting = true
			return exit
		}
		if n >= len(r.Params) {
			r.Params = nil
		} else {
			r.Params = r.Params[n:]
		}
	case "unset":
		vars := true
		funcs := true
	unsetOpts:
		for i, arg := range args {
			switch arg {
			case "-v":
				funcs = false
			case "-f":
				vars = false
			default:
				args = args[i:]
				break unsetOpts
			}
		}

		for _, arg := range args {
			declaredVar := r.lookupVar(arg).Declared()
			if vars {
				if ref, err := r.strictVarRef(arg); err == nil {
					if ref.Index == nil && !declaredVar {
						// Bash's plain `unset foo` falls through to shell functions
						// when there is no variable by that name.
					} else {
						if err := r.unsetVarByRef(ref, !funcs); err != nil {
							r.errf("unset: %v\n", err)
							exit.code = 1
						}
						continue
					}
				}
				if declaredVar {
					r.delVar(arg)
					continue
				}
			}
			if body := r.funcBody(arg); body != nil && funcs {
				r.delFunc(arg)
			}
		}
	case "echo":
		newline, doExpand := true, false
	echoOpts:
		for len(args) > 0 {
			switch args[0] {
			case "-n":
				newline = false
			case "-e":
				doExpand = true
			case "-E": // default
			default:
				break echoOpts
			}
			args = args[1:]
		}
		for i, arg := range args {
			if i > 0 {
				r.out(" ")
			}
			if doExpand {
				arg, _, _ = expand.Format(r.ecfg, arg, nil)
			}
			r.out(arg)
		}
		if newline {
			r.out("\n")
		}
	case "printf":
		if len(args) == 0 {
			return failf(2, "printf: usage: printf [-v var] format [arguments]\n")
		}
		var destRef *syntax.VarRef
		switch args[0] {
		case "--":
			args = args[1:]
			if len(args) == 0 {
				return failf(2, "printf: usage: printf [-v var] format [arguments]\n")
			}
		case "-v":
			if len(args) < 2 {
				return failf(2, "printf: -v: option requires a variable name\n")
			}
			var err error
			destRef, err = r.strictVarRef(args[1])
			if err != nil {
				return failf(2, "printf: `%s': not a valid identifier\n", args[1])
			}
			args = args[2:]
			if len(args) > 0 && args[0] == "--" {
				args = args[1:]
			}
			if len(args) == 0 {
				return failf(2, "printf: usage: printf [-v var] format [arguments]\n")
			}
		}
		format, args := args[0], args[1:]
		result := printfutil.Format(format, args, printfutil.Options{
			LookupEnv: r.lookupPrintfEnv,
			StartTime: r.startTime,
		})
		for _, diag := range result.Diagnostics {
			r.errf("printf: %s\n", diag)
		}
		if destRef == nil {
			if _, err := io.WriteString(r.stdout, result.Output); err != nil {
				if printfBrokenPipe(err) {
					if result.ExitCode != 0 {
						exit.code = result.ExitCode
					}
					return exit
				}
				return failf(1, "%v\n", err)
			}
			if result.ExitCode != 0 {
				exit.code = result.ExitCode
			}
		}
		if destRef != nil {
			prev := r.lookupVar(destRef.Name.Value)
			vr := prev
			vr.Set = true
			vr.Kind = expand.String
			vr.Str = result.Output
			vr.List = nil
			vr.Indices = nil
			vr.Map = nil
			if err := r.setVarByRef(prev, destRef, vr, false, attrUpdate{}); err != nil {
				return failf(2, "printf: %v\n", err)
			}
			if result.ExitCode != 0 {
				exit.code = result.ExitCode
			}
		}
	case "complete":
		cfg, err := completionutil.ParseCompleteArgs(args)
		if err != nil {
			return failf(2, "%v\n", err)
		}
		state := shellstate.CompletionStateFromContext(ctx)
		if state == nil {
			state = shellstate.NewCompletionState()
		}
		lines, err := completionutil.ApplyComplete(state, newRunnerCompletionBackend(ctx, r, nil), cfg)
		if err != nil {
			code := uint8(2)
			if cfg != nil && cfg.PrintMode {
				code = 1
			}
			return failf(code, "%v\n", err)
		}
		for _, line := range lines {
			r.outf("%s\n", line)
		}
	case "compopt":
		cfg, err := completionutil.ParseCompoptArgs(args)
		if err != nil {
			return failf(2, "%v\n", err)
		}
		state := shellstate.CompletionStateFromContext(ctx)
		if state == nil {
			state = shellstate.NewCompletionState()
		}
		if err := completionutil.ApplyCompopt(state, cfg); err != nil {
			return failf(1, "%v\n", err)
		}
	case "compgen":
		cfg, err := completionutil.ParseCompgenArgs(args)
		if err != nil {
			return failf(2, "%v\n", err)
		}
		if cfg.HasFunction {
			r.errf("compgen: warning: -F option may not work as you expect\n")
		}
		if cfg.HasCommand {
			r.errf("compgen: warning: -C option may not work as you expect\n")
		}
		lines, status, err := completionutil.GenerateCompgen(newRunnerCompletionBackend(ctx, r, nil), cfg)
		if err != nil {
			if status == 0 {
				status = 2
			}
			return failf(uint8(status), "%v\n", err)
		}
		for _, line := range lines {
			r.outf("%s\n", line)
		}
		exit.code = uint8(status)
	case "break", "continue":
		if !r.inLoop {
			return failf(0, "%s: only meaningful in a `for', `while', or `until' loop\n", name)
		}
		enclosing := &r.breakEnclosing
		if name == "continue" {
			enclosing = &r.contnEnclosing
		}
		switch len(args) {
		case 0:
			*enclosing = 1
		case 1:
			n, err := strconv.Atoi(args[0])
			if err != nil {
				exit = failf(2, "%s: %s: numeric argument required\n", name, args[0])
				exit.exiting = true
				return exit
			}
			*enclosing = n
		default:
			exit = failf(1, "%s: too many arguments\n", name)
			exit.exiting = true
			return exit
		}
	case "pwd":
		return r.pwdBuiltin(ctx, args)
	case "cd":
		exit.code = r.cdBuiltin(ctx, args)
		return exit
	case "wait":
		fp := flagParser{remaining: args}
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-n", "-p":
				return failf(2, "wait: unsupported option %q\n", flag)
			default:
				return failf(2, "wait: invalid option %q\n", flag)
			}
		}
		if len(args) == 0 {
			// Note that "wait" without arguments always returns exit status zero.
			for _, bg := range r.bgProcs {
				<-bg.done
			}
			break
		}
		for _, arg := range args {
			arg, ok := strings.CutPrefix(arg, "g")
			pid := atoi(arg)
			if !ok || pid <= 0 || pid > int64(len(r.bgProcs)) {
				return failf(1, "wait: pid %s is not a child of this shell\n", arg)
			}
			bg := r.bgProcs[pid-1]
			<-bg.done
			exit = *bg.exit
		}
	case "caller":
		depth := 0
		switch len(args) {
		case 0:
		case 1:
			n, err := strconv.Atoi(args[0])
			if err != nil || n < 0 {
				return failf(2, "caller: %s: numeric argument required\n", args[0])
			}
			depth = n
		default:
			return failf(2, "caller: too many arguments\n")
		}
		line, frame, ok := r.callerFrame(depth)
		if !ok {
			exit.code = 1
			return exit
		}
		lineText := strconv.Itoa(line)
		if frame.bashSource != "" && frame.label != "" {
			fmt.Fprintf(r.stdout, "%s %s %s\n", lineText, frame.label, frame.bashSource)
		} else if frame.bashSource != "" {
			fmt.Fprintf(r.stdout, "%s %s\n", lineText, frame.bashSource)
		} else if frame.label != "" {
			fmt.Fprintf(r.stdout, "%s %s\n", lineText, frame.label)
		} else {
			r.out(lineText)
			r.out("\n")
		}
	case "builtin":
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		if len(args) < 1 {
			break
		}
		if !IsBuiltin(args[0]) {
			r.errf("builtin: %s: not a shell builtin\n", args[0])
			exit.code = 1
			return exit
		}
		exit = r.builtin(ctx, pos, args[0], args[1:])
	case "declare", "local", "export", "readonly", "typeset", "nameref":
		r.cmd(ctx, declClauseFromFields(name, args))
		return r.exit
	case "type":
		return r.typeBuiltin(ctx, args)
	case "hash":
		return r.hashBuiltin(ctx, args)
	case "eval":
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		src := strings.Join(args, " ")
		r.evalDepth++
		defer func() {
			r.evalDepth--
		}()
		err := r.runShellReader(ctx, strings.NewReader(src), "", nil)
		var status ExitStatus
		if err != nil && !errors.As(err, &status) {
			return failf(1, "eval: %v\n", err)
		}
		exit = r.exit
	case "source", ".":
		return r.sourceBuiltin(ctx, pos, args)
	case "[":
		if len(args) == 0 || args[len(args)-1] != "]" {
			r.errf("[: missing `]'\n")
			return failf(2, "")
		}
		args = args[:len(args)-1]
		fallthrough
	case "test":
		cmdName := name // "[" or "test"
		parseErr := false
		p := testParser{
			args: args,
			err: func(err error) {
				r.errf("%s: %v\n", cmdName, err)
				parseErr = true
			},
		}
		expr := p.classicTest()
		if parseErr {
			exit.code = 2
			return exit
		}
		if r.bashTest(ctx, expr, true, cmdName) == "" && exit.code == 0 {
			exit.oneIf(true)
		}
		if r.exit.code != 0 {
			exit = r.exit
		}
	case "exec":
		// TODO: Consider unix.Exec, i.e. actually replacing
		// the process. It's in theory what a shell should do,
		// but in practice it would kill the entire Go process
		// and it's not available on Windows.
		if len(args) == 0 {
			r.keepRedirs = true
			break
		}
		r.exit.exiting = true
		r.exec(ctx, pos, args)
		exit = r.exit
	case "command":
		return r.commandBuiltin(ctx, pos, args)
	case "dirs":
		exit.code = r.dirsBuiltin(args)
		return exit
	case "pushd":
		exit.code = r.pushdBuiltin(ctx, args)
		return exit
	case "popd":
		exit.code = r.popdBuiltin(ctx, args)
		return exit
	case "return":
		if !r.inFunc && !r.inSource {
			return failf(2, "return: can only `return' from a function or sourced script\n")
		}
		switch len(args) {
		case 0:
		case 1:
			n, err := strconv.Atoi(args[0])
			if err != nil {
				return failf(2, "invalid return status code: %q\n", args[0])
			}
			exit.code = uint8(n)
		default:
			return failf(2, "return: too many arguments\n")
		}
		exit.returning = true
	case "read":
		opts, names, parseErr := parseReadBuiltinArgs(args)
		if parseErr != nil {
			return failf(parseErr.code, "%s", parseErr.msg)
		}
		if opts.arrayName != "" {
			if !syntax.ValidName(opts.arrayName) {
				return failf(2, "read: %q: invalid identifier\n", opts.arrayName)
			}
		} else {
			for _, name := range names {
				if !syntax.ValidName(name) {
					return failf(2, "read: invalid identifier %q\n", name)
				}
			}
		}

		fd := r.getFD(opts.fd)
		if fd == nil || fd.reader == nil {
			return failf(1, "read: %d: invalid file descriptor: Bad file descriptor\n", opts.fd)
		}
		if opts.prompt != "" && r.readBuiltinCanPrompt(opts.fd, fd) {
			r.errf("%s", opts.prompt)
		}

		chars, err := r.readBuiltinInput(ctx, fd, opts)
		record := readBuiltinCharsString(chars)
		switch {
		case opts.arrayName != "":
			values := []string(nil)
			if opts.exactChars >= 0 {
				values = []string{record}
			} else {
				values = expand.ReadFieldsFromChars(r.ecfg, chars, -1)
			}
			r.setVar(opts.arrayName, expand.Variable{
				Set:  true,
				Kind: expand.Indexed,
				List: values,
			})
		case len(names) == 0:
			r.setVarString(shellReplyVar, record)
		case opts.exactChars >= 0:
			r.setVarString(names[0], record)
			for _, name := range names[1:] {
				r.setVarString(name, "")
			}
		default:
			values := expand.ReadFieldsFromChars(r.ecfg, chars, len(names))
			for i, name := range names {
				val := ""
				if i < len(values) {
					val = values[i]
				}
				r.setVarString(name, val)
			}
		}

		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrDeadlineExceeded) &&
				!errors.Is(err, errReadBuiltinPollUnavailable) {
				r.errf("read: %d: read error: %s\n", opts.fd, readBuiltinErrorText(err))
			}
			if errors.Is(err, os.ErrDeadlineExceeded) {
				exit.code = readBuiltinTimeoutExitCode
			} else {
				exit.code = 1
			}
			return exit
		}

	case "getopts":
		if len(args) < 2 {
			return failf(2, "getopts: usage: getopts optstring name [arg ...]\n")
		}
		state := r.currentGetoptsState()
		optind, _ := strconv.Atoi(r.envGet("OPTIND"))
		if optind < 1 {
			optind = 1
		}
		if optind-1 != state.argidx {
			*state = getopts{argidx: optind - 1}
		}
		optstr := args[0]
		name := args[1]
		args = args[2:]
		if len(args) == 0 {
			args = r.Params
		}
		diagnostics := !strings.HasPrefix(optstr, ":")

		result := state.next(optstr, args)
		opt := result.opt
		switch result.kind {
		case getoptsResultDone, getoptsResultUnknown:
			opt = '?'
		case getoptsResultMissingArg:
			if diagnostics {
				opt = '?'
			} else {
				opt = ':'
			}
		}

		r.delVar("OPTARG")
		if result.kind == getoptsResultOption {
			if result.optarg != "" {
				r.setVarString("OPTARG", result.optarg)
			}
		} else if !diagnostics && !result.done() && result.optarg != "" {
			r.setVarString("OPTARG", result.optarg)
		}
		if optind-1 != state.argidx {
			r.setOPTIND(strconv.FormatInt(int64(state.argidx+1), 10))
		}
		if !syntax.ValidName(name) {
			return failf(2, "getopts: `%s': not a valid identifier\n", name)
		}
		r.setVarString(name, string(opt))
		switch result.kind {
		case getoptsResultUnknown:
			if diagnostics {
				r.errf("illegal option -- %s\n", result.optarg)
			}
		case getoptsResultMissingArg:
			if diagnostics {
				r.errf("option requires an argument -- %s\n", result.optarg)
			}
		}

		exit.oneIf(result.done())

	case "shopt":
		mode := ""
		posixOpts := false
		printReusable := false
		quiet := false
		fp := flagParser{remaining: args}
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-s", "-u":
				mode = flag
			case "-o":
				posixOpts = true
			case "-p":
				printReusable = true
			case "-q":
				quiet = true
			case "--":
				return failf(2, "shopt: --: invalid option\nshopt: usage: shopt [-pqsu] [-o] [optname ...]\n")
			default:
				return failf(2, "shopt: invalid option %q\n", flag)
			}
		}
		args := fp.args()
		if len(args) == 0 {
			if quiet {
				break
			}
			if posixOpts {
				for i, opt := range &posixOptsTable {
					if mode != "" && r.opts[i] != (mode == "-s") {
						continue
					}
					r.printShoptLine(opt.name, r.opts[i], printReusable, true)
				}
			} else {
				for i, opt := range &bashOptsTable {
					enabled := r.opts[len(posixOptsTable)+i]
					if mode != "" && enabled != (mode == "-s") {
						continue
					}
					r.printShoptLine(opt.name, enabled, printReusable, false)
				}
			}
			break
		}
		allEnabled := true
		for _, arg := range args {
			opt := (*bool)(nil)
			if posixOpts {
				opt = r.posixOptByName(arg)
			} else {
				opt, _ = r.bashOptByName(arg)
			}
			if opt == nil {
				return failf(1, "shopt: %s: invalid shell option name\n", arg)
			}

			switch mode {
			case "-s", "-u":
				*opt = mode == "-s"
			default: // ""
				if !*opt {
					allEnabled = false
				}
				if quiet {
					continue
				}
				r.printShoptLine(arg, *opt, printReusable, posixOpts)
			}
		}
		if mode == "" && !allEnabled {
			exit.code = 1
		}
		r.updateExpandOpts()

	case "alias":
		show := func(name string, als alias) {
			r.outf("alias %s='%s'\n", name, als.value)
		}

		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		if len(args) == 0 {
			names := make([]string, 0, len(r.alias))
			for name := range r.alias {
				names = append(names, name)
			}
			slices.Sort(names)
			for _, name := range names {
				show(name, r.alias[name])
			}
		}
		for _, arg := range args {
			name, src, ok := strings.Cut(arg, "=")
			if !ok {
				als, ok := r.alias[name]
				if !ok {
					r.errf("alias: %s: not found\n", name)
					exit.code = 1
					continue
				}
				show(name, als)
				continue
			}

			if r.alias == nil {
				r.alias = make(map[string]alias)
			}
			r.alias[name] = alias{value: src}
		}
	case "unalias":
		removeAll := false
		for len(args) > 0 {
			switch args[0] {
			case "-a":
				removeAll = true
				args = args[1:]
			case "--":
				args = args[1:]
				goto unaliasArgs
			default:
				if strings.HasPrefix(args[0], "-") && args[0] != "-" {
					return failf(2, "unalias: %s: invalid option\nunalias: usage: unalias [-a] name [name ...]\n", args[0])
				}
				goto unaliasArgs
			}
		}
	unaliasArgs:
		if removeAll {
			clear(r.alias)
			break
		}
		if len(args) == 0 {
			return failf(2, "unalias: usage: unalias [-a] name [name ...]\n")
		}
		for _, name := range args {
			if _, ok := r.alias[name]; !ok {
				r.errf("unalias: %s: not found\n", name)
				exit.code = 1
				continue
			}
			delete(r.alias, name)
		}

	case "trap":
		return r.trapBuiltin(ctx, args)

	case "readarray", "mapfile":
		opts, args, parseErr := parseMapfileBuiltinArgs(name, args)
		if parseErr != nil {
			return failf(parseErr.code, "%s", parseErr.msg)
		}
		var arrayName string
		switch len(args) {
		case 0:
			arrayName = "MAPFILE"
		case 1:
			if !syntax.ValidName(args[0]) {
				return failf(2, "%s: invalid identifier %q\n", name, args[0])
			}
			arrayName = args[0]
		default:
			return failf(2, "%s: Only one array name may be specified, %v\n", name, args)
		}

		fd := r.getFD(opts.fd)
		if fd == nil || fd.reader == nil {
			return failf(1, "%s: %d: invalid file descriptor: Bad file descriptor\n", name, opts.fd)
		}

		var target expand.Variable
		initialized := false
		firstRead := true
		nextIndex := opts.origin
		skipped := 0
		stored := 0
		for {
			if opts.maxLines > 0 && stored >= opts.maxLines {
				break
			}
			record, err := mapfileBuiltinReadRecord(fd, opts.delimiter)
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				if firstRead && mapfileBuiltinSuppressReadError(err) {
					break
				}
				return failf(1, "%s: %d: read error: %s\n", name, opts.fd, readBuiltinErrorText(err))
			}
			firstRead = false
			if !initialized {
				target = mapfileBuiltinTargetVar(r.lookupVar(arrayName), opts.hasOrigin)
				initialized = true
			}
			if skipped < opts.skipLines {
				skipped++
				continue
			}
			target = target.IndexedSet(nextIndex, mapfileBuiltinRecordValue(record, opts.delimiter, opts.stripDelimiter), false)
			nextIndex++
			stored++
		}
		if !initialized && !opts.hasOrigin {
			target = mapfileBuiltinTargetVar(r.lookupVar(arrayName), false)
			initialized = true
		}
		if initialized {
			r.setVar(arrayName, target)
		}

	default:
		return failf(2, "%s: unimplemented builtin\n", name)
	}
	return exit
}

const mapfileBuiltinUsage = "mapfile: usage: mapfile [-d delim] [-n count] [-O origin] [-s count] [-t] [-u fd] [array]\n"

type mapfileBuiltinOptions struct {
	stripDelimiter bool
	delimiter      byte
	maxLines       int
	origin         int
	skipLines      int
	fd             int
	hasOrigin      bool
}

type mapfileBuiltinParseError struct {
	code uint8
	msg  string
}

func parseMapfileBuiltinArgs(name string, args []string) (mapfileBuiltinOptions, []string, *mapfileBuiltinParseError) {
	opts := mapfileBuiltinOptions{
		delimiter: '\n',
		fd:        0,
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return opts, args[i+1:], nil
		}
		if len(arg) < 2 || arg[0] != '-' {
			return opts, args[i:], nil
		}
		for j := 1; j < len(arg); j++ {
			switch arg[j] {
			case 't':
				opts.stripDelimiter = true
			case 'd':
				value, next, ok := readBuiltinOptionArg(args, i, arg, j)
				if !ok {
					return opts, nil, &mapfileBuiltinParseError{code: 2, msg: fmt.Sprintf("%s: -d: option requires an argument\n", name)}
				}
				if value == "" {
					opts.delimiter = 0
				} else {
					opts.delimiter = value[0]
				}
				i = next
				j = len(arg)
			case 'n':
				value, next, ok := readBuiltinOptionArg(args, i, arg, j)
				if !ok {
					return opts, nil, &mapfileBuiltinParseError{code: 2, msg: fmt.Sprintf("%s: -n: option requires an argument\n", name)}
				}
				count, err := strconv.Atoi(value)
				if err != nil || count < 0 {
					return opts, nil, &mapfileBuiltinParseError{code: 1, msg: fmt.Sprintf("%s: %s: invalid line count\n", name, value)}
				}
				opts.maxLines = count
				i = next
				j = len(arg)
			case 'O':
				value, next, ok := readBuiltinOptionArg(args, i, arg, j)
				if !ok {
					return opts, nil, &mapfileBuiltinParseError{code: 2, msg: fmt.Sprintf("%s: -O: option requires an argument\n", name)}
				}
				origin, err := strconv.Atoi(value)
				if err != nil || origin < 0 {
					return opts, nil, &mapfileBuiltinParseError{code: 1, msg: fmt.Sprintf("%s: %s: invalid array origin\n", name, value)}
				}
				opts.origin = origin
				opts.hasOrigin = true
				i = next
				j = len(arg)
			case 's':
				value, next, ok := readBuiltinOptionArg(args, i, arg, j)
				if !ok {
					return opts, nil, &mapfileBuiltinParseError{code: 2, msg: fmt.Sprintf("%s: -s: option requires an argument\n", name)}
				}
				count, err := strconv.Atoi(value)
				if err != nil || count < 0 {
					return opts, nil, &mapfileBuiltinParseError{code: 1, msg: fmt.Sprintf("%s: %s: invalid line count\n", name, value)}
				}
				opts.skipLines = count
				i = next
				j = len(arg)
			case 'u':
				value, next, ok := readBuiltinOptionArg(args, i, arg, j)
				if !ok {
					return opts, nil, &mapfileBuiltinParseError{code: 2, msg: fmt.Sprintf("%s: -u: option requires an argument\n", name)}
				}
				fd, err := strconv.Atoi(value)
				if err != nil || fd < 0 {
					return opts, nil, &mapfileBuiltinParseError{code: 1, msg: fmt.Sprintf("%s: %s: invalid file descriptor specification\n", name, value)}
				}
				opts.fd = fd
				i = next
				j = len(arg)
			default:
				return opts, nil, &mapfileBuiltinParseError{
					code: 2,
					msg:  fmt.Sprintf("%s: -%c: invalid option\n%s", name, arg[j], mapfileBuiltinUsage),
				}
			}
		}
	}
	return opts, nil, nil
}

func mapfileBuiltinReadRecord(fd *shellFD, delim byte) ([]byte, error) {
	if fd == nil || fd.reader == nil {
		return nil, errors.New("bad file descriptor")
	}
	var buf []byte
	for {
		b, err := fd.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if len(buf) == 0 {
					return nil, io.EOF
				}
				return buf, nil
			}
			return nil, err
		}
		buf = append(buf, b)
		if b == delim {
			return buf, nil
		}
	}
}

func mapfileBuiltinSuppressReadError(err error) bool {
	return errors.Is(err, syscall.EISDIR) || readBuiltinErrorText(err) == "Is a directory"
}

func mapfileBuiltinRecordValue(record []byte, delim byte, stripDelimiter bool) string {
	if nul := bytes.IndexByte(record, 0); nul >= 0 {
		record = record[:nul]
	}
	if stripDelimiter && len(record) > 0 && record[len(record)-1] == delim {
		record = record[:len(record)-1]
	}
	return string(record)
}

func mapfileBuiltinTargetVar(prev expand.Variable, preserveExisting bool) expand.Variable {
	if preserveExisting {
		switch prev.Kind {
		case expand.Indexed:
			return prev
		case expand.String:
			if prev.IsSet() {
				prev.Kind = expand.Indexed
				prev.List = []string{prev.Str}
				prev.Str = ""
				prev.Map = nil
				prev.Indices = nil
				return prev
			}
		}
	}
	prev.Kind = expand.Indexed
	prev.Set = true
	prev.Str = ""
	prev.List = []string{}
	prev.Map = nil
	prev.Indices = nil
	return prev
}

func (r *Runner) printOptLine(name string, enabled, supported bool) {
	r.outf("%s\t%s\n", name, r.optStatusText(enabled))
}

func (r *Runner) printShoptLine(name string, enabled, reusable, posix bool) {
	if !reusable {
		r.printOptLine(name, enabled, true)
		return
	}
	if posix {
		flag := "+o"
		if enabled {
			flag = "-o"
		}
		r.outf("set %s %s\n", flag, name)
		return
	}
	flag := "-u"
	if enabled {
		flag = "-s"
	}
	r.outf("shopt %s %s\n", flag, name)
}

const readBuiltinUsage = "read: usage: read [-Eers] [-a array] [-d delim] [-i text] [-n nchars] [-N nchars] [-p prompt] [-t timeout] [-u fd] [name ...]\n"

var errReadBuiltinPollUnavailable = errors.New("read poll unavailable")

type readBuiltinOptions struct {
	raw        bool
	silent     bool
	prompt     string
	arrayName  string
	fd         int
	delimiter  byte
	maxChars   int
	exactChars int
	timeout    time.Duration
	timeoutSet bool
}

type readBuiltinParseError struct {
	code uint8
	msg  string
}

func parseReadBuiltinArgs(args []string) (readBuiltinOptions, []string, *readBuiltinParseError) {
	opts := readBuiltinOptions{
		fd:         0,
		delimiter:  '\n',
		maxChars:   -1,
		exactChars: -1,
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return opts, args[i+1:], nil
		}
		if len(arg) < 2 || arg[0] != '-' {
			return opts, args[i:], nil
		}
		for j := 1; j < len(arg); j++ {
			switch arg[j] {
			case 'r':
				opts.raw = true
			case 's':
				opts.silent = true
			case 'a':
				value, next, ok := readBuiltinOptionArg(args, i, arg, j)
				if !ok {
					return opts, nil, &readBuiltinParseError{code: 2, msg: "read: -a: option requires an argument\n"}
				}
				opts.arrayName = value
				i = next
				j = len(arg)
			case 'd':
				value, next, ok := readBuiltinOptionArg(args, i, arg, j)
				if !ok {
					return opts, nil, &readBuiltinParseError{code: 2, msg: "read: -d: option requires an argument\n"}
				}
				if value == "" {
					opts.delimiter = 0
				} else {
					opts.delimiter = value[0]
				}
				i = next
				j = len(arg)
			case 'n':
				value, next, ok := readBuiltinOptionArg(args, i, arg, j)
				if !ok {
					return opts, nil, &readBuiltinParseError{code: 2, msg: "read: -n: option requires an argument\n"}
				}
				num, err := strconv.Atoi(value)
				if err != nil || num < 0 {
					return opts, nil, &readBuiltinParseError{code: 1, msg: fmt.Sprintf("read: %s: invalid number\n", value)}
				}
				opts.maxChars = num
				opts.exactChars = -1
				i = next
				j = len(arg)
			case 'N':
				value, next, ok := readBuiltinOptionArg(args, i, arg, j)
				if !ok {
					return opts, nil, &readBuiltinParseError{code: 2, msg: "read: -N: option requires an argument\n"}
				}
				num, err := strconv.Atoi(value)
				if err != nil || num < 0 {
					return opts, nil, &readBuiltinParseError{code: 1, msg: fmt.Sprintf("read: %s: invalid number\n", value)}
				}
				opts.exactChars = num
				opts.maxChars = -1
				i = next
				j = len(arg)
			case 'p':
				value, next, ok := readBuiltinOptionArg(args, i, arg, j)
				if !ok {
					return opts, nil, &readBuiltinParseError{code: 2, msg: "read: -p: option requires an argument\n"}
				}
				opts.prompt = value
				i = next
				j = len(arg)
			case 't':
				value, next, ok := readBuiltinOptionArg(args, i, arg, j)
				if !ok {
					return opts, nil, &readBuiltinParseError{code: 2, msg: "read: -t: option requires an argument\n"}
				}
				timeout, err := strconv.ParseFloat(value, 64)
				if err != nil || timeout < 0 {
					return opts, nil, &readBuiltinParseError{code: 1, msg: fmt.Sprintf("read: %s: invalid timeout specification\n", value)}
				}
				opts.timeoutSet = true
				opts.timeout = time.Duration(timeout * float64(time.Second))
				i = next
				j = len(arg)
			case 'u':
				value, next, ok := readBuiltinOptionArg(args, i, arg, j)
				if !ok {
					return opts, nil, &readBuiltinParseError{code: 2, msg: "read: -u: option requires an argument\n"}
				}
				fd, err := strconv.Atoi(value)
				if err != nil || fd < 0 {
					return opts, nil, &readBuiltinParseError{code: 1, msg: fmt.Sprintf("read: %s: invalid file descriptor specification\n", value)}
				}
				opts.fd = fd
				i = next
				j = len(arg)
			default:
				return opts, nil, &readBuiltinParseError{
					code: 2,
					msg:  fmt.Sprintf("read: -%c: invalid option\n%s", arg[j], readBuiltinUsage),
				}
			}
		}
	}
	return opts, nil, nil
}

func readBuiltinOptionArg(args []string, i int, arg string, j int) (string, int, bool) {
	if j+1 < len(arg) {
		return arg[j+1:], i, true
	}
	if i+1 >= len(args) {
		return "", i, false
	}
	return args[i+1], i + 1, true
}

func readBuiltinCharsString(chars []expand.ReadFieldChar) string {
	if len(chars) == 0 {
		return ""
	}
	buf := make([]byte, len(chars))
	for i, ch := range chars {
		buf[i] = ch.Value
	}
	return string(buf)
}

func readBuiltinErrorText(err error) string {
	if err == nil {
		return ""
	}
	if strings.Contains(strings.ToLower(err.Error()), "is a directory") {
		return "Is a directory"
	}
	return err.Error()
}

func (r *Runner) readBuiltinCanPrompt(fdNum int, fd *shellFD) bool {
	if fdNum != 0 || !r.interactive || fd == nil {
		return false
	}
	file, ok := fd.reader.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func readBuiltinDeadlineGuard(ctx context.Context, fd *shellFD, deadline time.Time) func() {
	if fd == nil {
		return func() {}
	}
	if !deadline.IsZero() {
		_ = fd.SetReadDeadline(deadline)
	}
	stopc := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = fd.SetReadDeadline(time.Now())
		close(stopc)
	})
	return func() {
		if !stop() {
			<-stopc
		}
		_ = fd.SetReadDeadline(time.Time{})
	}
}

func (r *Runner) readBuiltinPoll(ctx context.Context, fd *shellFD) error {
	cleanup := readBuiltinDeadlineGuard(ctx, fd, time.Now())
	defer cleanup()

	_, err := fd.PeekByte()
	if err == nil || errors.Is(err, io.EOF) {
		return nil
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return errReadBuiltinPollUnavailable
	}
	return err
}

func (r *Runner) readBuiltinInput(ctx context.Context, fd *shellFD, opts readBuiltinOptions) ([]expand.ReadFieldChar, error) {
	if fd == nil || fd.reader == nil {
		return nil, errors.New("bad file descriptor")
	}
	if opts.maxChars == 0 || opts.exactChars == 0 {
		return nil, nil
	}
	if opts.timeoutSet && opts.timeout == 0 {
		return nil, r.readBuiltinPoll(ctx, fd)
	}

	deadline := time.Time{}
	if opts.timeoutSet {
		deadline = time.Now().Add(opts.timeout)
	}
	cleanup := readBuiltinDeadlineGuard(ctx, fd, deadline)
	defer cleanup()

	chars := make([]expand.ReadFieldChar, 0, 64)
	pendingEscape := false
	for {
		if opts.exactChars >= 0 && len(chars) >= opts.exactChars {
			return chars, nil
		}
		if opts.maxChars >= 0 && len(chars) >= opts.maxChars {
			return chars, nil
		}

		b, err := fd.ReadByte()
		if err != nil {
			return chars, err
		}
		if !opts.raw && pendingEscape {
			pendingEscape = false
			if b == '\n' {
				continue
			}
			chars = append(chars, expand.ReadFieldChar{Value: b, Escaped: true})
			continue
		}
		if !opts.raw && b == '\\' {
			pendingEscape = true
			continue
		}
		if opts.exactChars < 0 && b == opts.delimiter {
			return chars, nil
		}
		if b == 0 {
			continue
		}
		chars = append(chars, expand.ReadFieldChar{Value: b})
	}
}

func (r *Runner) readLine(ctx context.Context, raw bool) ([]byte, error) {
	if r.stdin == nil {
		return nil, errors.New("interp: can't read, there's no stdin")
	}

	var line []byte
	esc := false

	stopc := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		r.stdin.SetReadDeadline(time.Now())
		close(stopc)
	})
	defer func() {
		if !stop() {
			// The AfterFunc was started.
			// Wait for it to complete, and reset the file's deadline.
			<-stopc
			r.stdin.SetReadDeadline(time.Time{})
		}
	}()
	for {
		var buf [1]byte
		n, err := r.stdin.Read(buf[:])
		if n > 0 {
			b := buf[0]
			switch {
			case !raw && b == '\\':
				line = append(line, b)
				esc = !esc
			case !raw && b == '\n' && esc:
				// line continuation
				line = line[len(line)-1:]
				esc = false
			case b == '\n':
				return line, nil
			default:
				line = append(line, b)
				esc = false
			}
		}
		if err != nil {
			return line, err
		}
	}
}

func (r *Runner) pwdBuiltin(ctx context.Context, args []string) (exit exitStatus) {
	logical := true
	physical := false
	for len(args) > 0 {
		switch args[0] {
		case "-L":
			logical = true
			physical = false
		case "-P":
			physical = true
			logical = false
		default:
			if args[0] == "--" {
				args = nil
				continue
			}
			if strings.HasPrefix(args[0], "-") && args[0] != "-" {
				r.errf("pwd: usage: pwd [-LP]\n")
				exit.code = 2
				return exit
			}
			args = nil
			continue
		}
		args = args[1:]
	}
	pwd, err := r.resolvePwd(ctx, logical, physical)
	if err != nil {
		exit.fatal(err)
		return exit
	}
	r.outf("%s\n", pwd)
	return exit
}

func (r *Runner) resolvePwd(ctx context.Context, logical, physical bool) (string, error) {
	if physical {
		return r.pwdPhysicalPath(ctx)
	}
	if logical {
		if candidate, ok := r.pwdLogicalPath(); ok {
			return candidate, nil
		}
	}
	return r.pwdPhysicalPath(ctx)
}

func (r *Runner) pwdLogicalPath() (string, bool) {
	if r.logicalDir != "" {
		return r.logicalDir, true
	}
	if candidate := r.envGet("PWD"); r.pwdLooksReasonable(candidate) {
		return candidate, true
	}
	return "", false
}

func (r *Runner) pwdPhysicalPath(ctx context.Context) (string, error) {
	return r.realpath(ctx, r.Dir)
}

func (r *Runner) pwdLooksReasonable(candidate string) bool {
	if !path.IsAbs(candidate) {
		return false
	}
	for piece := range strings.SplitSeq(candidate, "/") {
		if piece == "." || piece == ".." {
			return false
		}
	}
	return r.pwdCandidateMatchesDir(candidate)
}

func (r *Runner) pwdCandidateMatchesDir(candidate string) bool {
	if candidate == "" {
		return false
	}
	if path.Clean(candidate) == r.Dir {
		return true
	}
	candidateReal, err1 := r.realpath(context.Background(), candidate)
	currentReal, err2 := r.realpath(context.Background(), r.Dir)
	return err1 == nil && err2 == nil && candidateReal == currentReal
}

func (r *Runner) cdBuiltin(ctx context.Context, args []string) uint8 {
	logical := true
	show := false
	operands := make([]string, 0, 1)
	parsingOptions := true
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		switch {
		case parsingOptions && arg == "--":
			parsingOptions = false
		case parsingOptions && strings.HasPrefix(arg, "-") && arg != "-":
			for _, opt := range arg[1:] {
				switch opt {
				case 'L':
					logical = true
				case 'P':
					logical = false
				default:
					r.errf("cd: usage: cd [-L|-P] [dir]\n")
					return 2
				}
			}
		default:
			parsingOptions = false
			operands = append(operands, arg)
		}
	}

	target := ""
	switch len(operands) {
	case 0:
		target = r.envGet("HOME")
		if target == "" {
			r.errf("cd: HOME not set\n")
			return 1
		}
	case 1:
		target = operands[0]
	default:
		r.errf("cd: too many arguments\n")
		return 2
	}
	if target == "-" {
		target = r.envGet("OLDPWD")
		if target == "" {
			r.errf("cd: OLDPWD not set\n")
			return 1
		}
		show = true
	}
	oldVisible := r.visibleDir()
	next, code := r.resolveDir(ctx, "cd", target, dirResolveOptions{
		logical:   logical,
		useCDPath: true,
	})
	if code != 0 {
		return code
	}
	r.setCurrentDir(next.physical, next.logical, oldVisible)
	if show || next.show {
		r.outf("%s\n", next.logical)
	}
	return 0
}

type dirResolveOptions struct {
	logical   bool
	useCDPath bool
}

type dirResolveResult struct {
	physical string
	logical  string
	show     bool
}

type dirResolveCandidate struct {
	operand string
	print   bool
}

type dirResolveFailure struct {
	path    string
	message string
}

func (r *Runner) visibleDir() string {
	if r.logicalDir != "" {
		return r.logicalDir
	}
	return r.Dir
}

func (r *Runner) resolveDir(ctx context.Context, cmd, name string, opts dirResolveOptions) (dirResolveResult, uint8) {
	name = cmp.Or(name, ".")
	for _, candidate := range r.dirResolveCandidates(name, opts.useCDPath) {
		result, failure := r.resolveDirCandidate(ctx, candidate.operand, opts.logical)
		if failure == nil {
			result.show = candidate.print
			return result, 0
		}
		if failure.message == "No such file or directory" {
			continue
		}
		r.errf("%s: %s: %s\n", cmd, failure.path, failure.message)
		return dirResolveResult{}, 1
	}
	r.errf("%s: %s: No such file or directory\n", cmd, name)
	return dirResolveResult{}, 1
}

func (r *Runner) dirResolveCandidates(name string, useCDPath bool) []dirResolveCandidate {
	candidates := make([]dirResolveCandidate, 0, 2)
	if useCDPath && shouldUseCDPath(name) {
		for entry := range strings.SplitSeq(r.envGet("CDPATH"), ":") {
			operand := name
			if entry != "" {
				operand = path.Join(entry, name)
			}
			candidates = append(candidates, dirResolveCandidate{
				operand: operand,
				print:   entry != "",
			})
		}
	}
	candidates = append(candidates, dirResolveCandidate{operand: name})
	return candidates
}

func shouldUseCDPath(name string) bool {
	return name != "" && !path.IsAbs(name) && !strings.ContainsRune(name, '/') && name[0] != '.'
}

func (r *Runner) resolveDirCandidate(ctx context.Context, operand string, logical bool) (dirResolveResult, *dirResolveFailure) {
	if logical {
		return r.resolveLogicalDirCandidate(ctx, operand)
	}
	return r.resolvePhysicalDirCandidate(ctx, operand)
}

func (r *Runner) resolveLogicalDirCandidate(ctx context.Context, operand string) (dirResolveResult, *dirResolveFailure) {
	current := r.visibleDir()
	if path.IsAbs(operand) {
		current = "/"
	}
	for _, part := range strings.Split(operand, "/") {
		switch part {
		case "", ".":
			continue
		default:
			next := path.Clean(path.Join(current, part))
			info, err := r.statHandler(ctx, next, true)
			if err != nil {
				return dirResolveResult{}, dirResolveFailureFromError(next, err)
			}
			if !info.IsDir() {
				return dirResolveResult{}, &dirResolveFailure{path: next, message: "Not a directory"}
			}
			current = next
		}
	}
	if err := r.access(ctx, current, access_X_OK); err != nil {
		return dirResolveResult{}, &dirResolveFailure{path: current, message: "Permission denied"}
	}
	physical, err := r.realpath(ctx, current)
	if err != nil {
		return dirResolveResult{}, dirResolveFailureFromError(current, err)
	}
	return dirResolveResult{physical: physical, logical: current}, nil
}

func (r *Runner) resolvePhysicalDirCandidate(ctx context.Context, operand string) (dirResolveResult, *dirResolveFailure) {
	current := r.Dir
	if path.IsAbs(operand) {
		current = "/"
	}
	for _, part := range strings.Split(operand, "/") {
		switch part {
		case "", ".":
			continue
		default:
			next := path.Clean(path.Join(current, part))
			info, err := r.statHandler(ctx, next, true)
			if err != nil {
				return dirResolveResult{}, dirResolveFailureFromError(next, err)
			}
			if !info.IsDir() {
				return dirResolveResult{}, &dirResolveFailure{path: next, message: "Not a directory"}
			}
			resolved, err := r.realpath(ctx, next)
			if err != nil {
				return dirResolveResult{}, dirResolveFailureFromError(next, err)
			}
			current = resolved
		}
	}
	if err := r.access(ctx, current, access_X_OK); err != nil {
		return dirResolveResult{}, &dirResolveFailure{path: current, message: "Permission denied"}
	}
	return dirResolveResult{physical: current, logical: current}, nil
}

func dirResolveFailureFromError(path string, err error) *dirResolveFailure {
	if errors.Is(err, os.ErrPermission) {
		return &dirResolveFailure{path: path, message: "Permission denied"}
	}
	return &dirResolveFailure{path: path, message: "No such file or directory"}
}

func (r *Runner) typeBuiltin(ctx context.Context, args []string) (exit exitStatus) {
	anyNotFound := false
	mode := shellTypeMode{}
	fp := flagParser{remaining: args}
	for fp.more() {
		switch flag := fp.flag(); flag {
		case "-a":
			mode.all = true
		case "-f":
			mode.suppressFuncs = true
		case "-p":
			mode.output = shellTypeOutputPath
		case "-P":
			mode.output = shellTypeOutputForcePath
		case "-t":
			mode.output = shellTypeOutputKind
		case "--help":
			r.errf("command: NOT IMPLEMENTED\n")
			exit.code = 3
			return exit
		default:
			r.errf("command: invalid option %q\n", flag)
			exit.code = 2
			return exit
		}
	}
	for _, arg := range fp.args() {
		matches, found := r.typeMatches(ctx, arg, mode)
		if !found {
			if mode.output == shellTypeOutputVerbose {
				r.errf("type: %s: not found\n", arg)
			}
			anyNotFound = true
			continue
		}
		for _, match := range matches {
			r.printTypeMatch(arg, match, mode)
		}
		if len(matches) == 0 && mode.output == shellTypeOutputKind {
			r.errf("type: %s: not found\n", arg)
			anyNotFound = true
		}
	}
	if anyNotFound {
		exit.code = 1
	}
	return exit
}

func (r *Runner) sourceBuiltin(ctx context.Context, pos syntax.Pos, args []string) (exit exitStatus) {
	if len(args) < 1 {
		r.errf("%v: source: need filename\n", pos)
		exit.code = 2
		return exit
	}
	sourceName := args[0]
	sourcePath := args[0]
	sourcepathOpt, _ := r.bashOptByName("sourcepath")
	if !strings.ContainsRune(args[0], '/') && sourcepathOpt != nil && *sourcepathOpt {
		if resolved, err := r.lookPath(ctx, r.Dir, r.writeEnv, args[0], false, false); err == nil {
			sourcePath = resolved
			sourceName = resolved
		}
	}
	f, err := r.open(ctx, sourcePath, os.O_RDONLY, 0, false)
	if err != nil {
		r.errf("%v\n", err)
		exit.code = 1
		return exit
	}
	defer f.Close()
	oldParams := r.Params
	oldSourceSetParams := r.sourceSetParams
	oldInSource := r.inSource

	sourceArgs := len(args[1:]) > 0
	if sourceArgs {
		r.Params = args[1:]
		r.sourceSetParams = false
	}
	r.sourceSetParams = false
	internal := r.currentInternal()
	bashSource := sourceName
	if internal {
		bashSource = ""
	}
	frame := &execFrame{
		kind:        frameKindSource,
		label:       "source",
		execFile:    sourceName,
		bashSource:  bashSource,
		callLine:    r.sourceCallLine(pos),
		internal:    internal,
		allowErr:    r.opts[optErrTrace],
		allowDebug:  r.opts[optFuncTrace],
		allowReturn: r.opts[optFuncTrace],
	}
	r.inSource = true
	runErr := r.runShellReader(ctx, f, sourceName, frame)
	if r.opts[optFuncTrace] && !r.exit.fatalExit {
		r.maybeRunReturnTrap(ctx, pos.Line(), r.exit.code)
	}

	if sourceArgs && !r.sourceSetParams {
		r.Params = oldParams
	}
	r.sourceSetParams = oldSourceSetParams
	r.inSource = oldInSource

	var status ExitStatus
	if runErr != nil && !errors.As(runErr, &status) {
		r.errf("source: %v\n", runErr)
		exit.code = 1
		return exit
	}
	exit = r.exit
	exit.returning = false
	return exit
}

func (r *Runner) commandBuiltin(ctx context.Context, pos syntax.Pos, args []string) (exit exitStatus) {
	showKind := false
	showVerbose := false
	useDefaultPath := false
	forcePath := false
	fp := flagParser{remaining: args}
	for fp.more() {
		switch flag := fp.flag(); flag {
		case "-v":
			showKind = true
			showVerbose = false
		case "-V":
			showKind = false
			showVerbose = true
		case "-p":
			useDefaultPath = true
		case "-P":
			forcePath = true
		default:
			r.errf("command: invalid option %q\n", flag)
			exit.code = 2
			return exit
		}
	}
	args = fp.args()
	if len(args) == 0 {
		return exit
	}
	if showKind || showVerbose {
		mode := shellTypeMode{}
		if forcePath {
			mode.output = shellTypeOutputForcePath
		}
		restorePath := func() {}
		if useDefaultPath {
			restorePath = r.setTemporaryPath(defaultExecPath)
			defer restorePath()
		}
		last := uint8(0)
		for _, arg := range args {
			last = 0
			matches, found := r.typeMatches(ctx, arg, mode)
			if !found || len(matches) == 0 {
				if showVerbose {
					r.errf("command: %s: not found\n", arg)
				}
				last = 1
				continue
			}
			for _, match := range matches {
				if showVerbose {
					r.printTypeMatch(arg, match, shellTypeMode{})
					continue
				}
				switch match.kind {
				case shellTypeFile:
					r.outf("%s\n", match.path)
				default:
					r.outf("%s\n", arg)
				}
			}
		}
		exit.code = last
		return exit
	}

	restorePath := func() {}
	if useDefaultPath {
		restorePath = r.setTemporaryPath(defaultExecPath)
		defer restorePath()
	}
	if !forcePath && !useDefaultPath && IsBuiltin(args[0]) {
		return r.builtin(ctx, pos, args[0], args[1:])
	}
	r.exec(ctx, pos, args)
	return r.exit
}

func (r *Runner) setTemporaryPath(pathValue string) func() {
	prev := r.writeEnv.Get("PATH")
	r.setVar("PATH", expand.Variable{Set: true, Kind: expand.String, Str: pathValue})
	return func() {
		_ = r.writeEnv.Set("PATH", prev)
		r.afterSetVar("PATH", prev)
	}
}

func (r *Runner) hashBuiltin(ctx context.Context, args []string) (exit exitStatus) {
	if len(args) > 0 && args[0] == "-r" {
		r.commandHashClear()
		args = args[1:]
		if len(args) == 0 {
			return exit
		}
	}
	if len(args) == 0 {
		entries := r.commandHashEntries()
		if len(entries) == 0 {
			r.out("hash: hash table empty\n")
			return exit
		}
		slices.SortFunc(entries, func(a, b commandHashEntry) int {
			if diff := cmp.Compare(a.path, b.path); diff != 0 {
				return diff
			}
			return cmp.Compare(a.hits, b.hits)
		})
		r.out("hits\tcommand\n")
		for _, entry := range entries {
			r.outf("%4d\t%s\n", entry.hits, entry.path)
		}
		return exit
	}

	for _, name := range args {
		path, err := r.lookPathForHash(ctx, r.Dir, r.writeEnv, name)
		if err != nil {
			r.errf("hash: %s: not found\n", name)
			exit.code = 1
			continue
		}
		r.commandHashRemember(name, path)
	}
	return exit
}

func (r *Runner) dirsBuiltin(args []string) uint8 {
	clearStack := false
	long := false
	printMode := false
	verbose := false
	indexArg := ""

	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		switch {
		case arg == "--":
			if len(args) > 0 {
				r.dirsUsage()
				return 2
			}
			args = nil
		case isDirStackIndexToken(arg):
			if indexArg != "" {
				r.dirsUsage()
				return 2
			}
			indexArg = arg
		case strings.HasPrefix(arg, "+"):
			r.errf("dirs: %s: invalid number\n", arg)
			r.dirsUsage()
			return 2
		case strings.HasPrefix(arg, "-") && arg != "-":
			for _, opt := range arg[1:] {
				switch opt {
				case 'c':
					clearStack = true
				case 'l':
					long = true
				case 'p':
					printMode = true
				case 'v':
					verbose = true
				default:
					r.errf("dirs: %s: invalid number\n", arg)
					r.dirsUsage()
					return 2
				}
			}
		case arg == "-":
			r.errf("dirs: %s: invalid number\n", arg)
			r.dirsUsage()
			return 2
		default:
			r.dirsUsage()
			return 2
		}
	}

	if clearStack {
		r.dirStack = append(r.dirStack[:0], r.visibleDir())
		return 0
	}

	mode := "line"
	if printMode {
		mode = "print"
	}
	if verbose {
		mode = "verbose"
	}

	if indexArg != "" {
		idx, label, err := r.dirStackIndex(indexArg)
		if err != nil {
			r.errf("dirs: %s: invalid number\n", indexArg)
			r.dirsUsage()
			return 2
		}
		if len(r.dirStack) <= 1 && idx != 0 {
			r.errf("dirs: directory stack empty\n")
			return 1
		}
		if idx < 0 || idx >= len(r.dirStack) {
			r.errf("dirs: %s: directory stack index out of range\n", label)
			return 1
		}
		r.printDirStack([]int{idx}, mode, long)
		return 0
	}

	indices := make([]int, len(r.dirStack))
	for i := range indices {
		indices[i] = i
	}
	r.printDirStack(indices, mode, long)
	return 0
}

func (r *Runner) pushdBuiltin(ctx context.Context, args []string) uint8 {
	noChdir := false
	operand := ""
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		switch {
		case arg == "--":
			break
		case arg == "-n":
			noChdir = true
			continue
		case isDirStackIndexToken(arg):
			if operand != "" {
				r.pushdUsage()
				return 2
			}
			operand = arg
			continue
		case strings.HasPrefix(arg, "+"), strings.HasPrefix(arg, "-"):
			r.errf("pushd: %s: invalid number\n", arg)
			r.pushdUsage()
			return 2
		default:
			if operand != "" {
				r.pushdUsage()
				return 2
			}
			operand = arg
			continue
		}
		if len(args) > 1 {
			r.pushdUsage()
			return 2
		}
		if len(args) == 1 {
			if operand != "" {
				r.pushdUsage()
				return 2
			}
			operand = args[0]
		}
		args = nil
	}

	oldCurrent := r.visibleDir()
	if operand == "" {
		if noChdir {
			return 0
		}
		if len(r.dirStack) <= 1 {
			r.errf("pushd: no other directory\n")
			return 1
		}
		operand = "+1"
	}

	if isDirStackIndexToken(operand) {
		idx, _, err := r.dirStackIndex(operand)
		if err != nil {
			r.errf("pushd: %s: invalid number\n", operand)
			r.pushdUsage()
			return 2
		}
		if idx == 0 {
			return r.dirsBuiltin(nil)
		}
		if len(r.dirStack) <= 1 {
			r.errf("pushd: directory stack empty\n")
			return 1
		}
		if idx < 0 || idx >= len(r.dirStack) {
			r.errf("pushd: %s: directory stack index out of range\n", operand)
			return 1
		}
		newStack := rotateDirStack(r.dirStack, idx)
		if noChdir {
			newStack[0] = oldCurrent
			r.dirStack = newStack
			return 0
		}
		next, code := r.resolveDir(ctx, "pushd", newStack[0], dirResolveOptions{logical: true})
		if code != 0 {
			return code
		}
		r.dirStack = newStack
		r.setCurrentDir(next.physical, next.logical, oldCurrent)
		return r.dirsBuiltin(nil)
	}

	if noChdir {
		newStack := make([]string, 0, len(r.dirStack)+1)
		newStack = append(newStack, r.dirStack[0], operand)
		newStack = append(newStack, r.dirStack[1:]...)
		r.dirStack = newStack
		return r.dirsBuiltin(nil)
	}

	next, code := r.resolveDir(ctx, "pushd", operand, dirResolveOptions{logical: true})
	if code != 0 {
		return code
	}
	newStack := make([]string, 0, len(r.dirStack)+1)
	newStack = append(newStack, next.logical)
	newStack = append(newStack, r.dirStack...)
	r.dirStack = newStack
	r.setCurrentDir(next.physical, next.logical, oldCurrent)
	return r.dirsBuiltin(nil)
}

func (r *Runner) popdBuiltin(ctx context.Context, args []string) uint8 {
	noChdir := false
	operand := "+0"
	explicitOperand := false
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		switch {
		case arg == "--":
			if len(args) > 0 {
				r.popdUsage()
				return 2
			}
			args = nil
		case arg == "-n":
			noChdir = true
		case isDirStackIndexToken(arg):
			if explicitOperand {
				r.popdUsage()
				return 2
			}
			operand = arg
			explicitOperand = true
		case strings.HasPrefix(arg, "+"), strings.HasPrefix(arg, "-"):
			r.errf("popd: %s: invalid number\n", arg)
			r.popdUsage()
			return 2
		default:
			r.popdUsage()
			return 2
		}
	}
	if len(r.dirStack) <= 1 {
		r.errf("popd: directory stack empty\n")
		return 1
	}
	idx, _, err := r.dirStackIndex(operand)
	if err != nil {
		r.errf("popd: %s: invalid number\n", operand)
		r.popdUsage()
		return 2
	}
	if idx < 0 || idx >= len(r.dirStack) {
		r.errf("popd: %s: directory stack index out of range\n", operand)
		return 1
	}

	oldCurrent := r.visibleDir()
	newStack := removeDirStackIndex(r.dirStack, idx)
	if noChdir {
		newStack[0] = oldCurrent
		r.dirStack = newStack
	} else if idx == 0 {
		next, code := r.resolveDir(ctx, "popd", newStack[0], dirResolveOptions{logical: true})
		if code != 0 {
			return code
		}
		r.dirStack = newStack
		r.setCurrentDir(next.physical, next.logical, oldCurrent)
	} else {
		r.dirStack = newStack
	}
	return r.dirsBuiltin(nil)
}

func (r *Runner) changeDir(ctx context.Context, cmd, name string) uint8 {
	oldVisible := r.visibleDir()
	next, code := r.resolveDir(ctx, cmd, name, dirResolveOptions{logical: true})
	if code != 0 {
		return code
	}
	r.setCurrentDir(next.physical, next.logical, oldVisible)
	return 0
}

func (r *Runner) setCurrentDir(newDir, newLogicalDir, oldLogicalDir string) {
	r.Dir = newDir
	r.logicalDir = newLogicalDir
	r.setExportedVarString("OLDPWD", oldLogicalDir)
	r.setExportedVarString("PWD", newLogicalDir)
	if len(r.dirStack) == 0 {
		r.dirStack = append(r.dirStack, newLogicalDir)
	} else {
		r.dirStack[0] = newLogicalDir
	}
}

func (r *Runner) realpath(ctx context.Context, name string) (string, error) {
	return r.realpathHandler(r.handlerCtx(ctx, handlerKindRealpath, todoPos), absPath(r.Dir, name))
}

func (r *Runner) printDirStack(indices []int, mode string, long bool) {
	switch mode {
	case "verbose":
		for _, idx := range indices {
			r.outf("%2d  %s\n", idx, r.displayDir(r.dirStack[idx], long))
		}
	case "print":
		for _, idx := range indices {
			r.outf("%s\n", r.displayDir(r.dirStack[idx], long))
		}
	default:
		for i, idx := range indices {
			if i > 0 {
				r.out(" ")
			}
			r.out(r.displayDir(r.dirStack[idx], long))
		}
		r.out("\n")
	}
}

func (r *Runner) displayDir(dir string, long bool) string {
	if long {
		return dir
	}
	home := r.envGet("HOME")
	switch {
	case home == "":
		return dir
	case home == "/" && dir == "/":
		return "/"
	case dir == home:
		return "~"
	case strings.HasPrefix(dir, home+"/"):
		return "~" + strings.TrimPrefix(dir, home)
	default:
		return dir
	}
}

func (r *Runner) dirsUsage()  { r.errf("dirs: usage: dirs [-clpv] [+N] [-N]\n") }
func (r *Runner) pushdUsage() { r.errf("pushd: usage: pushd [-n] [+N | -N | dir]\n") }
func (r *Runner) popdUsage()  { r.errf("popd: usage: popd [-n] [+N | -N]\n") }

func isDirStackIndexToken(arg string) bool {
	if len(arg) < 2 {
		return false
	}
	if arg[0] != '+' && arg[0] != '-' {
		return false
	}
	for _, r := range arg[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (r *Runner) dirStackIndex(arg string) (index int, label string, err error) {
	n, err := strconv.Atoi(arg[1:])
	if err != nil {
		return 0, "", err
	}
	label = arg[1:]
	if arg[0] == '+' {
		return n, label, nil
	}
	return len(r.dirStack) - 1 - n, label, nil
}

func rotateDirStack(stack []string, index int) []string {
	rotated := make([]string, 0, len(stack))
	rotated = append(rotated, stack[index:]...)
	rotated = append(rotated, stack[:index]...)
	return rotated
}

func removeDirStackIndex(stack []string, index int) []string {
	out := make([]string, 0, len(stack)-1)
	out = append(out, stack[:index]...)
	out = append(out, stack[index+1:]...)
	return out
}

func absPath(dir, name string) string {
	if name == "" {
		return ""
	}
	if !path.IsAbs(name) {
		name = path.Join(dir, name)
	}
	return path.Clean(name)
}

func (r *Runner) absPath(name string) string {
	return absPath(r.Dir, name)
}

type shellTypeMode struct {
	all           bool
	suppressFuncs bool
	output        shellTypeOutputMode
}

type shellTypeMatchKind uint8

const (
	shellTypeAlias shellTypeMatchKind = iota + 1
	shellTypeKeyword
	shellTypeFunction
	shellTypeBuiltin
	shellTypeFile
)

type shellTypeOutputMode uint8

const (
	shellTypeOutputVerbose shellTypeOutputMode = iota
	shellTypeOutputPath
	shellTypeOutputForcePath
	shellTypeOutputKind
)

type shellTypeMatch struct {
	kind shellTypeMatchKind
	path string
	als  alias
	body *syntax.Stmt
}

func (r *Runner) typeMatches(ctx context.Context, name string, mode shellTypeMode) ([]shellTypeMatch, bool) {
	files := r.typeFileMatches(ctx, name, mode.all)
	if mode.output == shellTypeOutputForcePath {
		return files, len(files) > 0
	}

	var matches []shellTypeMatch
	foundNonFile := false
	appendMatch := func(match shellTypeMatch) bool {
		matches = append(matches, match)
		foundNonFile = true
		return mode.all
	}

	if als, ok := r.alias[name]; ok && r.opts[optExpandAliases] {
		if !appendMatch(shellTypeMatch{kind: shellTypeAlias, als: als}) {
			if mode.output == shellTypeOutputPath {
				return nil, true
			}
			return matches, true
		}
	}
	if syntax.IsKeyword(name) {
		if !appendMatch(shellTypeMatch{kind: shellTypeKeyword}) {
			if mode.output == shellTypeOutputPath {
				return nil, true
			}
			return matches, true
		}
	}
	if !mode.suppressFuncs {
		if body := r.funcBody(name); body != nil && !r.funcInternal(name) {
			if !appendMatch(shellTypeMatch{kind: shellTypeFunction, body: body}) {
				if mode.output == shellTypeOutputPath {
					return nil, true
				}
				return matches, true
			}
		}
	}
	if IsBuiltin(name) {
		if !appendMatch(shellTypeMatch{kind: shellTypeBuiltin}) {
			if mode.output == shellTypeOutputPath {
				return nil, true
			}
			return matches, true
		}
	}

	if mode.output == shellTypeOutputPath {
		if len(files) > 0 {
			return files, true
		}
		return nil, foundNonFile
	}
	if len(files) > 0 {
		matches = append(matches, files...)
	}
	return matches, foundNonFile || len(files) > 0
}

func (r *Runner) typeFileMatches(ctx context.Context, name string, all bool) []shellTypeMatch {
	pathList := filepath.SplitList(r.writeEnv.Get("PATH").String())
	if len(pathList) == 0 {
		pathList = []string{""}
	}
	chars := `/`
	if runtime.GOOS == "windows" {
		chars = `:\/`
	}
	exts := pathExts(r.writeEnv)
	if strings.ContainsAny(name, chars) {
		if path, err := r.typeExecutablePath(ctx, name, exts); err == nil {
			return []shellTypeMatch{{kind: shellTypeFile, path: path}}
		}
		return nil
	}

	matches := make([]shellTypeMatch, 0, 1)
	for _, elem := range pathList {
		path := "." + string(filepath.Separator) + name
		if elem != "" && elem != "." {
			path = filepath.Join(elem, name)
		}
		if found, err := r.typeExecutablePath(ctx, path, exts); err == nil {
			matches = append(matches, shellTypeMatch{kind: shellTypeFile, path: found})
			if !all {
				break
			}
		}
	}
	return matches
}

func (r *Runner) typeExecutablePath(ctx context.Context, name string, exts []string) (string, error) {
	if len(exts) == 0 {
		return r.typeStatExecutable(ctx, name)
	}
	if winHasExt(name) {
		if path, err := r.typeStatExecutable(ctx, name); err == nil {
			return path, nil
		}
	}
	for _, ext := range exts {
		if path, err := r.typeStatExecutable(ctx, name+ext); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("not found")
}

func (r *Runner) typeStatExecutable(ctx context.Context, name string) (string, error) {
	info, err := r.stat(ctx, name)
	if err != nil {
		return "", err
	}
	mode := info.Mode()
	if mode.IsDir() {
		return "", fmt.Errorf("is a directory")
	}
	if runtime.GOOS != "windows" && mode&0o111 == 0 {
		return "", fmt.Errorf("permission denied")
	}
	return name, nil
}

func (r *Runner) printTypeMatch(name string, match shellTypeMatch, mode shellTypeMode) {
	if mode.output == shellTypeOutputKind {
		switch match.kind {
		case shellTypeAlias:
			r.out("alias\n")
		case shellTypeKeyword:
			r.out("keyword\n")
		case shellTypeFunction:
			r.out("function\n")
		case shellTypeBuiltin:
			r.out("builtin\n")
		case shellTypeFile:
			r.out("file\n")
		}
		return
	}
	if mode.output == shellTypeOutputPath || mode.output == shellTypeOutputForcePath {
		if match.kind == shellTypeFile {
			r.outf("%s\n", match.path)
		}
		return
	}

	switch match.kind {
	case shellTypeAlias:
		r.outf("%s is aliased to `%s'\n", name, match.als.value)
	case shellTypeKeyword:
		r.outf("%s is a shell keyword\n", name)
	case shellTypeFunction:
		r.outf("%s is a function\n", name)
		r.outf("%s () \n", name)
		r.out("{ \n")
		r.out(bashFunctionBody(match.body))
		r.out("}\n")
	case shellTypeBuiltin:
		r.outf("%s is a shell builtin\n", name)
	case shellTypeFile:
		r.outf("%s is %s\n", name, match.path)
	}
}

func bashFunctionBody(body *syntax.Stmt) string {
	if body == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := syntax.NewPrinter(syntax.Indent(4)).Print(&buf, body); err != nil {
		return ""
	}
	text := strings.TrimSpace(buf.String())
	if text == "" {
		return ""
	}
	if strings.HasPrefix(text, "{") && strings.HasSuffix(text, "}") {
		text = text[1 : len(text)-1]
	}
	if text == "" {
		return ""
	}
	if strings.Contains(text, "\n") {
		text = strings.TrimPrefix(text, "\n")
		text = strings.TrimSuffix(text, "\n")
		if text == "" {
			return ""
		}
		return text + "\n"
	}
	line := trimBashFunctionLine(strings.TrimSpace(text))
	if line == "" {
		return ""
	}
	return "    " + line + "\n"
}

func trimBashFunctionLine(line string) string {
	if strings.HasSuffix(line, ";;") {
		return line
	}
	return strings.TrimSuffix(line, ";")
}

// flagParser is used to parse builtin flags.
//
// It's similar to the getopts implementation, but with some key differences.
// First, the API is designed for Go loops, making it easier to use directly.
// Second, it doesn't require the awkward ":ab" syntax that getopts uses.
// Third, it supports "-a" flags as well as "+a".
type flagParser struct {
	current   string
	remaining []string
}

func (p *flagParser) more() bool {
	if p.current != "" {
		// We're still parsing part of "-ab".
		return true
	}
	if len(p.remaining) == 0 {
		// Nothing left.
		p.remaining = nil
		return false
	}
	arg := p.remaining[0]
	if arg == "--" {
		// We explicitly stop parsing flags.
		p.remaining = p.remaining[1:]
		return false
	}
	if arg == "" || (arg[0] != '-' && arg[0] != '+') {
		// The next argument is not a flag.
		return false
	}
	// More flags to come.
	return true
}

func (p *flagParser) flag() string {
	arg := p.current
	if arg == "" {
		arg = p.remaining[0]
		p.remaining = p.remaining[1:]
	} else {
		p.current = ""
	}
	if len(arg) > 2 {
		// We have "-ab", so return "-a" and keep "-b".
		p.current = arg[:1] + arg[2:]
		arg = arg[:2]
	}
	return arg
}

func (p *flagParser) value() string {
	if len(p.remaining) == 0 {
		return ""
	}
	arg := p.remaining[0]
	p.remaining = p.remaining[1:]
	return arg
}

func (p *flagParser) args() []string { return p.remaining }

type getoptsResultKind uint8

const (
	getoptsResultDone getoptsResultKind = iota
	getoptsResultOption
	getoptsResultUnknown
	getoptsResultMissingArg
)

type getoptsResult struct {
	kind   getoptsResultKind
	opt    rune
	optarg string
}

func (r getoptsResult) done() bool { return r.kind == getoptsResultDone }

type getopts struct {
	argidx  int
	runeidx int
}

func (g *getopts) next(optstr string, args []string) getoptsResult {
	if len(args) == 0 || g.argidx >= len(args) {
		g.argidx = len(args)
		g.runeidx = 0
		return getoptsResult{kind: getoptsResultDone, opt: '?'}
	}
	arg := []rune(args[g.argidx])
	if len(arg) < 2 || arg[0] != '-' {
		return getoptsResult{kind: getoptsResultDone, opt: '?'}
	}
	if arg[1] == '-' {
		if len(arg) == 2 {
			g.argidx++
			g.runeidx = 0
		}
		return getoptsResult{kind: getoptsResultDone, opt: '?'}
	}

	opts := arg[1:]
	if g.runeidx >= len(opts) {
		g.argidx++
		g.runeidx = 0
		return getoptsResult{kind: getoptsResultDone, opt: '?'}
	}
	opt := opts[g.runeidx]
	i := strings.IndexRune(optstr, opt)
	if i < 0 {
		if g.runeidx+1 < len(opts) {
			g.runeidx++
		} else {
			g.argidx++
			g.runeidx = 0
		}
		return getoptsResult{kind: getoptsResultUnknown, opt: '?', optarg: string(opt)}
	}
	if i+1 >= len(optstr) || optstr[i+1] != ':' {
		if g.runeidx+1 < len(opts) {
			g.runeidx++
		} else {
			g.argidx++
			g.runeidx = 0
		}
		return getoptsResult{kind: getoptsResultOption, opt: opt}
	}
	if g.runeidx+1 < len(opts) {
		optarg := string(opts[g.runeidx+1:])
		g.argidx++
		g.runeidx = 0
		return getoptsResult{kind: getoptsResultOption, opt: opt, optarg: optarg}
	}
	g.argidx++
	g.runeidx = 0
	if g.argidx >= len(args) {
		return getoptsResult{kind: getoptsResultMissingArg, opt: ':', optarg: string(opt)}
	}
	optarg := args[g.argidx]
	g.argidx++
	return getoptsResult{kind: getoptsResultOption, opt: opt, optarg: optarg}
}

func (g *getopts) reset() {
	g.argidx = 0
	g.runeidx = 0
}

// optStatusText returns a shell option's status text display
func (r *Runner) optStatusText(status bool) string {
	if status {
		return "on"
	}
	return "off"
}

func printfBrokenPipe(err error) bool {
	if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "broken pipe") || strings.Contains(lower, "closed pipe")
}

func (r *Runner) lookupPrintfEnv(name string) (string, bool) {
	vr := r.lookupVar(name)
	if !vr.IsSet() || !vr.Exported || vr.Kind != expand.String {
		if runtime.GOOS == "linux" && r.printfEnv != nil {
			value, ok := r.printfEnv[name]
			return value, ok
		}
		return "", false
	}
	if runtime.GOOS == "linux" {
		if r.printfEnv == nil {
			r.printfEnv = make(map[string]string)
		}
		if value, ok := r.printfEnv[name]; ok {
			return value, true
		}
		r.printfEnv[name] = vr.String()
	}
	return vr.String(), true
}
