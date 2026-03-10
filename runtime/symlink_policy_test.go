package runtime

import (
	"context"
	"os"
	"strings"
	"testing"

	jbfs "github.com/cadencerpm/just-bash-go/fs"
	"github.com/cadencerpm/just-bash-go/policy"
)

type symlinkFSFactory struct {
	files    map[string]string
	symlinks map[string]string
}

func (f symlinkFSFactory) New(ctx context.Context) (jbfs.FileSystem, error) {
	mem := jbfs.NewMemory()
	for name, contents := range f.files {
		file, err := mem.OpenFile(ctx, name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return nil, err
		}
		if _, err := file.Write([]byte(contents)); err != nil {
			file.Close()
			return nil, err
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
	}
	for linkName, target := range f.symlinks {
		if err := mem.Symlink(context.Background(), target, linkName); err != nil {
			return nil, err
		}
	}
	return mem, nil
}

func TestDefaultPolicyDeniesSymlinkTraversal(t *testing.T) {
	rt := newRuntime(t, &Config{
		FSFactory: symlinkFSFactory{
			files: map[string]string{
				"/safe/target.txt": "hello\n",
			},
			symlinks: map[string]string{
				"/safe/link.txt": "target.txt",
			},
		},
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "cat /safe/link.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "symlink traversal denied") {
		t.Fatalf("Stderr = %q, want symlink denial", result.Stderr)
	}
}

func TestFollowModeChecksResolvedReadTargetAgainstAllowedRoots(t *testing.T) {
	rt := newRuntime(t, &Config{
		FSFactory: symlinkFSFactory{
			files: map[string]string{
				"/denied/secret.txt": "secret\n",
			},
			symlinks: map[string]string{
				"/safe/link.txt": "/denied/secret.txt",
			},
		},
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/safe", "/usr/bin", "/bin"},
			WriteRoots:  []string{"/safe"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "cat /safe/link.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, `read "/denied/secret.txt" denied`) {
		t.Fatalf("Stderr = %q, want resolved-target denial", result.Stderr)
	}
}

func TestFollowModeAllowsSymlinkTraversalWithinAllowedRoots(t *testing.T) {
	rt := newRuntime(t, &Config{
		FSFactory: symlinkFSFactory{
			files: map[string]string{
				"/safe/target.txt": "hello\n",
			},
			symlinks: map[string]string{
				"/safe/link.txt": "target.txt",
			},
		},
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/safe", "/usr/bin", "/bin"},
			WriteRoots:  []string{"/safe"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "cat /safe/link.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "hello\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestFollowModeChecksResolvedWriteTargetAgainstAllowedRoots(t *testing.T) {
	rt := newRuntime(t, &Config{
		FSFactory: symlinkFSFactory{
			files: map[string]string{
				"/denied/.keep": "",
			},
			symlinks: map[string]string{
				"/safe/out": "/denied",
			},
		},
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/safe", "/usr/bin", "/bin"},
			WriteRoots:  []string{"/safe"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo hi > /safe/out/new.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 126 {
		t.Fatalf("ExitCode = %d, want 126", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, `write "/denied/new.txt" denied`) {
		t.Fatalf("Stderr = %q, want resolved-write denial", result.Stderr)
	}
}
