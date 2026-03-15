package builtins_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/policy"
)

func TestCPSupportsParityFlagsIsolated(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo new > /tmp/src.txt\n" +
			"echo old > /tmp/dst.txt\n" +
			"cp --no-clobber --preserve --verbose /tmp/src.txt /tmp/dst.txt\n" +
			"cat /tmp/dst.txt\n" +
			"cp -p -v /tmp/src.txt /tmp/fresh.txt\n" +
			"cat /tmp/fresh.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "old\n'/tmp/src.txt' -> '/tmp/fresh.txt'\nnew\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCPAcceptsForceFlagForOverwrite(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo new > /tmp/src.txt\n" +
			"echo old > /tmp/dst.txt\n" +
			"cp -f /tmp/src.txt /tmp/dst.txt\n" +
			"cat /tmp/dst.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "new\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCPNoDereferencePreservesSourceSymlink(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo payload > /tmp/target.txt\n" +
			"cd /tmp\n" +
			"ln -s target.txt src-link\n" +
			"cp -d /tmp/src-link /tmp/dst-link\n" +
			"readlink /tmp/dst-link\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "target.txt\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCPDereferenceCommandLineAppliesToAllSources(t *testing.T) {
	rt := newRuntime(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo one > /tmp/target1.txt\n" +
			"echo two > /tmp/target2.txt\n" +
			"cd /tmp\n" +
			"ln -s target1.txt link1\n" +
			"ln -s target2.txt link2\n" +
			"mkdir out\n" +
			"cp -H /tmp/link1 /tmp/link2 /tmp/out\n" +
			"test -L /tmp/out/link1 && echo link1-symlink || echo link1-regular\n" +
			"cat /tmp/out/link1\n" +
			"test -L /tmp/out/link2 && echo link2-symlink || echo link2-regular\n" +
			"cat /tmp/out/link2\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "link1-regular\none\nlink2-regular\ntwo\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCPSymlinkCopyOverwritesExistingDestinationByDefault(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo payload > /tmp/target.txt\n" +
			"cd /tmp\n" +
			"ln -s target.txt src-link\n" +
			"echo old > dst-link\n" +
			"cp -P /tmp/src-link /tmp/dst-link\n" +
			"test -L /tmp/dst-link && echo symlink || echo regular\n" +
			"readlink /tmp/dst-link\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "symlink\ntarget.txt\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCPRejectsUnsupportedLinkModes(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo new > /tmp/src.txt\n" +
			"echo old > /tmp/dst.txt\n" +
			"cp -l /tmp/src.txt /tmp/dst.txt\n" +
			"printf 'hard=%s\\n' \"$?\"\n" +
			"cat /tmp/dst.txt\n" +
			"cp -s /tmp/src.txt /tmp/dst.txt\n" +
			"printf 'sym=%s\\n' \"$?\"\n" +
			"cat /tmp/dst.txt\n" +
			"cp --update=older /tmp/src.txt /tmp/dst.txt\n" +
			"printf 'update=%s\\n' \"$?\"\n" +
			"cat /tmp/dst.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "hard=1\nold\nsym=1\nold\nupdate=1\nold\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "cp: --link is not yet supported") {
		t.Fatalf("Stderr = %q, want hard-link rejection", result.Stderr)
	}
	if !strings.Contains(result.Stderr, "cp: --symbolic-link is not yet supported") {
		t.Fatalf("Stderr = %q, want symbolic-link rejection", result.Stderr)
	}
	if !strings.Contains(result.Stderr, "cp: --update is not yet supported") {
		t.Fatalf("Stderr = %q, want update rejection", result.Stderr)
	}
}
