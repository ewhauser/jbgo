// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

// Package interp implements an interpreter to execute shell programs
// parsed by the [syntax] package.
//
// The interpreter generally aims to behave like Bash,
// but it does not support all of its features.
//
// The interpreter powers both the non-interactive and interactive shell paths
// owned by the gbash shell core.
package interp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/syntax"
)

// A Runner interprets shell programs. It can be reused, but it is not safe for
// concurrent use. Use [NewRunner] to build a new Runner.
//
// Note that writes to Stdout and Stderr may be concurrent if background
// commands are used. If you plan on using an [io.Writer] implementation that
// isn't safe for concurrent use, consider a workaround like hiding writes
// behind a mutex.
type Runner struct {
	// Env specifies the initial environment for the interpreter, which must
	// not be nil.
	//
	// If it includes a TMPDIR variable describing an absolute directory,
	// it is used as the directory in which to create temporary files needed
	// for the interpreter's use, such as named pipes for process substitutions.
	// Otherwise, a deterministic virtual default is used.
	Env expand.Environ

	// writeEnv overlays [Runner.Env] so that we can write environment variables
	// as an overlay.
	writeEnv expand.WriteEnviron

	// Dir specifies the working directory of the command, which must be an
	// absolute path.
	Dir string

	// logicalDir is the shell's internal logical cwd. Unlike the exported PWD
	// variable, it is not mutated by ordinary shell variable assignments.
	logicalDir string

	// tempDir is either $TMPDIR from [Runner.Env] or a deterministic virtual
	// default.
	tempDir string

	// Params are the current shell parameters, e.g. from running a shell
	// file or calling a function. Accessible via the $@/$* family of vars.
	Params []string

	// Separate maps - note that bash allows a name to be both a var and a
	// func simultaneously.
	funcs map[string]*syntax.Stmt

	funcSources   map[string]string
	funcInternals map[string]bool
	funcBodySrc   map[string]funcSourceSpan

	alias map[string]alias

	// callHandler is a function allowing to replace a simple command's
	// arguments. It may be nil.
	callHandler CallHandlerFunc

	// execHandler is responsible for executing programs. It must not be nil.
	execHandler ExecHandlerFunc

	// openHandler is a function responsible for opening files. It must not be nil.
	openHandler OpenHandlerFunc

	// readDirHandler is a function responsible for reading directories during
	// glob expansion. It must be non-nil.
	readDirHandler ReadDirHandlerFunc

	// statHandler is a function responsible for getting file stat. It must be non-nil.
	statHandler StatHandlerFunc

	// realpathHandler resolves a physical path without host filesystem fallbacks.
	realpathHandler RealpathHandlerFunc

	// procSubstHandler overrides the default host-FIFO implementation used by
	// process substitutions.
	procSubstHandler ProcSubstHandlerFunc

	uid  int
	euid int
	gid  int
	egid int
	pid  int
	ppid int

	stdin  StdinReader // e.g. the read end of a pipe
	stdout io.Writer
	stderr io.Writer
	fds    map[int]*shellFD

	ecfg *expand.Config
	ectx context.Context // just so that Runner.Subshell can use it again

	legacyBashCompat bool

	// didReset remembers whether the runner has ever been reset. This is
	// used so that Reset is automatically called when running any program
	// or node for the first time on a Runner.
	didReset bool

	filename string // only if Node was a File

	topLevelScriptPath string
	frames             []execFrame
	internalRun        bool

	// >0 to break or continue out of N enclosing loops
	breakEnclosing, contnEnclosing int

	inLoop       bool
	inFunc       bool
	inSource     bool
	evalDepth    int
	handlingTrap bool // whether we're currently in a trap callback

	// track if a sourced script set positional parameters
	sourceSetParams bool

	suppressXTrace bool

	currentChunkSource     string
	currentChunkSourceBase uint

	// noErrExit prevents failing commands from triggering [optErrExit],
	// such as the condition in a [syntax.IfClause].
	noErrExit bool

	// The current and last exit statuses. They can only be different if
	// the interpreter is in the middle of running a statement. In that
	// scenario, 'exit' is the status for the current statement being run,
	// and 'lastExit' corresponds to the previous statement that was run.
	exit     exitStatus
	lastExit exitStatus

	lastExpandExit exitStatus // used to surface exit statuses while expanding fields

	// bgProcs holds all background shells spawned by this runner.
	// Their PIDs are 1-indexed, from 1 to len(bgProcs), with a "g" prefix
	// to distinguish them from real PIDs on the host operating system.
	//
	// Note that each shell only tracks its direct children;
	// subshells do not share nor inherit the background PIDs they can wait for.
	bgProcs []bgProc

	opts runnerOpts

	startupHome string

	origDir     string
	origParams  []string
	origOpts    runnerOpts
	origStdin   StdinReader
	origStdout  io.Writer
	origStderr  io.Writer
	origFDs     map[int]*shellFD
	fdSnapshots []map[int]*shellFD
	origStart   time.Time

	startTime time.Time

	inRedirectWord int

	// Most scripts don't use pushd/popd, so make space for the initial visible
	// cwd without requiring an extra allocation.
	dirStack     []string
	dirBootstrap [1]string

	optState getopts

	interactive            bool
	commandString          bool
	syntheticPipelineStmts map[*syntax.Stmt]*syntax.Stmt

	// keepRedirs is used so that "exec" can make any redirections
	// apply to the current shell, and not just the command.
	keepRedirs bool

	// printfEnv keeps process-level environment state used by printf %T.
	printfEnv map[string]string

	// Fake signal callbacks
	callbackErr  string
	callbackExit string
}

type funcSourceSpan struct {
	text string
	base uint
}

// exitStatus holds the state of the shell after running one command.
// Beyond the exit status code, it also holds whether the shell should return or exit,
// as well as any Go error values that should be given back to the user.
//
// TODO(v4): consider replacing ExitStatus with a struct like this,
// so that an [ExecHandlerFunc] can e.g. mimic `exit 0` or fatal errors
// with specific exit codes.
type exitStatus struct {
	// code is the exit status code.
	// When code is zero, err must be nil.
	code uint8

	// TODO: consider an enum, as only one of these should be set at a time
	returning bool // whether the current function `return`ed
	exiting   bool // whether the current shell is exiting
	fatalExit bool // whether the current shell is exiting due to a fatal error; err below must not be nil

	// err holds the error information for a non-zero exit status code or fatal error.
	// Used so that running a single statement with a custom handler
	// which returns a non-fatal Go error such as [ExitStatus],
	// can be returned by [Runner.Run] without being lost entirely.
	err error
}

// clear sets the exit status code and error to zero, as long as the exit status
// was not set by `return`, `exit`, or a fatal error.
func (e *exitStatus) clear() {
	if e.returning || e.exiting || e.fatalExit {
		return
	}
	e.code = 0
	e.err = nil
}

func (e *exitStatus) ok() bool { return e.code == 0 }

// oneIf sets the exit status code to 1 if b is true.
// Note that it assumes the exit status hasn't been set yet,
// meaning that [exitStatus.code] and [exitStatus.err] are zero values.
func (e *exitStatus) oneIf(b bool) {
	if b {
		e.code = 1
	}
}

func (e *exitStatus) fatal(err error) {
	if e.fatalExit || err == nil {
		return
	}
	e.exiting = true
	e.fatalExit = true
	e.err = err
	if e.code == 0 {
		e.code = 1
	}
}

func (e *exitStatus) fromHandlerError(err error) {
	if err == nil {
		return
	}
	var exit errBuiltinExitStatus
	var es ExitStatus
	if errors.As(err, &exit) {
		*e = exitStatus(exit)
	} else if errors.As(err, &es) {
		e.err = err
		e.code = uint8(es)
	} else {
		e.fatal(err) // handler's custom fatal error
	}
}

type bgProc struct {
	// closed when the background process finishes,
	// after which point the result fields below are set.
	done chan struct{}

	exit          *exitStatus
	procSubst     bool
	waitAtStmtEnd bool
}

type alias struct {
	value string
}

func (a alias) blank() bool {
	return strings.TrimRight(a.value, " \t") != a.value
}

const (
	defaultVirtualHomeDir = "/home/agent"
	defaultVirtualTempDir = "/tmp"
	defaultVirtualID      = 1000
	defaultVirtualPID     = 1
	defaultVirtualPPID    = 0
)

// RunnerConfig defines the runtime boundary for a Runner.
type RunnerConfig struct {
	Env expand.Environ

	// StartupHome overrides the home directory used for plain current-user
	// tilde expansion. Callers should only populate this from a trusted
	// sandbox boundary.
	StartupHome string

	// Dir is the authoritative virtual current directory.
	Dir string

	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Params []string

	Interactive   bool
	CommandString bool

	LegacyBashCompat bool

	CallHandler      CallHandlerFunc
	ExecHandler      ExecHandlerFunc
	OpenHandler      OpenHandlerFunc
	ReadDirHandler   ReadDirHandlerFunc
	StatHandler      StatHandlerFunc
	RealpathHandler  RealpathHandlerFunc
	ProcSubstHandler ProcSubstHandlerFunc
}

func newRunnerBase() *Runner {
	r := &Runner{}
	r.dirStack = r.dirBootstrap[:0]
	// turn "on" the default Bash options
	for i, opt := range &bashOptsTable {
		r.opts[len(posixOptsTable)+i] = opt.defaultState
	}
	return r
}

func (r *Runner) applyConstructorDefaults() error {
	if r.Env == nil {
		r.Env = expand.ListEnviron()
	}
	if r.Dir == "" {
		if home := r.Env.Get("HOME").String(); path.IsAbs(home) {
			r.Dir = path.Clean(home)
		} else {
			r.Dir = defaultVirtualHomeDir
		}
	} else if !path.IsAbs(r.Dir) {
		return fmt.Errorf("working directory must be an absolute path: %q", r.Dir)
	} else {
		r.Dir = path.Clean(r.Dir)
	}
	if r.tempDir == "" {
		if dir := r.Env.Get("TMPDIR").String(); path.IsAbs(dir) {
			r.tempDir = path.Clean(dir)
		} else {
			r.tempDir = defaultVirtualTempDir
		}
	} else if !path.IsAbs(r.tempDir) {
		return fmt.Errorf("temporary directory must be an absolute path: %q", r.tempDir)
	} else {
		r.tempDir = path.Clean(r.tempDir)
	}
	if r.openHandler == nil {
		r.openHandler = closedOpenHandler()
	}
	if r.readDirHandler == nil {
		r.readDirHandler = closedReadDirHandler()
	}
	if r.statHandler == nil {
		r.statHandler = closedStatHandler()
	}
	if r.realpathHandler == nil {
		r.realpathHandler = defaultRealpathHandler()
	}
	if r.stdout == nil || r.stderr == nil {
		if err := r.setStdIO(r.stdin, r.stdout, r.stderr); err != nil {
			return err
		}
	}
	r.normalizeVirtualState()
	return nil
}

func (r *Runner) normalizeVirtualState() {
	r.uid = firstVirtualInt(r.uid, r.Env, "UID", defaultVirtualID)
	r.euid = firstVirtualInt(r.euid, r.Env, "EUID", r.uid)
	r.gid = firstVirtualInt(r.gid, r.Env, "GID", defaultVirtualID)
	r.egid = firstVirtualInt(r.egid, r.Env, "EGID", r.gid)
	r.pid = firstVirtualInt(r.pid, nil, "", defaultVirtualPID)
	r.ppid = firstVirtualInt(r.ppid, nil, "", defaultVirtualPPID)
}

func firstVirtualInt(current int, env expand.Environ, name string, fallback int) int {
	if current != 0 {
		return current
	}
	if env != nil && name != "" {
		if value, err := strconv.Atoi(env.Get(name).String()); err == nil {
			return value
		}
	}
	return fallback
}

// NewRunner creates a new Runner configured with the explicit gbash runtime
// boundary used by the shell core.
func NewRunner(cfg *RunnerConfig) (*Runner, error) {
	if cfg == nil {
		cfg = &RunnerConfig{}
	}
	r := newRunnerBase()
	r.Env = cfg.Env
	r.startupHome = cfg.StartupHome
	r.Dir = cfg.Dir
	r.callHandler = cfg.CallHandler
	r.execHandler = cfg.ExecHandler
	r.openHandler = cfg.OpenHandler
	r.readDirHandler = cfg.ReadDirHandler
	r.statHandler = cfg.StatHandler
	r.realpathHandler = cfg.RealpathHandler
	r.procSubstHandler = cfg.ProcSubstHandler
	r.legacyBashCompat = cfg.LegacyBashCompat
	r.commandString = cfg.CommandString
	if err := r.setStdIO(cfg.Stdin, cfg.Stdout, cfg.Stderr); err != nil {
		return nil, err
	}
	if err := r.setParams(cfg.Params...); err != nil {
		return nil, err
	}
	r.setInteractive(cfg.Interactive)
	return r, r.applyConstructorDefaults()
}

func stdinReader(r io.Reader) StdinReader {
	switch r := r.(type) {
	case StdinReader:
		return r
	case nil:
		return nil
	default:
		pr, pw := NewVirtualPipe()
		go func() {
			io.Copy(pw, r)
			pw.Close()
		}()
		return pr
	}
}

func (r *Runner) posixOptByName(name string) *bool {
	for i, opt := range &posixOptsTable {
		if opt.name == name {
			return &r.opts[i]
		}
	}
	return nil
}

func (r *Runner) posixOptByFlag(flag byte) *bool {
	for i, opt := range &posixOptsTable {
		if opt.flag == flag {
			return &r.opts[i]
		}
	}
	return nil
}

func (r *Runner) bashOptByName(name string) (status *bool, supported bool) {
	for i, opt := range &bashOptsTable {
		if opt.name == name {
			index := len(posixOptsTable) + i
			return &r.opts[index], opt.supported
		}
	}
	return nil, false
}

// runnerOpts contains all POSIX Shell and Bash options as one contiguous table.
type runnerOpts [len(posixOptsTable) + len(bashOptsTable)]bool

type posixOpt struct {
	flag byte   // one-character flag form for this option; a space if none exists
	name string // full name of the option
}

type bashOpt struct {
	name         string
	defaultState bool // Bash's default value for this option
	supported    bool // whether we support the option's non-default state
}

var posixOptsTable = [...]posixOpt{
	// sorted alphabetically by name
	{'a', "allexport"},
	{'e', "errexit"},
	{'n', "noexec"},
	{'f', "noglob"},
	{'u', "nounset"},
	{'v', "verbose"},
	{'x', "xtrace"},
	{' ', "pipefail"},
}

var bashOptsTable = [...]bashOpt{
	// supported options, sorted alphabetically by name
	{
		name:         "dotglob",
		defaultState: false,
		supported:    true,
	},
	{
		name:         "expand_aliases",
		defaultState: false,
		supported:    true,
	},
	{
		name:         "extdebug",
		defaultState: false,
		supported:    true,
	},
	{
		name:         "extglob",
		defaultState: false,
		supported:    true,
	},
	{
		name:         "globstar",
		defaultState: false,
		supported:    true,
	},
	{
		name:         "lastpipe",
		defaultState: false,
		supported:    true,
	},
	{
		name:         "nocaseglob",
		defaultState: false,
		supported:    true,
	},
	{
		name:         "nullglob",
		defaultState: false,
		supported:    true,
	},
	// unsupported options, sorted alphabetically by name
	{name: "assoc_expand_once"},
	{name: "autocd"},
	{name: "cdable_vars"},
	{name: "cdspell"},
	{name: "checkhash"},
	{name: "checkjobs"},
	{
		name:         "checkwinsize",
		defaultState: true,
	},
	{
		name:         "cmdhist",
		defaultState: true,
	},
	{name: "compat31"},
	{name: "compat32"},
	{name: "compat40"},
	{name: "compat41"},
	{name: "compat42"},
	{name: "compat44"},
	{name: "compat43"},
	{name: "compat44"},
	{
		name:         "complete_fullquote",
		defaultState: true,
	},
	{name: "direxpand"},
	{name: "dirspell"},
	{name: "execfail"},
	{
		name:         "extquote",
		defaultState: true,
	},
	{name: "failglob"},
	{
		name:         "force_fignore",
		defaultState: true,
	},
	{name: "globasciiranges"},
	{name: "gnu_errfmt"},
	{name: "histappend"},
	{name: "histreedit"},
	{name: "histverify"},
	{
		name:         "hostcomplete",
		defaultState: true,
	},
	{name: "huponexit"},
	{
		name:         "inherit_errexit",
		defaultState: true,
	},
	{
		name:         "interactive_comments",
		defaultState: true,
	},
	{name: "lithist"},
	{name: "localvar_inherit"},
	{name: "localvar_unset"},
	{name: "login_shell"},
	{name: "mailwarn"},
	{name: "no_empty_cmd_completion"},
	{name: "nocasematch"},
	{
		name:         "progcomp",
		defaultState: true,
	},
	{name: "progcomp_alias"},
	{
		name:         "promptvars",
		defaultState: true,
	},
	{name: "restricted_shell"},
	{name: "shift_verbose"},
	{
		name:         "sourcepath",
		defaultState: true,
	},
	{name: "xpg_echo"},
}

// To access the shell options arrays without a linear search when we
// know which option we're after at compile time. First come the shell options,
// then the bash options.
const (
	// These correspond to indexes in [shellOptsTable]
	optAllExport = iota
	optErrExit
	optNoExec
	optNoGlob
	optNoUnset
	optVerbose
	optXTrace
	optPipeFail

	// These correspond to indexes (offset by the above seven items) of
	// supported options in [bashOptsTable]
	optDotGlob
	optExpandAliases
	optExtDebug
	optExtGlob
	optGlobStar
	optLastPipe
	optNoCaseGlob
	optNullGlob
)

// Reset returns a runner to its initial state, right before the first call to
// Run or Reset.
//
// Typically, this function only needs to be called if a runner is reused to run
// multiple programs non-incrementally. Not calling Reset between each run will
// mean that the shell state will be kept, including variables, options, and the
// current directory.
func (r *Runner) Reset() {
	if !r.didReset {
		r.origDir = r.Dir
		r.origParams = r.Params
		r.origOpts = r.opts
		r.origStdin = r.stdin
		r.origStdout = r.stdout
		r.origStderr = r.stderr
		r.origFDs = cloneFDTable(initialFDTable(r.stdin, r.stdout, r.stderr))
		r.origStart = time.Now()

		if r.execHandler == nil {
			r.execHandler = closedExecHandler()
		}
	}
	// reset the internal state
	*r = Runner{
		Env:              r.Env,
		tempDir:          r.tempDir,
		callHandler:      r.callHandler,
		execHandler:      r.execHandler,
		openHandler:      r.openHandler,
		readDirHandler:   r.readDirHandler,
		statHandler:      r.statHandler,
		realpathHandler:  r.realpathHandler,
		procSubstHandler: r.procSubstHandler,
		uid:              r.uid,
		euid:             r.euid,
		gid:              r.gid,
		egid:             r.egid,
		pid:              r.pid,
		ppid:             r.ppid,
		startupHome:      r.startupHome,

		// These can be set by functions like [Dir] or [Params], but
		// builtins can overwrite them; reset the fields to whatever the
		// constructor set up.
		Dir:              r.origDir,
		Params:           r.origParams,
		opts:             r.origOpts,
		stdin:            r.origStdin,
		stdout:           r.origStdout,
		stderr:           r.origStderr,
		fds:              cloneFDTable(r.origFDs),
		legacyBashCompat: r.legacyBashCompat,
		commandString:    r.commandString,

		origDir:    r.origDir,
		origParams: r.origParams,
		origOpts:   r.origOpts,
		origStdin:  r.origStdin,
		origStdout: r.origStdout,
		origStderr: r.origStderr,
		origFDs:    cloneFDTable(r.origFDs),
		origStart:  r.origStart,
		startTime:  r.origStart,

		funcSources:   r.funcSources,
		funcInternals: r.funcInternals,
		funcs:         r.funcs,
		printfEnv:     make(map[string]string),

		dirStack:               r.dirStack[:0],
		topLevelScriptPath:     r.topLevelScriptPath,
		interactive:            r.interactive,
		syntheticPipelineStmts: r.syntheticPipelineStmts,
	}
	// Ensure we stop referencing any pointers before we reuse bgProcs.
	clear(r.bgProcs)
	r.bgProcs = r.bgProcs[:0]

	if r.funcSources == nil {
		r.funcSources = make(map[string]string)
	} else {
		clear(r.funcSources)
	}
	if r.funcInternals == nil {
		r.funcInternals = make(map[string]bool)
	} else {
		clear(r.funcInternals)
	}
	if r.funcs == nil {
		r.funcs = make(map[string]*syntax.Stmt, 4)
	} else {
		clear(r.funcs)
	}
	// TODO(v4): Use the supplied Env directly if it implements enough methods.
	r.writeEnv = &overlayEnviron{parent: r.Env}
	if !r.writeEnv.Get("HOME").IsSet() {
		r.setVarString("HOME", defaultVirtualHomeDir)
	}
	if !r.writeEnv.Get("TMPDIR").IsSet() {
		r.setVarString("TMPDIR", r.tempDir)
	}
	r.setVar("UID", expand.Variable{
		Set:      true,
		Kind:     expand.String,
		ReadOnly: true,
		Str:      strconv.Itoa(r.uid),
	})
	r.setVar("EUID", expand.Variable{
		Set:      true,
		Kind:     expand.String,
		ReadOnly: true,
		Str:      strconv.Itoa(r.euid),
	})
	r.setVar("GID", expand.Variable{
		Set:      true,
		Kind:     expand.String,
		ReadOnly: true,
		Str:      strconv.Itoa(r.gid),
	})
	r.setVar("EGID", expand.Variable{
		Set:      true,
		Kind:     expand.String,
		ReadOnly: true,
		Str:      strconv.Itoa(r.egid),
	})
	pwd := r.Dir
	if candidate := r.writeEnv.Get("PWD").String(); r.pwdCandidateMatchesDir(candidate) {
		pwd = candidate
	}
	r.logicalDir = pwd
	r.setExportedVarString("PWD", pwd)
	r.setVarString("IFS", " \t\n")
	r.setVarString("OPTIND", "1")
	r.setVarString("PS4", "+ ")

	r.dirStack = append(r.dirStack, r.logicalDir)
	r.syncStandardFDs()

	r.didReset = true
}

// ExitStatus is a non-zero status code resulting from running a shell node.
type ExitStatus uint8

func (s ExitStatus) Error() string { return fmt.Sprintf("exit status %d", s) }

// Run interprets a node, which can be a [*File], [*Stmt], or [Command]. If a non-nil
// error is returned, it will typically contain a command's exit status, which
// can be retrieved with [errors.As] and [ExitStatus].
//
// Run can be called multiple times synchronously to interpret programs
// incrementally. To reuse a [Runner] without keeping the internal shell state,
// call Reset.
//
// Calling Run on an entire [*File] implies an exit, meaning that an exit trap may
// run.
func (r *Runner) Run(ctx context.Context, node syntax.Node) error {
	return r.run(ctx, node, true, true)
}

func (r *Runner) currentRunError() error {
	if err := r.exit.err; err != nil {
		if r.exit.code == 0 {
			panic("ended up with a non-nil exitStatus.err but a zero exitStatus.code")
		}
		return err
	}
	if code := r.exit.code; code != 0 {
		return ExitStatus(code)
	}
	return nil
}

func (r *Runner) run(ctx context.Context, node syntax.Node, runExitTrap, manageMainFrame bool) error {
	if !r.didReset {
		r.Reset()
	}
	r.fillExpandConfig(ctx)
	r.exit = exitStatus{}
	r.filename = ""
	switch node := node.(type) {
	case *syntax.File:
		r.filename = node.Name
		if manageMainFrame && !r.internalRun && r.topLevelScriptPath != "" && node.Name == r.topLevelScriptPath {
			restoreFrame := r.pushFrame(execFrame{
				kind:       frameKindMain,
				label:      "main",
				execFile:   node.Name,
				bashSource: node.Name,
				callLine:   0,
				internal:   false,
			})
			r.stmts(ctx, node.Stmts)
			restoreFrame()
		} else {
			r.stmts(ctx, node.Stmts)
		}
	case *syntax.Stmt:
		r.stmt(ctx, node)
	case syntax.Command:
		r.cmd(ctx, node)
	default:
		return fmt.Errorf("node can only be File, Stmt, or Command: %T", node)
	}
	if runExitTrap {
		r.trapCallback(ctx, r.callbackExit, "exit")
	}
	return r.currentRunError()
}

// Exited reports whether the last Run call should exit an entire shell. This
// can be triggered by the "exit" built-in command, for example.
//
// Note that this state is overwritten at every Run call, so it should be
// checked immediately after each Run call.
func (r *Runner) Exited() bool {
	return r.exit.exiting
}

// subshell is like [Runner.subshell], but allows skipping some allocations and copies
// when creating subshells which will not be used concurrently with the parent shell.
// TODO(v4): we should expose this, e.g. SubshellForeground and SubshellBackground.
func (r *Runner) subshell(background bool) *Runner {
	if !r.didReset {
		r.Reset()
	}
	// Keep in sync with the Runner type. Manually copy fields, to not copy
	// sensitive ones like [errgroup.Group], and to do deep copies of slices.
	r2 := &Runner{
		Dir:                    r.Dir,
		logicalDir:             r.logicalDir,
		tempDir:                r.tempDir,
		Params:                 r.Params,
		callHandler:            r.callHandler,
		execHandler:            r.execHandler,
		openHandler:            r.openHandler,
		readDirHandler:         r.readDirHandler,
		statHandler:            r.statHandler,
		realpathHandler:        r.realpathHandler,
		procSubstHandler:       r.procSubstHandler,
		uid:                    r.uid,
		euid:                   r.euid,
		gid:                    r.gid,
		egid:                   r.egid,
		pid:                    r.pid,
		ppid:                   r.ppid,
		stdin:                  r.stdin,
		stdout:                 r.stdout,
		stderr:                 r.stderr,
		fds:                    cloneFDTable(r.fds),
		filename:               r.filename,
		topLevelScriptPath:     r.topLevelScriptPath,
		internalRun:            r.internalRun,
		opts:                   r.opts,
		interactive:            r.interactive,
		commandString:          r.commandString,
		legacyBashCompat:       r.legacyBashCompat,
		inFunc:                 r.inFunc,
		inSource:               r.inSource,
		exit:                   r.exit,
		lastExit:               r.lastExit,
		suppressXTrace:         r.suppressXTrace,
		currentChunkSource:     r.currentChunkSource,
		currentChunkSourceBase: r.currentChunkSourceBase,
		printfEnv:              r.printfEnv,
		origStart:              r.origStart,
		startTime:              r.startTime,

		origStdout: r.origStdout, // used for process substitutions
	}
	r2.writeEnv = newOverlayEnviron(r.writeEnv, background)
	// Funcs are copied, since they might be modified.
	r2.funcs = maps.Clone(r.funcs)
	r2.funcSources = maps.Clone(r.funcSources)
	r2.funcInternals = maps.Clone(r.funcInternals)
	r2.alias = maps.Clone(r.alias)
	r2.frames = append(r2.frames, r.frames...)
	r2.syntheticPipelineStmts = r.syntheticPipelineStmts

	r2.dirStack = append(r2.dirBootstrap[:0], r.dirStack...)
	r2.fillExpandConfig(r.ectx)
	r2.didReset = true
	return r2
}
