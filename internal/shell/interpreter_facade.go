package shell

import (
	"context"
	"io"

	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/interp"
	"github.com/ewhauser/gbash/policy"
	"github.com/ewhauser/gbash/trace"
)

func (m *core) commandFacade(exec *Execution) *commandFacade {
	return newCommandFacade(commandFacadeDeps{
		Resolve: func(ctx context.Context, dir string, env expand.Environ, name string) (*facadeResolvedCommand, bool, error) {
			resolved, ok, err := lookupCommand(ctx, exec, dir, env, name)
			if err != nil || !ok {
				return nil, ok, err
			}
			return &facadeResolvedCommand{
				Name:    resolved.name,
				Path:    resolved.path,
				Source:  resolved.source,
				Payload: resolved,
			}, true, nil
		},
		Allow: func(ctx context.Context, name string, argv []string) error {
			return allowCommand(ctx, exec.Policy, name, argv)
		},
		Invoke: func(ctx context.Context, resolved *facadeResolvedCommand, argv []string, currentEnv map[string]string, virtualWD string, stdin io.Reader, stdout io.Writer, stderr io.Writer) (map[string]string, error) {
			runtimeResolved, _ := resolved.Payload.(*resolvedCommand)
			return invokeResolvedCommand(ctx, exec, runtimeResolved, argv, currentEnv, virtualWD, stdin, stdout, stderr)
		},
		IsPolicyDenied: policy.IsDenied,
		PolicyFailure: func(ctx context.Context, stderr io.Writer, format string, args ...any) error {
			return shellFailureToWriter(ctx, stderr, 126, format, args...)
		},
		NotFound: func(ctx context.Context, stderr io.Writer, name string) error {
			return shellFailureToWriter(ctx, stderr, 127, "%s: command not found", name)
		},
		RecordDenied: func(err error, action, path, name, source string) {
			recordPolicyDenied(exec.Trace, err, action, path, name, source)
		},
		RecordStart: func(argv []string, info facadeCommandTrace) {
			recordCommand(exec.Trace, trace.EventCommandStart, traceCommandInfo(argv, false, &commandTraceResolution{
				Dir:              info.Dir,
				Position:         info.Position,
				ResolvedName:     info.ResolvedName,
				ResolvedPath:     info.ResolvedPath,
				ResolutionSource: info.ResolutionSource,
			}))
		},
		RecordExit: func(argv []string, info facadeCommandTrace) {
			recordCommand(exec.Trace, trace.EventCommandExit, traceCommandInfo(argv, false, &commandTraceResolution{
				Dir:              info.Dir,
				Position:         info.Position,
				ExitCode:         info.ExitCode,
				Duration:         info.Duration,
				ResolvedName:     info.ResolvedName,
				ResolvedPath:     info.ResolvedPath,
				ResolutionSource: info.ResolutionSource,
			}))
		},
	})
}

func (m *core) newRunner(config *interp.VirtualConfig, gbash *interp.GBashConfig) (*interp.Runner, error) {
	return m.commandFacade(&Execution{}).NewRunner(config, gbash)
}
