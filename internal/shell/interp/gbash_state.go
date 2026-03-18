package interp

import (
	"fmt"
	"io"

	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/syntax"
)

type GBashConfig struct {
	Stdin                      io.Reader
	Stdout                     io.Writer
	Stderr                     io.Writer
	Params                     []string
	Interactive                bool
	TopLevelScriptPath         string
	SyntheticPipelineSubshells map[*syntax.Stmt]*syntax.Stmt
}

func (r *Runner) setStdIO(in io.Reader, out, err io.Writer) error {
	r.stdin = stdinReader(in)
	if out == nil {
		out = io.Discard
	}
	r.stdout = out
	if err == nil {
		err = io.Discard
	}
	r.stderr = err
	return nil
}

// setParams mirrors the shell's "set" builtin for startup args.
func (r *Runner) setParams(args ...string) error {
	fp := flagParser{remaining: args}
	for fp.more() {
		flag := fp.flag()
		if flag == "+" {
			// Bash treats a lone "+" like an ignored set flag rather than
			// an invalid option or positional argument.
			continue
		}
		if flag == "-" {
			// TODO: implement "The -x and -v options are turned off."
			if args := fp.args(); len(args) > 0 {
				r.Params = args
			}
			return nil
		}
		enable := flag[0] == '-'
		if flag[1] != 'o' {
			opt := r.posixOptByFlag(flag[1])
			if opt == nil {
				return fmt.Errorf("invalid option: %q", flag)
			}
			*opt = enable
			continue
		}
		value := fp.value()
		if value == "" && enable {
			for i, opt := range &posixOptsTable {
				r.printOptLine(opt.name, r.opts[i], true)
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
			return fmt.Errorf("invalid option: %q", value)
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

func (r *Runner) ApplyGBashConfig(cfg *GBashConfig) error {
	if cfg == nil {
		return nil
	}
	if err := r.setStdIO(cfg.Stdin, cfg.Stdout, cfg.Stderr); err != nil {
		return err
	}
	if err := r.setParams(cfg.Params...); err != nil {
		return err
	}
	r.SetInteractiveMode(cfg.Interactive)
	r.SetTopLevelScriptPath(cfg.TopLevelScriptPath)
	r.SetSyntheticPipelineSubshells(cfg.SyntheticPipelineSubshells)
	return nil
}

func (r *Runner) SetInteractiveMode(enabled bool) {
	r.interactive = enabled
	r.opts[optExpandAliases] = enabled
}

func (r *Runner) SetTopLevelScriptPath(scriptPath string) {
	r.topLevelScriptPath = scriptPath
}

func (r *Runner) SetSyntheticPipelineSubshells(nodes map[*syntax.Stmt]*syntax.Stmt) {
	r.syntheticPipelineStmts = nodes
}

func (r *Runner) SetShellVar(name string, vr expand.Variable) error {
	if !r.didReset {
		r.Reset()
	}
	if r.opts[optAllExport] {
		vr.Exported = true
	}
	if err := r.writeEnv.Set(name, vr); err != nil {
		return err
	}
	r.syncVarSnapshot(name, vr)
	return nil
}

func (r *Runner) SetShellString(name, value string) error {
	return r.SetShellVar(name, expand.Variable{
		Set:  true,
		Kind: expand.String,
		Str:  value,
	})
}

func (r *Runner) SetExportedString(name, value string) error {
	return r.SetShellVar(name, expand.Variable{
		Set:      true,
		Kind:     expand.String,
		Str:      value,
		Exported: true,
	})
}

func (r *Runner) UnsetShellVar(name string) error {
	if !r.didReset {
		r.Reset()
	}
	if err := r.writeEnv.Set(name, expand.Variable{}); err != nil {
		return err
	}
	if r.Vars != nil {
		delete(r.Vars, name)
	}
	return nil
}

func (r *Runner) syncVarSnapshot(name string, vr expand.Variable) {
	if r.Vars == nil {
		r.Vars = make(map[string]expand.Variable)
	}
	if !vr.IsSet() {
		delete(r.Vars, name)
		return
	}
	r.Vars[name] = vr
}

func (hc *HandlerContext) SetShellVar(name string, vr expand.Variable) error {
	if hc == nil || hc.runner == nil {
		return nil
	}
	return hc.runner.SetShellVar(name, vr)
}

func (hc *HandlerContext) SetShellString(name, value string) error {
	if hc == nil || hc.runner == nil {
		return nil
	}
	return hc.runner.SetShellString(name, value)
}

func (hc *HandlerContext) SetExportedString(name, value string) error {
	if hc == nil || hc.runner == nil {
		return nil
	}
	return hc.runner.SetExportedString(name, value)
}

func (hc *HandlerContext) UnsetShellVar(name string) error {
	if hc == nil || hc.runner == nil {
		return nil
	}
	return hc.runner.UnsetShellVar(name)
}
