//go:build !windows

package fs

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestHostFSDoesNotReadThroughRacedFinalSymlink(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outsideRoot := t.TempDir()

	notePath := filepath.Join(root, "note.txt")
	secretPath := filepath.Join(outsideRoot, "secret.txt")
	const safeContents = "inside\n"
	const secretContents = "secret\n"

	if err := os.WriteFile(notePath, []byte(safeContents), 0o644); err != nil {
		t.Fatalf("WriteFile(note) error = %v", err)
	}
	if err := os.WriteFile(secretPath, []byte(secretContents), 0o644); err != nil {
		t.Fatalf("WriteFile(secret) error = %v", err)
	}

	fsys, err := NewHost(HostOptions{Root: root})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		raceFlipRegularAndSymlink(notePath, safeContents, secretPath, stop)
	})
	defer func() {
		close(stop)
		wg.Wait()
	}()

	successfulReads := 0
	for range 30000 {
		file, err := fsys.Open(context.Background(), defaultHostVirtualRoot+"/note.txt")
		if err != nil {
			continue
		}
		data, readErr := io.ReadAll(file)
		_ = file.Close()
		if readErr != nil {
			continue
		}
		successfulReads++
		if got := string(data); got == secretContents {
			t.Fatalf("read escaped host contents %q during raced final-symlink replacement", got)
		}
	}
	if successfulReads == 0 {
		t.Fatal("race regression did not produce any successful reads")
	}
}

func TestReadWriteFSDoesNotWriteThroughRacedFinalSymlink(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outsideRoot := t.TempDir()

	notePath := filepath.Join(root, "note.txt")
	outsidePath := filepath.Join(outsideRoot, "escaped.txt")
	const safeContents = "inside\n"
	const outsideContents = "outside\n"
	const payload = "PWN\n"

	if err := os.WriteFile(notePath, []byte(safeContents), 0o644); err != nil {
		t.Fatalf("WriteFile(note) error = %v", err)
	}
	if err := os.WriteFile(outsidePath, []byte(outsideContents), 0o644); err != nil {
		t.Fatalf("WriteFile(outside) error = %v", err)
	}

	fsys, err := NewReadWrite(ReadWriteOptions{Root: root})
	if err != nil {
		t.Fatalf("NewReadWrite() error = %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		raceFlipRegularAndSymlink(notePath, safeContents, outsidePath, stop)
	})
	defer func() {
		close(stop)
		wg.Wait()
	}()

	successfulWrites := 0
	for range 10000 {
		file, err := fsys.OpenFile(context.Background(), "/note.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			continue
		}
		_, writeErr := io.WriteString(file, payload)
		closeErr := file.Close()
		if writeErr == nil && closeErr == nil {
			successfulWrites++
		}
	}
	if successfulWrites == 0 {
		t.Fatal("race regression did not produce any successful writes")
	}

	gotOutside, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatalf("ReadFile(outside) error = %v", err)
	}
	if got := string(gotOutside); got != outsideContents {
		t.Fatalf("outside file escaped write containment: got %q, want %q", got, outsideContents)
	}
}

func raceFlipRegularAndSymlink(targetPath, safeContents, symlinkTarget string, stop <-chan struct{}) {
	linkSwap := targetPath + ".swap-link"
	fileSwap := targetPath + ".swap-file"

	for {
		select {
		case <-stop:
			return
		default:
		}

		_ = os.Remove(linkSwap)
		if err := os.Symlink(symlinkTarget, linkSwap); err == nil {
			_ = os.Rename(linkSwap, targetPath)
		}
		runtime.Gosched()

		select {
		case <-stop:
			return
		default:
		}

		if err := os.WriteFile(fileSwap, []byte(safeContents), 0o644); err == nil {
			_ = os.Rename(fileSwap, targetPath)
		}
		runtime.Gosched()
	}
}
