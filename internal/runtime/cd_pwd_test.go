package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/policy"
)

func TestVirtualCDReportsUnsetOldPWD(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "" +
			"unset OLDPWD\n" +
			"cd - >/dev/null\n" +
			"echo status=$?\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status=1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "cd: OLDPWD not set\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestVirtualCDRejectsLexicallyMissingIntermediate(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "" +
			"cd nonexistent_ZZ/..\n" +
			"echo status=$?\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status=1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "cd: nonexistent_ZZ/..: No such file or directory\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestVirtualCDUsesCDPATH(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "" +
			"mkdir -p /tmp/spam/foo /tmp/eggs/foo\n" +
			"CDPATH='/tmp/spam:/tmp/eggs'\n" +
			"cd foo\n" +
			"echo status=$?\n" +
			"pwd\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/tmp/spam/foo\nstatus=0\n/tmp/spam/foo\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestPwdIgnoresPWDMutationAndUnset(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "" +
			"dir=/tmp/oil-spec-test/pwd\n" +
			"mkdir -p $dir\n" +
			"cd $dir\n" +
			"PWD=foo\n" +
			"echo before $PWD\n" +
			"pwd\n" +
			"echo after $PWD\n" +
			"unset PWD\n" +
			"echo PWD=$PWD\n" +
			"pwd\n" +
			"echo PWD=$PWD\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "" +
		"before foo\n" +
		"/tmp/oil-spec-test/pwd\n" +
		"after foo\n" +
		"PWD=\n" +
		"/tmp/oil-spec-test/pwd\n" +
		"PWD=\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestPwdReturnsRememberedPathAfterRemovingCurrentDir(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "" +
			"dir=/tmp/oil-spec-test/pwd-removed\n" +
			"mkdir -p $dir\n" +
			"cd $dir\n" +
			"pwd\n" +
			"rmdir $dir\n" +
			"echo status=$?\n" +
			"pwd\n" +
			"echo status=$?\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "" +
		"/tmp/oil-spec-test/pwd-removed\n" +
		"status=0\n" +
		"/tmp/oil-spec-test/pwd-removed\n" +
		"status=0\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestChildShellPwdUsesPhysicalStartupDirWhenPWDUnset(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "" +
			"tmp=/tmp/cd-startup-symlink\n" +
			"mkdir -p $tmp/target\n" +
			"ln -s -f $tmp/target $tmp/symlink\n" +
			"cd $tmp/symlink\n" +
			"sh -c 'basename $(pwd)'\n" +
			"unset PWD\n" +
			"sh -c 'basename $(pwd)'\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "symlink\ntarget\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestVirtualCDHandlesLogicalAndPhysicalSymlinkModes(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "" +
			"targ=/tmp/cd-symtarget\n" +
			"lnk=/tmp/cd-symlink\n" +
			"mkdir -p $targ/subdir\n" +
			"ln -s $targ $lnk\n" +
			"cd $lnk\n" +
			"echo $PWD\n" +
			"cd /\n" +
			"cd -P $lnk\n" +
			"echo $PWD\n" +
			"cd $lnk/subdir\n" +
			"cd ..\n" +
			"echo $PWD\n" +
			"cd $lnk/subdir\n" +
			"cd -P ..\n" +
			"echo $PWD\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "" +
		"/tmp/cd-symlink\n" +
		"/tmp/cd-symtarget\n" +
		"/tmp/cd-symlink\n" +
		"/tmp/cd-symtarget\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestVirtualCDRejectsSymlinkTraversalByDefault(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "" +
			"mkdir -p /tmp/cd-deny-target\n" +
			"ln -s /tmp/cd-deny-target /tmp/cd-deny-link\n" +
			"cd /tmp/cd-deny-link\n" +
			"echo status=$?\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "status=1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "symlink traversal denied") {
		t.Fatalf("Stderr = %q, want symlink traversal denial", result.Stderr)
	}
}

func TestDirectoryStackDisplaysRootWhenHOMEIsRoot(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		WorkDir: "/",
		Env: map[string]string{
			"HOME": "/",
			"PWD":  "/",
		},
		Script: "" +
			"cd /\n" +
			"dirs\n" +
			"pushd /tmp >/dev/null\n" +
			"dirs\n" +
			"dirs -p\n" +
			"dirs -v\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "" +
		"/\n" +
		"/tmp /\n" +
		"/tmp\n" +
		"/\n" +
		" 0  /tmp\n" +
		" 1  /\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if strings.Contains(result.Stdout, "~") {
		t.Fatalf("Stdout = %q, want no tilde display for root", result.Stdout)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}
