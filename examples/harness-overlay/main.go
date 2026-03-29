package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/contrib/extras"
	"golang.org/x/term"
)

const exampleName = "harness-overlay"

var allowedURLPrefixes = []string{
	"https://api.openai.com/",
	"https://api.anthropic.com/",
	"https://api.groq.com/",
	"https://api.deepseek.com/",
	"https://openrouter.ai/",
	"https://api.z.ai/",
	"https://chatgpt.com/",
	"https://auth.openai.com/",
}

var forwardedEnvNames = []string{
	"HARNESS_PROVIDER",
	"HARNESS_MODEL",
	"HARNESS_MAX_TURNS",
	"HARNESS_TOOL_TIMEOUT",
	"OPENAI_API_KEY",
	"OPENAI_API_URL",
	"OPENAI_MAX_TOKENS",
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_API_URL",
	"ANTHROPIC_API_VERSION",
	"ANTHROPIC_MAX_TOKENS",
	"GROQ_API_KEY",
	"DEEPSEEK_API_KEY",
	"OPENROUTER_API_KEY",
	"ZAI_AUTH_TOKEN",
	"CHATGPT_MODEL",
}

type cliOptions struct {
	script string
}

func main() {
	workspaceDir, err := resolveWorkspaceDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	exitCode, err := runWithWorkspace(context.Background(), workspaceDir, os.Stdin, os.Stdout, os.Stderr, os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(exitCode)
}

func runWithWorkspace(ctx context.Context, workspaceDir string, stdin io.Reader, stdout, stderr io.Writer, args []string) (int, error) {
	opts, err := parseCLIOptions(stderr, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 1, err
	}

	rt, err := newRuntime(ctx, workspaceDir)
	if err != nil {
		return 1, fmt.Errorf("create runtime: %w", err)
	}

	session, err := rt.NewSession(ctx)
	if err != nil {
		return 1, fmt.Errorf("create session: %w", err)
	}

	if opts.script == "" && stdinIsTTY(stdin) {
		result, err := session.Interact(ctx, &gbash.InteractiveRequest{
			Name:    exampleName,
			WorkDir: gbash.DefaultWorkspaceMountPoint,
			Stdin:   stdin,
			Stdout:  stdout,
			Stderr:  stderr,
		})
		if err != nil {
			return 1, fmt.Errorf("run interactive shell: %w", err)
		}
		return result.ExitCode, nil
	}

	script := opts.script
	if script == "" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return 1, fmt.Errorf("read stdin: %w", err)
		}
		script = string(data)
	}

	result, err := session.Exec(ctx, &gbash.ExecutionRequest{
		Name:    exampleName,
		Script:  script,
		WorkDir: gbash.DefaultWorkspaceMountPoint,
	})
	if err != nil {
		return 1, fmt.Errorf("run script: %w", err)
	}

	if stdout != nil {
		if _, err := io.WriteString(stdout, result.Stdout); err != nil {
			return 1, fmt.Errorf("write stdout: %w", err)
		}
	}
	if stderr != nil {
		if _, err := io.WriteString(stderr, result.Stderr); err != nil {
			return 1, fmt.Errorf("write stderr: %w", err)
		}
	}

	return result.ExitCode, nil
}

func parseCLIOptions(stderr io.Writer, args []string) (cliOptions, error) {
	var opts cliOptions

	fs := flag.NewFlagSet(exampleName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.script, "script", "", "shell script to run; when empty the example starts an interactive shell or reads stdin")

	if err := fs.Parse(args); err != nil {
		return cliOptions{}, err
	}
	if fs.NArg() != 0 {
		return cliOptions{}, fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}

	return opts, nil
}

func newRuntime(ctx context.Context, workspaceDir string) (*gbash.Runtime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	opts := []gbash.Option{
		gbash.WithWorkspace(workspaceDir),
		gbash.WithRegistry(extras.FullRegistry()),
		gbash.WithWorkingDir(gbash.DefaultWorkspaceMountPoint),
		gbash.WithNetwork(&gbash.NetworkConfig{
			AllowedURLPrefixes: append([]string(nil), allowedURLPrefixes...),
			AllowedMethods: []gbash.Method{
				gbash.MethodGet,
				gbash.MethodHead,
				gbash.MethodPost,
			},
			DenyPrivateRanges: true,
		}),
	}

	if env := forwardedBaseEnv(); len(env) > 0 {
		opts = append(opts, gbash.WithBaseEnv(env))
	}

	return gbash.New(opts...) //nolint:contextcheck // constructor does not accept context
}

func forwardedBaseEnv() map[string]string {
	env := make(map[string]string)
	for _, name := range forwardedEnvNames {
		value, ok := os.LookupEnv(name)
		if !ok || value == "" {
			continue
		}
		env[name] = value
	}
	return env
}

func mustWorkspaceDir() string {
	return filepath.Join(mustExampleDir(), "workspace")
}

func mustExampleDir() string {
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		panic("resolve example dir: runtime.Caller failed")
	}
	return filepath.Dir(file)
}

func resolveWorkspaceDir() (string, error) {
	if workspaceDir := os.Getenv("HARNESS_OVERLAY_WORKSPACE"); workspaceDir != "" {
		if _, err := os.Stat(filepath.Join(workspaceDir, "bin", "harness")); err != nil {
			return "", fmt.Errorf("HARNESS_OVERLAY_WORKSPACE=%q is not a prepared harness workspace: %w", workspaceDir, err)
		}
		return workspaceDir, nil
	}

	workspaceDir := mustWorkspaceDir()
	if _, err := os.Stat(filepath.Join(workspaceDir, "bin", "harness")); err == nil {
		return workspaceDir, nil
	}

	return "", fmt.Errorf("harness workspace is not prepared; run `make -C examples run-harness-overlay` or set HARNESS_OVERLAY_WORKSPACE")
}

func stdinIsTTY(stdin io.Reader) bool {
	file, ok := stdin.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}
