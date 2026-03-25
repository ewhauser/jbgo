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
	stdfs "io/fs"
	"maps"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ewhauser/gbash/host"
	"github.com/ewhauser/gbash/internal/commandutil"
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
	// shellEnvCache stores the last materialized shell-visible environment for
	// the current writeEnv/epoch pair.
	shellEnvCache map[string]string
	// shellEnvCacheEnv identifies the writeEnv used to build shellEnvCache.
	shellEnvCacheEnv expand.WriteEnviron
	// shellEnvCacheEpoch identifies the overlay epoch used to build shellEnvCache.
	shellEnvCacheEpoch uint64
	// shellEnvCacheReady reports whether shellEnvCache is valid.
	shellEnvCacheReady bool
	// setVarsCache stores sorted set output for the current writeEnv/epoch pair.
	setVarsCache []namedVariable
	// setVarsCacheEnv identifies the writeEnv used to build setVarsCache.
	setVarsCacheEnv expand.WriteEnviron
	// setVarsCacheEpoch identifies the overlay epoch used to build setVarsCache.
	setVarsCacheEpoch uint64
	// setVarsCacheHasBashLineNo tracks whether writeEnv already provided
	// BASH_LINENO while building setVarsCache.
	setVarsCacheHasBashLineNo bool
	// setVarsCacheReady reports whether setVarsCache is valid.
	setVarsCacheReady bool

	// Dir specifies the working directory of the command, which must be an
	// absolute path.
	Dir string

	// logicalDir is the shell's internal logical cwd. Unlike the exported PWD
	// variable, it is not mutated by ordinary shell variable assignments.
	logicalDir string

	// tempDir is either $TMPDIR from [Runner.Env] or a deterministic virtual
	// default.
	tempDir string

	platform host.Platform

	pipeFactory func() (io.ReadCloser, io.WriteCloser, error)
	timeNow     func() time.Time

	// Params are the current shell parameters, e.g. from running a shell
	// file or calling a function. Accessible via the $@/$* family of vars.
	Params []string

	// Separate maps - note that bash allows a name to be both a var and a
	// func simultaneously.
	funcs map[string]funcInfo
	// funcsShared tracks whether funcs is shared with another runner and must
	// be cloned before mutation.
	funcsShared bool

	alias map[string]alias
	// aliasShared tracks whether alias is shared with another runner and must
	// be cloned before mutation.
	aliasShared bool

	commandHash map[string]commandHashEntry
	// commandHashShared tracks whether commandHash is shared with another
	// runner and must be cloned before mutation.
	commandHashShared bool

	disabledBuiltins map[string]bool
	// disabledBuiltinsShared tracks whether disabledBuiltins is shared with
	// another runner and must be cloned before mutation.
	disabledBuiltinsShared bool

	// readonly -a/-A on a new unset variable preserves array semantics while bash
	// still omits the array type from `declare -p` output.
	hiddenReadonlyArrayDecl map[string]expand.ValueKind
	// hiddenReadonlyArrayDeclShared tracks whether hiddenReadonlyArrayDecl is
	// shared with another runner and must be cloned before mutation.
	hiddenReadonlyArrayDeclShared bool

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

	// bashPID is the current shell's PID-like identity. Unlike $$, BASHPID
	// changes for subshells and command substitutions.
	bashPID int
	ppid    int

	// nextVirtualPID is shared by a shell family so BASHPID changes across
	// subshells while $$ remains stable.
	nextVirtualPID *virtualPIDState

	stdin  StdinReader // e.g. the read end of a pipe
	stdout io.Writer
	stderr io.Writer
	fds    map[int]*shellFD
	// fdsShared tracks whether fds is shared with another runner and must be
	// cloned before mutation.
	fdsShared bool

	// namedFDReleased tracks named redirect variables whose descriptor was
	// explicitly closed with a persistent redirect such as `exec {fd}>&-`.
	namedFDReleased map[string]bool
	// namedFDReleasedShared tracks whether namedFDReleased is shared with
	// another runner and must be cloned before mutation.
	namedFDReleasedShared bool

	// traceOutput keeps xtrace on the shell's stderr even when a statement
	// temporarily redirects fd 2.
	traceOutput io.Writer

	// expandBaseFDs are the file descriptors visible while expanding the
	// current statement's words, before the statement's own redirects apply.
	expandBaseFDs map[int]*shellFD

	ecfg     expand.Config
	ecfgInit bool
	ectx     context.Context // just so that Runner.Subshell can use it again

	legacyBashCompat bool
	inSubshell       bool

	// didReset remembers whether the runner has ever been reset. This is
	// used so that Reset is automatically called when running any program
	// or node for the first time on a Runner.
	didReset bool

	filename string // only if Node was a File

	topLevelScriptPath string
	frames             []execFrame
	// framesShared tracks whether frames is shared with another runner and must
	// be cloned before mutation.
	framesShared bool
	internalRun  bool

	// >0 to break or continue out of N enclosing loops
	breakEnclosing, contnEnclosing int

	loopDepth int
	inFunc    bool
	inSource  bool
	evalDepth int

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

	lastExpandExit  exitStatus // used to surface exit statuses while expanding fields
	lastStmtLine    uint
	currentStmtLine uint
	stmtDepth       int
	skipStmtLine    uint
	commandAborted  bool
	pipeStatuses    []string
	pipeStatusSet   bool
	// pipelineErrTrapDepth defers ERR handling to an enclosing pipeline until
	// the final pipeline status and PIPESTATUS array have been established.
	pipelineErrTrapDepth int
	// pipelineDebugSkips suppresses parent-side DEBUG re-entry for pipeline
	// nodes whose segment traps were already fired by an enclosing pipeline.
	pipelineDebugSkips map[*syntax.BinaryCmd]int
	// pipelineSegmentDebugSkips suppresses the normal top-level DEBUG trap for
	// segment commands after the pipeline has already fired parent-side DEBUG.
	pipelineSegmentDebugSkips map[syntax.Command]int
	// suppressTopLevelErrTrap skips the synthetic top-level ERR trap that bash
	// does not run for asynchronous statements like "false & wait".
	suppressTopLevelErrTrap bool

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
	// Most statements need at most one active FD snapshot, so keep a small
	// inline stack to avoid per-runner heap churn on the common path.
	fdSnapshotBootstrap [4]map[int]*shellFD
	origStart           time.Time
	// origShellStart is the shell-visible start timestamp used by printf %T -2.
	origShellStart time.Time
	origRandom     uint32

	startTime      time.Time
	shellStartTime time.Time
	random         uint32

	inRedirectWord int
	inAssignment   int

	// Most scripts don't use pushd/popd, so make space for the initial visible
	// cwd without requiring an extra allocation.
	dirStack     []string
	dirBootstrap [1]string
	// dirStackShared tracks whether dirStack is shared with another runner and
	// must be cloned before mutation.
	dirStackShared bool

	optState getopts

	interactive            bool
	commandString          bool
	commandStringValue     string
	syntheticPipelineStmts map[*syntax.Stmt]*syntax.Stmt

	// keepRedirs is used so that "exec" can make any redirections
	// apply to the current shell, and not just the command.
	keepRedirs bool

	// printfEnv keeps process-level environment state used by printf %T and is
	// shared across subshells in the same shell family.
	printfEnv *printfEnvCache

	traps trapState

	trapLineOverride uint
	signalOwner      *Runner
	// signalChildren is an owner-wide registry of inherited shell runners keyed
	// by their bash PID so signal dispatch can inspect direct child shells.
	signalChildren      *sync.Map
	signalParentBASHPID int
}

type commandHashEntry struct {
	path string
	hits int
}

type funcSourceSpan struct {
	text string
	base uint
}

type funcInfo struct {
	body             *syntax.Stmt
	definitionSource string
	bodySource       funcSourceSpan
	hasBodySource    bool
	internal         bool
	trace            bool
}

type trapState struct {
	actions             map[trapID]trapAction
	display             map[trapID]trapAction
	active              map[trapID]int
	pending             []pendingSignalTrap
	currentSignalNumber int
	generation          uint64 // bumped on every setTrapAction call
}

type pendingSignalTrap struct {
	id     trapID
	status uint8
	line   uint
}

type virtualPIDState struct {
	next atomic.Int64
}

func (r *Runner) commandHashLookup(name string) (commandHashEntry, bool) {
	if r == nil || r.commandHash == nil {
		return commandHashEntry{}, false
	}
	entry, ok := r.commandHash[name]
	return entry, ok
}

func (r *Runner) commandHashRemember(name, path string) {
	if r == nil || name == "" || path == "" {
		return
	}
	r.ensureOwnCommandHash()
	r.commandHash[name] = commandHashEntry{path: path}
}

func (r *Runner) commandHashIncrement(name string) {
	if r == nil || r.commandHash == nil {
		return
	}
	r.commandHash = cloneMapOnWrite(r.commandHash, &r.commandHashShared)
	entry, ok := r.commandHash[name]
	if !ok {
		return
	}
	entry.hits++
	r.commandHash[name] = entry
}

func (r *Runner) commandHashClear() {
	if r == nil || r.commandHash == nil {
		return
	}
	r.clearCommandHash()
}

func (r *Runner) commandHashEntries() []commandHashEntry {
	if r == nil || len(r.commandHash) == 0 {
		return nil
	}
	entries := make([]commandHashEntry, 0, len(r.commandHash))
	for _, entry := range r.commandHash {
		entries = append(entries, entry)
	}
	return entries
}

func newVirtualPIDState(next int) *virtualPIDState {
	state := &virtualPIDState{}
	state.next.Store(int64(next))
	return state
}

func (s *virtualPIDState) allocate() int {
	if s == nil {
		return defaultVirtualPID
	}
	return int(s.next.Add(1) - 1)
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

	// errExitIgnored tracks whether this non-zero status originated from a
	// context where errexit is ignored, such as an if-condition or the left
	// side of &&/||. Compound commands can then return that status without
	// re-triggering errexit on the enclosing statement.
	errExitIgnored bool

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
	e.errExitIgnored = false
}

func (e *exitStatus) ok() bool { return e.code == 0 }

// oneIf sets the exit status code to 1 if b is true.
// Note that it assumes the exit status hasn't been set yet,
// meaning that [exitStatus.code] and [exitStatus.err] are zero values.
func (e *exitStatus) oneIf(b bool) {
	if b {
		e.code = 1
		e.errExitIgnored = false
	}
}

func (e *exitStatus) fatal(err error) {
	if e.fatalExit || err == nil {
		return
	}
	e.exiting = true
	e.fatalExit = true
	e.err = err
	e.errExitIgnored = false
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

	runner        *Runner
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
	defaultVirtualPath    = "/usr/bin:/bin"
	defaultVirtualShell   = "/bin/sh"
	defaultVirtualID      = 1000
	defaultVirtualPID     = 1
	defaultVirtualPPID    = 0
)

// RunnerConfig defines the runtime boundary for a Runner.
type RunnerConfig struct {
	Env expand.Environ

	Platform host.Platform

	PID  int
	PPID int

	// StartupHome carries the shell's trusted startup home for callers that
	// need startup-sensitive tilde semantics.
	StartupHome string

	// Dir is the authoritative virtual current directory.
	Dir string

	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Params []string
	Now    func() time.Time
	// ShellStartTime is the shell-visible wall clock used for printf %T -2.
	ShellStartTime time.Time

	Interactive        bool
	CommandString      bool
	CommandStringValue string

	LegacyBashCompat bool

	CallHandler      CallHandlerFunc
	ExecHandler      ExecHandlerFunc
	OpenHandler      OpenHandlerFunc
	ReadDirHandler   ReadDirHandlerFunc
	StatHandler      StatHandlerFunc
	RealpathHandler  RealpathHandlerFunc
	ProcSubstHandler ProcSubstHandlerFunc
	NewPipe          func() (io.ReadCloser, io.WriteCloser, error)
}

func newRunnerBase() *Runner {
	r := &Runner{}
	r.dirStack = r.dirBootstrap[:0]
	r.opts[optBraceExpand] = true // braceexpand is on by default in bash
	// turn "on" the default Bash options
	for i, opt := range &bashOptsTable {
		r.opts[len(posixOptsTable)+i] = opt.defaultState
	}
	return r
}

func (r *Runner) applyConstructorDefaults() error {
	if r.Env == nil {
		r.Env = expand.ListEnvironWithCase(r.platform.UsesCaseInsensitiveEnv())
	}
	if r.startupHome == "" {
		if home := r.Env.Get("HOME").String(); path.IsAbs(home) {
			r.startupHome = path.Clean(home)
		}
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
	r.applyDisabledBuiltinsEnv()
	r.normalizeVirtualState()
	return nil
}

func (r *Runner) normalizeVirtualState() {
	r.uid = firstVirtualInt(r.uid, r.Env, "UID", defaultVirtualID)
	r.euid = firstVirtualInt(r.euid, r.Env, "EUID", r.uid)
	r.gid = firstVirtualInt(r.gid, r.Env, "GID", defaultVirtualID)
	r.egid = firstVirtualInt(r.egid, r.Env, "EGID", r.gid)
	r.pid = firstVirtualInt(r.pid, nil, "", defaultVirtualPID)
	r.bashPID = firstVirtualInt(r.bashPID, nil, "", r.pid)
	r.ppid = firstVirtualInt(r.ppid, nil, "", defaultVirtualPPID)
	if r.nextVirtualPID == nil {
		r.nextVirtualPID = newVirtualPIDState(max(r.pid, r.bashPID) + 1)
	}
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
	r.platform = normalizePlatform(cfg.Platform)
	r.startupHome = cfg.StartupHome
	r.Dir = cfg.Dir
	r.timeNow = cfg.Now
	r.shellStartTime = cfg.ShellStartTime
	r.callHandler = cfg.CallHandler
	r.execHandler = cfg.ExecHandler
	r.openHandler = cfg.OpenHandler
	r.readDirHandler = cfg.ReadDirHandler
	r.statHandler = cfg.StatHandler
	r.realpathHandler = cfg.RealpathHandler
	r.procSubstHandler = cfg.ProcSubstHandler
	r.pipeFactory = cfg.NewPipe
	r.pid = cfg.PID
	r.ppid = cfg.PPID
	r.legacyBashCompat = cfg.LegacyBashCompat
	r.commandString = cfg.CommandString
	r.commandStringValue = cfg.CommandStringValue
	if err := r.setStdIO(cfg.Stdin, cfg.Stdout, cfg.Stderr); err != nil {
		return nil, err
	}
	if err := r.setParams(cfg.Params...); err != nil {
		return nil, err
	}
	r.setInteractive(cfg.Interactive)
	return r, r.applyConstructorDefaults()
}

func (r *Runner) now() time.Time {
	if r != nil && r.timeNow != nil {
		return r.timeNow()
	}
	return time.Now()
}

func stdinReader(r io.Reader, pipeFactory func() (io.ReadCloser, io.WriteCloser, error)) StdinReader {
	var redirectMeta redirectedStdinMetadata
	if meta, ok := r.(commandutil.RedirectMetadata); ok {
		redirectMeta = snapshotRedirectedStdinMetadata(meta)
	}
	switch r := r.(type) {
	case StdinReader:
		return wrapRedirectedStdinReader(r, r, redirectMeta)
	case nil:
		return nil
	default:
		pr, pw, err := newPipe(pipeFactory)
		if err != nil {
			pr, pw = NewVirtualPipe()
		}
		go func() {
			io.Copy(pw, r)
			pw.Close()
		}()
		return wrapRedirectedStdinReader(pr, r, redirectMeta)
	}
}

type redirectedStdinMetadata struct {
	path   string
	flags  int
	offset int64
}

func snapshotRedirectedStdinMetadata(meta commandutil.RedirectMetadata) redirectedStdinMetadata {
	if meta == nil {
		return redirectedStdinMetadata{}
	}
	return redirectedStdinMetadata{
		path:   meta.RedirectPath(),
		flags:  meta.RedirectFlags(),
		offset: meta.RedirectOffset(),
	}
}

type redirectedStdinReader struct {
	StdinReader
	handle io.Reader
	path   string
	flags  int
	offset int64
}

func wrapRedirectedStdinReader(reader StdinReader, underlying io.Reader, meta redirectedStdinMetadata) StdinReader {
	if reader == nil || meta.path == "" {
		return reader
	}
	if underlying == nil {
		underlying = reader
	}
	return redirectedStdinReader{
		StdinReader: reader,
		handle:      underlying,
		path:        meta.path,
		flags:       meta.flags,
		offset:      meta.offset,
	}
}

func (r redirectedStdinReader) RedirectPath() string {
	return r.path
}

func (r redirectedStdinReader) RedirectFlags() int {
	return r.flags
}

func (r redirectedStdinReader) RedirectOffset() int64 {
	return r.offset
}

func (r redirectedStdinReader) Stat() (stdfs.FileInfo, error) {
	type statter interface {
		Stat() (stdfs.FileInfo, error)
	}
	if statter, ok := r.handle.(statter); ok {
		return statter.Stat()
	}
	return nil, errors.New("bad file descriptor")
}

func (r redirectedStdinReader) Seek(offset int64, whence int) (int64, error) {
	type seeker interface {
		Seek(offset int64, whence int) (int64, error)
	}
	if seeker, ok := r.handle.(seeker); ok {
		return seeker.Seek(offset, whence)
	}
	return 0, errors.New("bad file descriptor")
}

func (r redirectedStdinReader) Fd() uintptr {
	type fileDescriber interface {
		Fd() uintptr
	}
	if file, ok := r.handle.(fileDescriber); ok {
		return file.Fd()
	}
	return 0
}

func newPipe(pipeFactory func() (io.ReadCloser, io.WriteCloser, error)) (StdinReader, io.WriteCloser, error) {
	if pipeFactory == nil {
		pr, pw := NewVirtualPipe()
		return pr, pw, nil
	}
	reader, writer, err := pipeFactory()
	if err != nil {
		return nil, nil, err
	}
	if reader == nil || writer == nil {
		return nil, nil, fmt.Errorf("pipe factory returned nil endpoint")
	}
	if stdin, ok := reader.(StdinReader); ok {
		return stdin, writer, nil
	}
	return nopDeadlineReader{ReadCloser: reader}, writer, nil
}

type nopDeadlineReader struct {
	io.ReadCloser
}

func (nopDeadlineReader) SetReadDeadline(time.Time) error { return nil }

func normalizePlatform(platform host.Platform) host.Platform {
	if platform.OS == "" {
		platform.OS = host.OSLinux
	}
	defaults := platform.OS.PlatformDefaults()
	if platform.OSType == "" {
		platform.OSType = defaults.OSType
	}
	if platform.EnvCaseInsensitive == nil {
		value := defaults.EnvCaseInsensitive
		platform.EnvCaseInsensitive = &value
	}
	if platform.PathExtensions == nil {
		platform.PathExtensions = append([]string(nil), defaults.PathExtensions...)
	}
	if platform.RequireExecutableBit == nil {
		value := defaults.RequireExecutableBit
		platform.RequireExecutableBit = &value
	}
	return platform
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
	{'B', "braceexpand"},
	{'E', "errtrace"},
	{'e', "errexit"},
	{'T', "functrace"},
	{'C', "noclobber"},
	{'n', "noexec"},
	{'f', "noglob"},
	{'u', "nounset"},
	{' ', "pipefail"},
	{' ', "posix"},
	{'v', "verbose"},
	{'x', "xtrace"},
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
		name:         "failglob",
		defaultState: false,
		supported:    true,
	},
	{
		name:         "globskipdots",
		defaultState: true,
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
		name:         "nocasematch",
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
	// These correspond to indexes in [posixOptsTable]
	optAllExport = iota
	optBraceExpand
	optErrTrace
	optErrExit
	optFuncTrace
	optNoClobber
	optNoExec
	optNoGlob
	optNoUnset
	optPipeFail
	optPosix
	optVerbose
	optXTrace

	// These correspond to indexes (offset by the POSIX options above) of
	// supported options in [bashOptsTable]
	optDotGlob
	optExpandAliases
	optExtDebug
	optExtGlob
	optFailGlob
	optGlobSkipDots
	optGlobStar
	optLastPipe
	optNoCaseGlob
	optNoCaseMatch
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
		if r.shellStartTime.IsZero() {
			r.shellStartTime = r.now()
		}
		r.origShellStart = r.shellStartTime
		r.origRandom = randomSeed(r.bashPID, r.origStart)

		if r.execHandler == nil {
			r.execHandler = closedExecHandler()
		}
	}
	funcs := r.funcs
	funcsShared := r.funcsShared
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
		timeNow:          r.timeNow,
		uid:              r.uid,
		euid:             r.euid,
		gid:              r.gid,
		egid:             r.egid,
		pid:              r.pid,
		bashPID:          r.bashPID,
		ppid:             r.ppid,
		startupHome:      r.startupHome,

		// These can be set by functions like [Dir] or [Params], but
		// builtins can overwrite them; reset the fields to whatever the
		// constructor set up.
		Dir:                r.origDir,
		Params:             r.origParams,
		opts:               r.origOpts,
		stdin:              r.origStdin,
		stdout:             r.origStdout,
		stderr:             r.origStderr,
		fds:                cloneFDTable(r.origFDs),
		legacyBashCompat:   r.legacyBashCompat,
		commandString:      r.commandString,
		commandStringValue: r.commandStringValue,

		origDir:        r.origDir,
		origParams:     r.origParams,
		origOpts:       r.origOpts,
		origStdin:      r.origStdin,
		origStdout:     r.origStdout,
		origStderr:     r.origStderr,
		origFDs:        cloneFDTable(r.origFDs),
		origStart:      r.origStart,
		origShellStart: r.origShellStart,
		origRandom:     r.origRandom,
		startTime:      r.origStart,
		shellStartTime: r.origShellStart,
		random:         r.origRandom,

		funcs:     funcs,
		printfEnv: newPrintfEnvCache(),

		topLevelScriptPath:     r.topLevelScriptPath,
		interactive:            r.interactive,
		syntheticPipelineStmts: r.syntheticPipelineStmts,
		signalOwner:            r.signalOwner,
		signalChildren:         r.signalChildren,
		signalParentBASHPID:    r.signalParentBASHPID,
	}
	r.traps.actions = nil
	r.traps.display = nil
	r.traps.active = nil
	r.traps.pending = nil
	r.traps.currentSignalNumber = 0
	r.trapLineOverride = 0
	if r.signalOwner == nil {
		r.signalOwner = r
	}
	if r.commandHash == nil {
		r.commandHash = make(map[string]commandHashEntry)
	} else {
		clear(r.commandHash)
	}
	if r.signalChildren == nil {
		r.signalChildren = &sync.Map{}
	}
	r.nextVirtualPID = newVirtualPIDState(max(r.pid, r.bashPID) + 1)
	// Ensure we stop referencing any pointers before we reuse bgProcs.
	clear(r.bgProcs)
	r.bgProcs = r.bgProcs[:0]

	if r.funcs == nil {
		r.funcs = make(map[string]funcInfo, 4)
	} else if funcsShared {
		r.funcs = make(map[string]funcInfo, 4)
	} else {
		clear(r.funcs)
	}
	r.funcsShared = false
	r.dirStack = r.dirBootstrap[:0]
	r.dirStackShared = false
	// TODO(v4): Use the supplied Env directly if it implements enough methods.
	r.writeEnv = newScopedOverlayEnviron(r.Env, r.platform.UsesCaseInsensitiveEnv())
	r.applyDisabledBuiltinsEnv()
	r.delVar(disabledBuiltinsEnvVar)
	if !r.writeEnv.Get("TMPDIR").IsSet() {
		r.setVarString("TMPDIR", r.tempDir)
	}
	if !r.writeEnv.Get("PATH").IsSet() {
		r.setVarString("PATH", defaultVirtualPath)
	}
	if !r.writeEnv.Get("SHELL").IsSet() {
		r.setVarString("SHELL", defaultVirtualShell)
	}
	if !r.writeEnv.Get("HOSTNAME").Declared() {
		r.setVarString("HOSTNAME", r.defaultHostname())
	}
	if !r.writeEnv.Get("OSTYPE").Declared() {
		r.setVarString("OSTYPE", r.defaultOSType())
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
	if r.commandStringValue != "" {
		r.setVar("BASH_EXECUTION_STRING", expand.Variable{
			Set:  true,
			Kind: expand.String,
			Str:  r.commandStringValue,
		})
	}
	pwd := r.Dir
	if candidate := r.writeEnv.Get("PWD").String(); r.pwdCandidateMatchesDir(candidate) {
		pwd = candidate
	}
	r.logicalDir = pwd
	r.setExportedVarString("PWD", pwd)
	r.setVarString("_", "")
	r.setVarString("IFS", " \t\n")
	r.setOPTIND("1")
	r.setVarString("PS4", "+ ")
	if r.interactive && !r.writeEnv.Get("HISTFILE").IsSet() {
		home := strings.TrimSpace(r.writeEnv.Get("HOME").String())
		if home == "" {
			home = defaultVirtualHomeDir
		}
		r.setVarString("HISTFILE", path.Join(home, ".bash_history"))
	}

	// When a parent shell exports SHELLOPTS, apply the inherited
	// options so that the child mirrors the parent's set -o state.
	// Only enable options present in the value; bash does not clear
	// defaults when importing SHELLOPTS (e.g. braceexpand stays on).
	if shellOpts := r.writeEnv.Get("SHELLOPTS"); shellOpts.IsSet() && shellOpts.Exported {
		for _, optName := range strings.Split(shellOpts.String(), ":") {
			if opt := r.posixOptByName(optName); opt != nil {
				*opt = true
			}
		}
	}

	// Similarly, when a parent shell exports BASHOPTS, apply the
	// inherited shopt options so the child mirrors the parent's state.
	if bashOpts := r.writeEnv.Get("BASHOPTS"); bashOpts.IsSet() && bashOpts.Exported {
		for _, optName := range strings.Split(bashOpts.String(), ":") {
			if opt, _ := r.bashOptByName(optName); opt != nil {
				*opt = true
			}
		}
	}

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
	if r.interactive && !r.commandString {
		defer func() {
			r.skipStmtLine = 0
		}()
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
		r.runTrap(ctx, trapIDExit, r.currentStmtLine, r.exit.code)
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
func (r *Runner) subshell(_ bool) *Runner {
	if !r.didReset {
		r.Reset()
	}
	bashPID := r.allocateSubshellPID()
	random := randomSeed(bashPID, r.origStart)
	// Keep in sync with the Runner type. Manually copy fields to avoid copying
	// sensitive ones like [errgroup.Group], while sharing large shell state via
	// copy-on-write.
	r2 := &Runner{
		Env:                       r.Env,
		Dir:                       r.Dir,
		logicalDir:                r.logicalDir,
		tempDir:                   r.tempDir,
		platform:                  r.platform,
		pipeFactory:               r.pipeFactory,
		Params:                    r.Params,
		callHandler:               r.callHandler,
		execHandler:               r.execHandler,
		openHandler:               r.openHandler,
		readDirHandler:            r.readDirHandler,
		statHandler:               r.statHandler,
		realpathHandler:           r.realpathHandler,
		procSubstHandler:          r.procSubstHandler,
		uid:                       r.uid,
		euid:                      r.euid,
		gid:                       r.gid,
		egid:                      r.egid,
		pid:                       r.pid,
		bashPID:                   bashPID,
		ppid:                      r.ppid,
		nextVirtualPID:            r.nextVirtualPID,
		stdin:                     r.stdin,
		stdout:                    r.stdout,
		stderr:                    r.stderr,
		fds:                       r.fds,
		namedFDReleased:           r.namedFDReleased,
		traceOutput:               r.traceOutput,
		expandBaseFDs:             r.expandBaseFDs,
		filename:                  r.filename,
		topLevelScriptPath:        r.topLevelScriptPath,
		internalRun:               r.internalRun,
		opts:                      r.opts,
		interactive:               r.interactive,
		commandString:             r.commandString,
		commandStringValue:        r.commandStringValue,
		legacyBashCompat:          r.legacyBashCompat,
		noErrExit:                 r.noErrExit,
		inSubshell:                true,
		inFunc:                    r.inFunc,
		inSource:                  r.inSource,
		exit:                      r.exit,
		lastExit:                  r.lastExit,
		pipeStatuses:              append([]string(nil), r.pipeStatuses...),
		pipelineErrTrapDepth:      r.pipelineErrTrapDepth,
		pipelineDebugSkips:        maps.Clone(r.pipelineDebugSkips),
		pipelineSegmentDebugSkips: maps.Clone(r.pipelineSegmentDebugSkips),
		suppressXTrace:            r.suppressXTrace,
		currentChunkSource:        r.currentChunkSource,
		currentChunkSourceBase:    r.currentChunkSourceBase,
		printfEnv:                 r.printfEnv,
		hiddenReadonlyArrayDecl:   r.hiddenReadonlyArrayDecl,
		commandHash:               r.commandHash,
		disabledBuiltins:          r.disabledBuiltins,
		startupHome:               r.startupHome,
		origStart:                 r.origStart,
		origShellStart:            r.origShellStart,
		startTime:                 r.startTime,
		shellStartTime:            r.shellStartTime,
		timeNow:                   r.timeNow,
		random:                    random,
		origRandom:                random,
		signalOwner:               r.signalOwner,
		signalChildren:            r.signalChildren,
		signalParentBASHPID:       r.signalParentBASHPID,

		origStdout: r.origStdout, // used for process substitutions
	}
	if shareMapForSubshell(r.fds, &r.fdsShared) {
		r2.fdsShared = true
	}
	if shareMapForSubshell(r.namedFDReleased, &r.namedFDReleasedShared) {
		r2.namedFDReleasedShared = true
	}
	if shareMapForSubshell(r.hiddenReadonlyArrayDecl, &r.hiddenReadonlyArrayDeclShared) {
		r2.hiddenReadonlyArrayDeclShared = true
	}
	if shareMapForSubshell(r.commandHash, &r.commandHashShared) {
		r2.commandHashShared = true
	}
	if shareMapForSubshell(r.disabledBuiltins, &r.disabledBuiltinsShared) {
		r2.disabledBuiltinsShared = true
	}
	r2.writeEnv = newOverlayEnviron(r.writeEnv, true, r.platform.UsesCaseInsensitiveEnv())
	r2.funcs = r.funcs
	if shareMapForSubshell(r.funcs, &r.funcsShared) {
		r2.funcsShared = true
	}
	r2.alias = r.alias
	if shareMapForSubshell(r.alias, &r.aliasShared) {
		r2.aliasShared = true
	}
	r2.frames = r.frames
	if shareSliceForSubshell(r.frames, &r.framesShared) {
		r2.framesShared = true
	}
	r2.syntheticPipelineStmts = r.syntheticPipelineStmts
	r2.cloneTrapStateFrom(r)

	r2.dirStack = r.dirStack
	if shareSliceForSubshell(r.dirStack, &r.dirStackShared) {
		r2.dirStackShared = true
	}
	r2.fillExpandConfig(r.ectx)
	r2.didReset = true
	return r2
}

func (r *Runner) allocateSubshellPID() int {
	if r == nil {
		return defaultVirtualPID
	}
	if r.nextVirtualPID == nil {
		r.nextVirtualPID = newVirtualPIDState(max(r.pid, r.bashPID) + 1)
	}
	return r.nextVirtualPID.allocate()
}

func (r *Runner) InheritSignalFamily(owner *Runner, stablePID, parentBASHPID int) {
	if r == nil || owner == nil {
		return
	}
	if owner.signalOwner != nil {
		owner = owner.signalOwner
	}
	if owner.signalChildren == nil {
		owner.signalChildren = &sync.Map{}
	}
	if stablePID <= 0 {
		stablePID = owner.pid
	}
	if parentBASHPID <= 0 {
		parentBASHPID = owner.bashPID
	}
	r.signalOwner = owner
	r.signalChildren = owner.signalChildren
	r.signalParentBASHPID = parentBASHPID
	r.pid = stablePID
	r.ppid = parentBASHPID
	r.nextVirtualPID = owner.nextVirtualPID
	r.bashPID = owner.allocateSubshellPID()
	owner.signalChildren.Store(r.bashPID, r)
}

func (r *Runner) signalChildTrapTarget(id trapID) *Runner {
	if r == nil {
		return nil
	}
	owner := r
	if owner.signalOwner != nil {
		owner = owner.signalOwner
	}
	if owner.signalChildren != nil {
		var target *Runner
		owner.signalChildren.Range(func(_, child any) bool {
			runner, ok := child.(*Runner)
			if !ok || runner == nil || runner.signalParentBASHPID != r.bashPID {
				return true
			}
			if action := runner.trapAction(id); action.active() {
				target = runner
				return false
			}
			return true
		})
		if target != nil {
			return target
		}
	}
	return nil
}

func randomSeed(pid int, started time.Time) uint32 {
	seed := uint32(pid)
	if !started.IsZero() {
		nanos := started.UnixNano()
		seed ^= uint32(nanos)
		seed ^= uint32(nanos >> 32)
	}
	if seed == 0 {
		return 1
	}
	return seed
}
