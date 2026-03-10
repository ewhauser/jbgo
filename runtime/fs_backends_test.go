package runtime

import (
	"context"
	"io"
	"os"
	"testing"

	jbfs "github.com/cadencerpm/just-bash-go/fs"
)

type seededFSFactory struct {
	files map[string]string
}

func (f seededFSFactory) New(ctx context.Context) (jbfs.FileSystem, error) {
	mem := jbfs.NewMemory()
	for name, contents := range f.files {
		file, err := mem.OpenFile(ctx, name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return nil, err
		}
		if _, err := io.WriteString(file, contents); err != nil {
			file.Close()
			return nil, err
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
	}
	return mem, nil
}

func TestOverlayFactorySupportsShellReadsAndCopyOnWrite(t *testing.T) {
	rt := newRuntime(t, &Config{
		FSFactory: jbfs.OverlayFactory{
			Lower: seededFSFactory{files: map[string]string{
				"/seed.txt": "seed\n",
			}},
		},
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "cat /seed.txt\necho upper > /seed.txt\ncat /seed.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "seed\nupper\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
