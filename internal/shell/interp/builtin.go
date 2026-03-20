// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"bufio"
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
			if vars {
				if ref, err := r.strictVarRef(arg); err == nil {
					if err := r.unsetVarByRef(ref); err != nil {
						r.errf("unset: %v\n", err)
						exit.code = 1
					}
					continue
				}
				if r.lookupVar(arg).Declared() {
					r.delVar(arg)
					continue
				}
			}
			if _, ok := r.funcs[arg]; ok && funcs {
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
			return failf(2, "usage: printf format [arguments]\n")
		}
		var destRef *syntax.VarRef
		switch args[0] {
		case "--":
			args = args[1:]
			if len(args) == 0 {
				return failf(2, "usage: printf format [arguments]\n")
			}
		case "-v":
			if len(args) < 2 {
				return failf(2, "printf: -v: option requires a variable name\n")
			}
			var err error
			destRef, err = r.strictVarRef(args[1])
			if err != nil {
				return failf(2, "printf: %q: invalid variable name for -v\n", args[1])
			}
			args = args[2:]
			if len(args) > 0 && args[0] == "--" {
				args = args[1:]
			}
			if len(args) == 0 {
				return failf(2, "usage: printf format [arguments]\n")
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
			if err := r.setVarByRef(prev, destRef, vr, false); err != nil {
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
		if len(args) < 1 {
			break
		}
		if !IsBuiltin(args[0]) {
			exit.code = 1
			return exit
		}
		exit = r.builtin(ctx, pos, args[0], args[1:])
	case "type":
		return r.typeBuiltin(ctx, args)
	case "hash":
		// TODO: implement. for now, having this as a no-op is better than nothing.
	case "eval":
		src := strings.Join(args, " ")
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
			r.errf("[: missing matching ]\n")
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
				// Match bash error format: "[: error" or just "error" for test
				if cmdName == "[" {
					r.errf("%s: %v\n", cmdName, err)
				} else {
					r.errf("%v\n", err)
				}
				parseErr = true
			},
		}
		expr := p.classicTest()
		if parseErr {
			exit.code = 2
			return exit
		}
		if r.bashTest(ctx, expr, true) == "" && exit.code == 0 {
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
		var prompt string
		raw := false
		silent := false
		readArray := false
		fp := flagParser{remaining: args}
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-s":
				silent = true
			case "-r":
				raw = true
			case "-a":
				readArray = true
			case "-p":
				prompt = fp.value()
				if prompt == "" {
					return failf(2, "read: -p: option requires an argument\n")
				}
			default:
				return failf(2, "read: invalid option %q\n", flag)
			}
		}

		args := fp.args()
		for _, name := range args {
			if !syntax.ValidName(name) {
				return failf(2, "read: invalid identifier %q\n", name)
			}
		}

		if prompt != "" {
			r.out(prompt)
		}

		var line []byte
		var err error
		_ = silent
		line, err = r.readLine(ctx, raw)
		if readArray {
			// read -a arrayname: split line into fields and assign to indexed array.
			arrayName := shellReplyVar
			if len(args) > 0 {
				arrayName = args[0]
			}
			// Use -1 as max to get all fields without joining the last ones.
			values := expand.ReadFields(r.ecfg, string(line), -1, raw)
			r.setVar(arrayName, expand.Variable{
				Set:  true,
				Kind: expand.Indexed,
				List: values,
			})
		} else {
			if len(args) == 0 {
				args = append(args, shellReplyVar)
			}

			values := expand.ReadFields(r.ecfg, string(line), len(args), raw)
			for i, name := range args {
				val := ""
				if i < len(values) {
					val = values[i]
				}
				r.setVarString(name, val)
			}
		}

		// We can get data back from readLine and an error at the same time, so
		// check err after we process the data.
		if err != nil {
			exit.code = 1
			return exit
		}

	case "getopts":
		if len(args) < 2 {
			return failf(2, "getopts: usage: getopts optstring name [arg ...]\n")
		}
		optind, _ := strconv.Atoi(r.envGet("OPTIND"))
		if optind-1 != r.optState.argidx {
			if optind < 1 {
				optind = 1
			}
			r.optState = getopts{argidx: optind - 1}
		}
		optstr := args[0]
		name := args[1]
		if !syntax.ValidName(name) {
			return failf(2, "getopts: invalid identifier: %q\n", name)
		}
		args = args[2:]
		if len(args) == 0 {
			args = r.Params
		}
		diagnostics := !strings.HasPrefix(optstr, ":")

		opt, optarg, done := r.optState.next(optstr, args)

		r.setVarString(name, string(opt))
		r.delVar("OPTARG")
		switch {
		case opt == '?' && diagnostics && !done:
			r.errf("getopts: illegal option -- %q\n", optarg)
		case opt == ':' && diagnostics:
			r.errf("getopts: option requires an argument -- %q\n", optarg)
		default:
			if optarg != "" {
				r.setVarString("OPTARG", optarg)
			}
		}
		if optind-1 != r.optState.argidx {
			r.setVarString("OPTIND", strconv.FormatInt(int64(r.optState.argidx+1), 10))
		}

		exit.oneIf(done)

	case "shopt":
		mode := ""
		posixOpts := false
		fp := flagParser{remaining: args}
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-s", "-u":
				mode = flag
			case "-o":
				posixOpts = true
			case "-p", "-q":
				panic(fmt.Sprintf("unhandled shopt flag: %s", flag))
			default:
				if flag == "--" {
					return failf(2, "shopt: --: invalid option\nshopt: usage: shopt [-pqsu] [-o] [optname ...]\n")
				}
				return failf(2, "shopt: invalid option %q\n", flag)
			}
		}
		args := fp.args()
		if len(args) == 0 {
			if posixOpts {
				for i, opt := range &posixOptsTable {
					r.printOptLine(opt.name, r.opts[i], true)
				}
			} else {
				for i, opt := range &bashOptsTable {
					r.printOptLine(opt.name, r.opts[len(posixOptsTable)+i], opt.supported)
				}
			}
			break
		}
		for _, arg := range args {
			opt, supported := (*bool)(nil), true
			if posixOpts {
				opt = r.posixOptByName(arg)
			} else {
				opt, supported = r.bashOptByName(arg)
			}
			if opt == nil {
				return failf(1, "shopt: invalid option name %q\n", arg)
			}

			switch mode {
			case "-s", "-u":
				if !supported {
					return failf(1, "shopt: unsupported option %q\n", arg)
				}
				*opt = mode == "-s"
			default: // ""
				r.printOptLine(arg, *opt, supported)
			}
		}
		r.updateExpandOpts()

	case "alias":
		show := func(name string, als alias) {
			r.outf("alias %s='%s'\n", name, als.value)
		}

		if len(args) == 0 {
			for name, als := range r.alias {
				show(name, als)
			}
		}
		for _, arg := range args {
			name, src, ok := strings.Cut(arg, "=")
			if !ok {
				als, ok := r.alias[name]
				if !ok {
					r.errf("alias: %q not found\n", name)
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
		for _, name := range args {
			delete(r.alias, name)
		}

	case "trap":
		fp := flagParser{remaining: args}
		callback := "-"
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-l", "-p":
				return failf(2, "trap: %q: NOT IMPLEMENTED flag\n", flag)
			case "-":
				// default signal
			default:
				r.errf("trap: %q: invalid option\n", flag)
				r.errf("trap: usage: trap [-lp] [[arg] signal_spec ...]\n")
				exit.code = 2
				return exit
			}
		}
		args := fp.args()
		switch len(args) {
		case 0:
			// Print non-default signals
			if r.callbackExit != "" {
				r.outf("trap -- %q EXIT\n", r.callbackExit)
			}
			if r.callbackErr != "" {
				r.outf("trap -- %q ERR\n", r.callbackErr)
			}
		case 1:
			// assume it's a signal, the default will be restored
		default:
			callback = args[0]
			args = args[1:]
		}
		// For now, treat both empty and - the same since ERR and EXIT have no
		// default callback.
		if callback == "-" {
			callback = ""
		}
		for _, arg := range args {
			switch arg {
			case "ERR":
				r.callbackErr = callback
			case "EXIT":
				r.callbackExit = callback
			default:
				return failf(2, "trap: %s: invalid signal specification\n", arg)
			}
		}

	case "readarray", "mapfile":
		dropDelim := false
		delim := "\n"
		fp := flagParser{remaining: args}
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-t":
				// Remove the delim from each line read
				dropDelim = true
			case "-d":
				if len(fp.remaining) == 0 {
					return failf(2, "%s: -d: option requires an argument\n", name)
				}
				delim = fp.value()
				if delim == "" {
					// Bash sets the delim to an ASCII NUL if provided with an empty
					// string.
					delim = "\x00"
				}
			default:
				return failf(2, "%s: invalid option %q\n", name, flag)
			}
		}

		args := fp.args()
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

		var vr expand.Variable
		vr.Kind = expand.Indexed
		scanner := bufio.NewScanner(r.stdin)
		scanner.Split(mapfileSplit(delim[0], dropDelim))
		for scanner.Scan() {
			vr.List = append(vr.List, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			return failf(2, "%s: unable to read, %v\n", name, err)
		}
		r.setVar(arrayName, vr)

	default:
		return failf(2, "%s: unimplemented builtin\n", name)
	}
	return exit
}

// mapfileSplit returns a suitable Split function for a [bufio.Scanner];
// the code is mostly stolen from [bufio.ScanLines].
func mapfileSplit(delim byte, dropDelim bool) bufio.SplitFunc {
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		if i := bytes.IndexByte(data, delim); i >= 0 {
			// We have a full newline-terminated line.
			if dropDelim {
				return i + 1, data[0:i], nil
			} else {
				return i + 1, data[0 : i+1], nil
			}
		}
		// If we're at EOF, we have a final, non-terminated line. Return it.
		if atEOF {
			return len(data), data, nil
		}
		// Request more data.
		return 0, nil, nil
	}
}

func (r *Runner) printOptLine(name string, enabled, supported bool) {
	state := r.optStatusText(enabled)
	if supported {
		r.outf("%s\t%s\n", name, state)
		return
	}
	r.outf("%s\t%s\t(%q not supported)\n", name, state, r.optStatusText(!enabled))
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
	logical := false
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
			r.errf("pwd: usage: pwd [-LP]\n")
			exit.code = 2
			return exit
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
	useLogical := logical || (!physical && r.envGet("POSIXLY_CORRECT") != "")
	if physical || !useLogical {
		return r.pwdPhysicalPath(ctx)
	}
	if candidate, ok := r.pwdLogicalPath(); ok {
		return candidate, nil
	}
	return r.pwdPhysicalPath(ctx)
}

func (r *Runner) pwdLogicalPath() (string, bool) {
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
	target := ""
	show := false
	switch len(args) {
	case 0:
		target = r.envGet("HOME")
	case 1:
		if args[0] == "--" {
			target = r.envGet("HOME")
		} else {
			target = args[0]
		}
	case 2:
		if args[0] != "--" {
			r.errf("cd: usage: cd [dir]\n")
			return 2
		}
		target = args[1]
	default:
		r.errf("cd: usage: cd [dir]\n")
		return 2
	}
	if target == "-" {
		target = r.envGet("OLDPWD")
		show = true
	}
	code := r.changeDir(ctx, "cd", target)
	if code == 0 && show {
		r.outf("%s\n", r.envGet("PWD"))
	}
	return code
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
	if !strings.ContainsRune(args[0], '/') {
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
		kind:       frameKindSource,
		label:      "source",
		execFile:   sourceName,
		bashSource: bashSource,
		callLine:   r.sourceCallLine(pos),
		internal:   internal,
	}
	r.inSource = true
	runErr := r.runShellReader(ctx, f, sourceName, frame)

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
	show := false
	useDefaultPath := false
	forcePath := false
	fp := flagParser{remaining: args}
	for fp.more() {
		switch flag := fp.flag(); flag {
		case "-v":
			show = true
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
	if show {
		last := uint8(0)
		for _, arg := range args {
			last = 0
			if !forcePath && !useDefaultPath {
				if r.funcs[arg] != nil || IsBuiltin(arg) {
					r.outf("%s\n", arg)
					continue
				}
			}
			if foundPath, err := r.lookPath(ctx, r.Dir, r.writeEnv, arg, true, useDefaultPath); err == nil {
				r.outf("%s\n", foundPath)
			} else {
				last = 1
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
	}
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
		r.dirStack = append(r.dirStack[:0], r.Dir)
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

	oldCurrent := r.Dir
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
		next, code := r.resolveDir(ctx, "pushd", newStack[0])
		if code != 0 {
			return code
		}
		r.dirStack = newStack
		r.setCurrentDir(next, oldCurrent)
		return r.dirsBuiltin(nil)
	}

	if noChdir {
		newStack := make([]string, 0, len(r.dirStack)+1)
		newStack = append(newStack, r.dirStack[0], operand)
		newStack = append(newStack, r.dirStack[1:]...)
		r.dirStack = newStack
		return r.dirsBuiltin(nil)
	}

	next, code := r.resolveDir(ctx, "pushd", operand)
	if code != 0 {
		return code
	}
	newStack := make([]string, 0, len(r.dirStack)+1)
	newStack = append(newStack, next)
	newStack = append(newStack, r.dirStack...)
	r.dirStack = newStack
	r.setCurrentDir(next, oldCurrent)
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

	oldCurrent := r.Dir
	newStack := removeDirStackIndex(r.dirStack, idx)
	if noChdir {
		newStack[0] = oldCurrent
		r.dirStack = newStack
	} else if idx == 0 {
		next, code := r.resolveDir(ctx, "popd", newStack[0])
		if code != 0 {
			return code
		}
		r.dirStack = newStack
		r.setCurrentDir(next, oldCurrent)
	} else {
		r.dirStack = newStack
	}
	return r.dirsBuiltin(nil)
}

func (r *Runner) changeDir(ctx context.Context, cmd, name string) uint8 {
	next, code := r.resolveDir(ctx, cmd, name)
	if code != 0 {
		return code
	}
	r.setCurrentDir(next, r.Dir)
	return 0
}

func (r *Runner) resolveDir(ctx context.Context, cmd, name string) (string, uint8) {
	name = cmp.Or(name, ".")
	apath := r.absPath(name)
	info, err := r.stat(ctx, apath)
	if err != nil {
		r.errf("%s: %s: No such file or directory\n", cmd, name)
		return "", 1
	}
	if !info.IsDir() {
		r.errf("%s: %s: Not a directory\n", cmd, name)
		return "", 1
	}
	if r.access(ctx, apath, access_X_OK) != nil {
		r.errf("%s: %s: Permission denied\n", cmd, name)
		return "", 1
	}
	return apath, 0
}

func (r *Runner) setCurrentDir(newDir, oldDir string) {
	r.Dir = newDir
	r.setVarString("OLDPWD", oldDir)
	r.setVarString("PWD", newDir)
	if len(r.dirStack) == 0 {
		r.dirStack = append(r.dirStack, newDir)
	} else {
		r.dirStack[0] = newDir
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
		if body := r.funcs[name]; body != nil && !r.funcInternal(name) {
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
		printer := syntax.NewPrinter()
		var buf bytes.Buffer
		printer.Print(&buf, &syntax.FuncDecl{
			Name:   &syntax.Lit{Value: name},
			Parens: true,
			Body:   match.body,
		})
		r.outf("%s\n", buf.String())
	case shellTypeBuiltin:
		r.outf("%s is a shell builtin\n", name)
	case shellTypeFile:
		r.outf("%s is %s\n", name, match.path)
	}
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

type getopts struct {
	argidx  int
	runeidx int
}

func (g *getopts) next(optstr string, args []string) (opt rune, optarg string, done bool) {
	if len(args) == 0 || g.argidx >= len(args) {
		return '?', "", true
	}
	arg := []rune(args[g.argidx])
	if len(arg) < 2 || arg[0] != '-' || arg[1] == '-' {
		return '?', "", true
	}

	opts := arg[1:]
	opt = opts[g.runeidx]
	if g.runeidx+1 < len(opts) {
		g.runeidx++
	} else {
		g.argidx++
		g.runeidx = 0
	}

	i := strings.IndexRune(optstr, opt)
	if i < 0 {
		// invalid option
		return '?', string(opt), false
	}

	if i+1 < len(optstr) && optstr[i+1] == ':' {
		if g.argidx >= len(args) {
			// missing argument
			return ':', string(opt), false
		}
		optarg = args[g.argidx]
		g.argidx++
		g.runeidx = 0
	}

	return opt, optarg, false
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
