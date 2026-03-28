package interp

import (
	"context"
	"fmt"
	"io"
	"maps"

	"github.com/ewhauser/gbash/internal/shellstate"
	"github.com/ewhauser/gbash/shell/analysis"
	"github.com/ewhauser/gbash/shell/expand"
	"github.com/ewhauser/gbash/shell/syntax"
)

func (r *Runner) setStdIO(in io.Reader, out, err io.Writer) error {
	r.setStdinReader(stdinReader(in, r.pipeFactory))
	r.setStdoutWriter(out)
	r.setStderrWriter(err)
	return nil
}

// setParams mirrors the shell's "set" builtin for startup args.
func (r *Runner) setParams(args ...string) error {
	before := r.analysisOptions()
	defer func() {
		r.analysisOptionChanges(analysis.OptionNamespaceSet, before, r.analysisOptions())
	}()
	fp := flagParser{remaining: args}
	for fp.more() {
		flag := fp.flag()
		if flag == "+" {
			// Bash treats a lone "+" like an ignored set flag rather than
			// an invalid option or positional argument.
			continue
		}
		if flag == "-" {
			if opt := r.posixOptByFlag('v'); opt != nil {
				*opt = false
			}
			if opt := r.posixOptByFlag('x'); opt != nil {
				*opt = false
			}
			if args := fp.args(); len(args) > 0 {
				r.Params = args
				if r.inSource {
					r.sourceSetParams = true
				}
			}
			return nil
		}
		enable := flag[0] == '-'
		if flag[1] != 'o' {
			opt := r.posixOptByFlag(flag[1])
			if opt == nil {
				return fmt.Errorf("%s: invalid option", flag)
			}
			*opt = enable
			continue
		}
		value := fp.value()
		if value == "" && enable {
			for i, opt := range &posixOptsTable {
				r.printSetOptLine(opt.name, r.opts[i])
			}
			continue
		}
		if value == "" && !enable {
			for i, opt := range &posixOptsTable {
				setFlag := "+o"
				if r.opts[i] {
					setFlag = "-o"
				}
				r.outf("set %s %s\n", setFlag, opt.name)
			}
			continue
		}
		opt := r.posixOptByName(value)
		if opt == nil {
			return fmt.Errorf("%s: invalid option name", value)
		}
		*opt = enable
	}
	if args := fp.args(); args != nil {
		// If "--" wasn't given and there were zero arguments,
		// we don't want to override the current parameters.
		r.Params = args

		// Record whether a sourced script sets the parameters.
		if r.inSource {
			r.sourceSetParams = true
		}
	}
	return nil
}

func (r *Runner) setInteractive(enabled bool) {
	r.interactive = enabled
	r.opts[optExpandAliases] = enabled
}

func (r *Runner) RunWithMetadata(ctx context.Context, node syntax.Node, topLevelScriptPath string, syntheticPipelineSubshells map[*syntax.Stmt]*syntax.Stmt) error {
	prevTopLevelScriptPath := r.topLevelScriptPath
	prevSyntheticPipelineStmts := r.syntheticPipelineStmts
	r.topLevelScriptPath = topLevelScriptPath
	r.syntheticPipelineStmts = syntheticPipelineSubshells
	defer func() {
		r.topLevelScriptPath = prevTopLevelScriptPath
		r.syntheticPipelineStmts = prevSyntheticPipelineStmts
	}()
	return r.Run(ctx, node)
}

func (r *Runner) NewParser(opts ...syntax.ParserOption) *syntax.Parser {
	return r.newParser(opts...)
}

func (r *Runner) SetShellVar(name string, vr expand.Variable) error {
	if !r.didReset {
		r.Reset()
	}
	return r.setVarObserved(name, vr, nil)
}

func (r *Runner) UnsetShellVar(name string) error {
	if !r.didReset {
		r.Reset()
	}
	return r.setVarObserved(name, expand.Variable{}, nil)
}

func (r *Runner) ShellEnv() map[string]string {
	if !r.didReset || r.writeEnv == nil {
		return nil
	}
	out := r.materializedShellEnv()
	if len(out) == 0 {
		return nil
	}
	return maps.Clone(out)
}

func (r *Runner) materializedShellEnv() map[string]string {
	cacheEnv, cacheEpoch, cacheable := r.currentWriteEnvCacheToken()
	if cacheable && r.shellEnvCacheReady && r.shellEnvCacheEnv == cacheEnv && r.shellEnvCacheEpoch == cacheEpoch {
		return r.shellEnvCache
	}

	out := make(map[string]string)
	for name, vr := range r.writeEnv.Each() {
		if !vr.IsSet() {
			delete(out, name)
			continue
		}
		out[name] = vr.String()
	}
	if len(out) == 0 {
		out = nil
	}
	if cacheable {
		r.shellEnvCache = out
		r.shellEnvCacheEnv = cacheEnv
		r.shellEnvCacheEpoch = cacheEpoch
		r.shellEnvCacheReady = true
	}
	return out
}

func (r *Runner) ShellVarString(name string) (string, bool) {
	if !r.didReset || r.writeEnv == nil {
		return "", false
	}
	vr := r.writeEnv.Get(name)
	if !vr.IsSet() {
		return "", false
	}
	return vr.String(), true
}

func (hc *HandlerContext) SetShellVar(name string, vr expand.Variable) error {
	if hc == nil || hc.runner == nil {
		return nil
	}
	return hc.runner.SetShellVar(name, vr)
}

func (hc *HandlerContext) UnsetShellVar(name string) error {
	if hc == nil || hc.runner == nil {
		return nil
	}
	return hc.runner.UnsetShellVar(name)
}

func (hc HandlerContext) DispatchSignal(target string, number int) error {
	if hc.runner == nil {
		return nil
	}
	return hc.runner.DispatchSignal(target, number)
}

func (hc HandlerContext) SignalFamily() shellstate.SignalFamily {
	if hc.runner == nil {
		return shellstate.SignalFamily{}
	}
	owner := hc.runner
	if owner.signalOwner != nil {
		owner = owner.signalOwner
	}
	return shellstate.SignalFamily{
		Owner:         owner,
		StablePID:     owner.pid,
		ParentBASHPID: hc.runner.bashPID,
	}
}

func (hc HandlerContext) ProcessGroup() (int, bool) {
	if hc.runner == nil {
		return 0, false
	}
	return shellstate.ProcessGroupFromContext(hc.runner.ectx)
}
