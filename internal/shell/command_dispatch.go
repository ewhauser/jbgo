package shell

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ewhauser/gbash/commands"
	"github.com/ewhauser/gbash/internal/shell/interp"
	"github.com/ewhauser/gbash/policy"
	"github.com/ewhauser/gbash/shell/expand"
	"github.com/ewhauser/gbash/trace"
)

type commandExecuteRequest struct {
	Argv          []string
	CommandPath   string
	CommandName   string
	VirtualWD     string
	Env           expand.Environ
	CurrentEnv    map[string]string
	Stdin         io.Reader
	Stdout        io.Writer
	Stderr        io.Writer
	Position      string
	Internal      bool
	FromBootstrap bool
	PrepareInvoke func(context.Context) context.Context
	SyncEnv       func(context.Context, map[string]string, map[string]string) error
}

func (m *core) newRunner(ctx context.Context, exec *Execution, budget *executionBudget) (*interp.Runner, error) {
	cfg := m.runnerConfig(exec, budget)
	interp.InheritNestedShellState(ctx, cfg)
	return interp.NewRunner(cfg)
}

func (m *core) executeCommand(ctx context.Context, exec *Execution, req *commandExecuteRequest) (map[string]string, error) {
	if req == nil {
		req = &commandExecuteRequest{}
	}
	if len(req.Argv) == 0 {
		return req.CurrentEnv, nil
	}

	var (
		resolved *resolvedCommand
		ok       bool
		err      error
	)
	if req.CommandPath != "" {
		resolved, ok, err = lookupCommandPath(ctx, exec, req.VirtualWD, req.CommandPath, "path", req.Argv[0])
	} else {
		resolved, ok, err = lookupCommand(ctx, exec, req.VirtualWD, req.Env, req.Argv[0])
	}
	if err != nil {
		if policy.IsDenied(err) {
			recordPolicyDenied(exec.Trace, err, "stat", "", req.Argv[0], "")
			return req.CurrentEnv, shellFailureToWriter(ctx, req.Stderr, 126, "%v", err)
		}
		return req.CurrentEnv, err
	}
	if !ok {
		return req.CurrentEnv, shellFailureToWriter(ctx, req.Stderr, 127, "%s: command not found", req.Argv[0])
	}
	if req.CommandName != "" {
		resolved.name = req.CommandName
	}

	start := time.Now().UTC()
	if !req.Internal {
		if err := allowCommand(ctx, exec.Policy, resolved.name, req.Argv); err != nil {
			recordPolicyDenied(exec.Trace, err, "", resolved.path, resolved.name, resolved.source)
			return req.CurrentEnv, shellFailureToWriter(ctx, req.Stderr, 126, "%v", err)
		}
		if hc, ok := interp.LookupHandlerContext(ctx); ok && !strings.Contains(req.Argv[0], "/") && resolved.hashPath != "" {
			if resolved.source != "path-cache" {
				hc.RememberCommandHash(req.Argv[0], resolved.hashPath)
			}
			hc.IncrementCommandHash(req.Argv[0])
		}
		if !req.FromBootstrap {
			recordCommand(exec.Trace, trace.EventCommandStart, traceCommandInfo(req.Argv, false, &commandTraceResolution{
				Dir:              req.VirtualWD,
				Position:         req.Position,
				ResolvedName:     resolved.name,
				ResolvedPath:     resolved.path,
				ResolutionSource: resolved.source,
			}))
		}
	}

	invokeCtx := ctx
	if req.PrepareInvoke != nil {
		invokeCtx = req.PrepareInvoke(ctx)
	}
	finalEnv, err := invokeResolvedCommand(invokeCtx, exec, resolved, req.Argv, req.CurrentEnv, req.VirtualWD, req.Stdin, req.Stdout, req.Stderr)
	if req.SyncEnv != nil {
		if syncErr := req.SyncEnv(ctx, req.CurrentEnv, finalEnv); syncErr != nil {
			return finalEnv, syncErr
		}
	}

	exitCode := 0
	if err != nil {
		exitCode = 1
		if code, ok := commands.ExitCode(err); ok {
			exitCode = code
			if msg, ok := commands.DiagnosticMessage(err); ok && req.Stderr != nil {
				_, _ = fmt.Fprintln(req.Stderr, msg)
			}
			err = interp.ExitStatus(code)
		}
	}

	if !req.Internal && !req.FromBootstrap {
		recordCommand(exec.Trace, trace.EventCommandExit, traceCommandInfo(req.Argv, false, &commandTraceResolution{
			Dir:              req.VirtualWD,
			Position:         req.Position,
			ResolvedName:     resolved.name, //nolint:nilaway // resolved is non-nil when ok is true (guarded above)
			ResolvedPath:     resolved.path,
			ResolutionSource: resolved.source,
			ExitCode:         exitCode,
			Duration:         time.Since(start),
		}))
	}
	return finalEnv, err
}
