package builtins_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gbruntime "github.com/ewhauser/gbash"
)

func TestEnvSupportsLongIgnoreEnvironmentIsolated(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env --ignore-environment ONLY=present printenv ONLY\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "present\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEnvSupportsBareDoubleDashCommandSeparator(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env -- printenv HOME\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/home/agent\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEnvSupportsAssignmentsAfterBareDoubleDash(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env -- ONLY=present printenv ONLY\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "present\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEnvTreatsDoubleDashAfterAssignmentsAsCommandName(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env ONLY=present -- true\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 127 {
		t.Fatalf("ExitCode = %d, want 127; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stderr, "env: '--': No such file or directory\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestEnvCanExecuteDoubleDashCommandAfterAssignments(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	script := "#!/bin/sh\n" +
		"echo pass\n"
	if err := os.WriteFile(filepath.Join(root, "simple_echo"), []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(simple_echo) error = %v", err)
	}
	if err := os.Symlink("simple_echo", filepath.Join(root, "--")); err != nil {
		t.Fatalf("Symlink(--) error = %v", err)
	}

	rt := newRuntime(t, &Config{
		FileSystem: gbruntime.ReadWriteDirectoryFileSystem(root, gbruntime.ReadWriteDirectoryOptions{}),
	})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		WorkDir: "/",
		Script: "PATH=/bin:/usr/bin:\n" +
			"export PATH\n" +
			"env ONLY=present -- true\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "pass\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestEnvPreservesExportedEnvironmentAlongsideAssignments(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "export KEEP=present\n" +
			"env ADD=extra | grep '^KEEP='\n" +
			"env ADD=extra | grep '^ADD='\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "KEEP=present\nADD=extra\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEnvIgnoreEnvironmentDoesNotLeakProjectedRuntimeEnv(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env -i env\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got := result.Stdout; got != "" {
		t.Fatalf("Stdout = %q, want empty", got)
	}
}

func TestEnvPropagatesPrefixAssignmentsToNestedPrintEnv(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "export KEEP=present\n" +
			"env ADD=extra printenv KEEP ADD\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "present\nextra\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEnvPreservesInvalidBytesForNestedCommands(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "value=$(printf '\\355\\272\\255')\n" +
			"env printf '%s' \"$value\" | od -An -tx1 -v\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, " ed ba ad\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEnvSupportsUnsetShortAndLongAttachedForms(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env -uHOME --unset=USER printenv HOME USER\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got := result.Stdout; got != "" {
		t.Fatalf("Stdout = %q, want empty", got)
	}
}

func TestEnvReportsMissingUnsetArgument(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env -u\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 125 {
		t.Fatalf("ExitCode = %d, want 125; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stderr, "env: option requires an argument -- 'u'\nTry 'env --help' for more information.\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestEnvRejectsEmptyUnsetName(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env -u '' true\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 125 {
		t.Fatalf("ExitCode = %d, want 125; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stderr, "env: cannot unset '': Invalid argument\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestPrintEnvReturnsOneWhenAnyNameIsMissing(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printenv HOME MISSING\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/home/agent\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEnvSupportsChdirWithCommand(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir /tmp/work\nenv --chdir=/tmp/work pwd\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/tmp/work\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEnvReportsMissingCommandForChdir(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir /tmp/work\nenv -C /tmp/work\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 125 {
		t.Fatalf("ExitCode = %d, want 125; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stderr, "env: must specify command with --chdir (-C)\nTry 'env --help' for more information.\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestEnvDebugReportsArgv0Override(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env -v -a short true\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "argv0:     'short'\nexecuting: true\n   arg[0]= 'short'\n"
	if got := result.Stderr; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestEnvDebugSupportsExplicitEmptyArgv0(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env -v -a short --argv0= true\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "argv0:     ''\nexecuting: true\n   arg[0]= ''\n"
	if got := result.Stderr; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestEnvNullPrintsNULTerminatedEnvironment(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env -i -0 \"$(printf 'a=b\\nc=')\" | od -An -tx1 -v\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, " 61 3d 62 0a 63 3d 00\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEnvNullRejectsCommands(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env -0 echo hi\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 125 {
		t.Fatalf("ExitCode = %d, want 125; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stderr, "env: cannot specify --null (-0) with command\nTry 'env --help' for more information.\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestEnvSplitStringExpandsOriginalEnvironmentBeforeUnset(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "FOO=BAR env -S'-uFOO sh -c \"echo x${FOO}x =\\$FOO=\"'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "xBARx ==\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEnvSplitStringPreservesEmptyQuotedArgs(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env -S'printf x%sx\\\\n A \"\" B'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "xAx\nxx\nxBx\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEnvSplitStringRejectsInvalidEscape(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env -S'A=B\\q'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 125 {
		t.Fatalf("ExitCode = %d, want 125; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stderr, "env: invalid sequence '\\q' in -S\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestEnvRejectsWhitespaceShebangMisuse(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env '-v -S cat -n'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 125 {
		t.Fatalf("ExitCode = %d, want 125; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "env: invalid option -- ' '\nenv: use -[v]S to pass options in shebang lines\nTry 'env --help' for more information.\n"
	if got := result.Stderr; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestPrintEnvInvalidOptionReturnsTwo(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printenv -x\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stderr, "printenv: invalid option -- 'x'\nTry 'printenv --help' for more information.\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestEnvBypassesCompatWrapperScriptsForNestedCommands(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	script := "mkdir -p /tmp/compat\n" +
		"cat <<'EOF' >/tmp/compat/printenv\n" +
		"#!/bin/sh\n" +
		"gbash_bin=$root_dir/build-aux/gbash-harness/gbash\n" +
		"jbgo_disabled_builtins=$(gnu_disabled_builtins)\n" +
		"if [ -e /build-aux/gbash-harness/gnu-programs.txt ]; then\n" +
		"  exec \"/bin/printenv\" \"$@\"\n" +
		"fi\n" +
		"exec \"$gbash_bin\" --readwrite-root \"$root_dir\" --cwd \"$sandbox_cwd\" -c 'exec \"$@\"' _ printenv \"$@\"\n" +
		"echo wrong >&2\n" +
		"exit 99\n" +
		"EOF\n" +
		"chmod 755 /tmp/compat/printenv\n" +
		"PATH=/tmp/compat:$PATH\n" +
		"ENV_TEST=a env printenv ENV_TEST\n"

	result, err := rt.Run(context.Background(), &ExecutionRequest{Script: script})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "a\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestEnvBypassesCompatWrapperScriptsForNestedShells(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	script := "mkdir -p /tmp/compat\n" +
		"cat <<'EOF' >/tmp/compat/sh\n" +
		"#!/bin/sh\n" +
		"gbash_bin=$root_dir/build-aux/gbash-harness/gbash\n" +
		"jbgo_disabled_builtins=$(gnu_disabled_builtins)\n" +
		"if [ -e /build-aux/gbash-harness/gnu-programs.txt ]; then\n" +
		"  exec \"/bin/sh\" \"$@\"\n" +
		"fi\n" +
		"exec \"$gbash_bin\" --readwrite-root \"$root_dir\" --cwd \"$sandbox_cwd\" -c 'exec \"$@\"' _ sh \"$@\"\n" +
		"echo wrong >&2\n" +
		"exit 99\n" +
		"EOF\n" +
		"chmod 755 /tmp/compat/sh\n" +
		"PATH=/tmp/compat:$PATH\n" +
		"FOO=BAR env sh -c 'echo =$FOO='\n"

	result, err := rt.Run(context.Background(), &ExecutionRequest{Script: script})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "=BAR=\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestEnvNestedShellResolvesEqualsCommandThroughEmptyPathEntry(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "PATH=$PATH:\n" +
			"export PATH\n" +
			"cat <<'EOF' > ./c=d\n" +
			"#!/bin/sh\n" +
			"echo pass\n" +
			"EOF\n" +
			"chmod 755 ./c=d\n" +
			"env sh -c 'exec \"$@\"' sh c=d echo fail\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "pass\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEnvFollowsSymlinkCommandsFromEmptyPathEntry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	script := "#!/bin/sh\n" +
		"echo pass\n"
	if err := os.WriteFile(filepath.Join(root, "simple_echo"), []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(simple_echo) error = %v", err)
	}
	if err := os.Symlink("simple_echo", filepath.Join(root, "-u")); err != nil {
		t.Fatalf("Symlink(-u) error = %v", err)
	}

	rt := newRuntime(t, &Config{
		FileSystem: gbruntime.ReadWriteDirectoryFileSystem(root, gbruntime.ReadWriteDirectoryOptions{}),
	})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		WorkDir: "/",
		Script: "PATH=/bin:/usr/bin:\n" +
			"export PATH\n" +
			"env -- -u pass\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "pass\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestEnvDoesNotRewriteSilentExit127FromExistingCommand(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "env sh -c 'exit 127'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 127 {
		t.Fatalf("ExitCode = %d, want 127; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got := result.Stdout; got != "" {
		t.Fatalf("Stdout = %q, want empty", got)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestEnvPreservesInvokedSymlinkNameForNestedCommands(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"${0##*/}\"\n"
	if err := os.WriteFile(filepath.Join(root, "show_argv0"), []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(show_argv0) error = %v", err)
	}
	if err := os.Symlink("show_argv0", filepath.Join(root, "alias0")); err != nil {
		t.Fatalf("Symlink(alias0) error = %v", err)
	}

	rt := newRuntime(t, &Config{
		FileSystem: gbruntime.ReadWriteDirectoryFileSystem(root, gbruntime.ReadWriteDirectoryOptions{}),
	})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		WorkDir: "/",
		Script: "PATH=/bin:/usr/bin:\n" +
			"export PATH\n" +
			"env alias0\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "alias0\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestEnvMapsHostAbsoluteCompatWrapperPathsFromTopBuilddir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("MkdirAll(src) error = %v", err)
	}
	wrapper := "#!/bin/sh\n" +
		"gbash_bin=$root_dir/build-aux/gbash-harness/gbash\n" +
		"jbgo_disabled_builtins=$(gnu_disabled_builtins)\n" +
		"if [ -e /build-aux/gbash-harness/gnu-programs.txt ]; then\n" +
		"  exec \"/bin/printf\" \"$@\"\n" +
		"fi\n" +
		"exec \"$gbash_bin\" --readwrite-root \"$root_dir\" --cwd \"$sandbox_cwd\" -c 'exec \"$@\"' _ printf \"$@\"\n"
	if err := os.WriteFile(filepath.Join(root, "src", "printf"), []byte(wrapper), 0o755); err != nil {
		t.Fatalf("WriteFile(printf wrapper) error = %v", err)
	}

	rt := newRuntime(t, &Config{
		FileSystem: gbruntime.ReadWriteDirectoryFileSystem(root, gbruntime.ReadWriteDirectoryOptions{}),
	})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Env: map[string]string{
			"abs_top_builddir": root,
		},
		Script: "env -S '" + root + "/src/printf x%sx\\\\n A B'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "xAx\nxBx\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestEnvIgnoresLargeBuiltinNamedExecutablesWhenProbingCompatWrappers(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	script := "#!/bin/sh\n" +
		"printf 'pass\\n'\n" +
		strings.Repeat("# padding to exceed compat probe size\n", 128)
	if err := os.WriteFile(filepath.Join(root, "printf"), []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(printf) error = %v", err)
	}

	rt := newRuntime(t, &Config{
		FileSystem: gbruntime.ReadWriteDirectoryFileSystem(root, gbruntime.ReadWriteDirectoryOptions{}),
	})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		WorkDir: "/",
		Script: "PATH=:/bin:/usr/bin\n" +
			"export PATH\n" +
			"env printf\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "pass\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}
