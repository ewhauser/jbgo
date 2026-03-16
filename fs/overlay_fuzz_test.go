package fs

import (
	"context"
	"os"
	"strings"
	"testing"
)

func FuzzOverlayFSRealpath(f *testing.F) {
	f.Add([]byte{0, 0})
	f.Add([]byte{1, 1})
	f.Add([]byte{2, 1})
	f.Add([]byte{3, 0})
	f.Add([]byte{4, 1})
	f.Add([]byte{5, 0})

	f.Fuzz(func(t *testing.T, raw []byte) {
		cursor := newOverlayFuzzCursor(raw)
		lower := NewMemory()
		writeOverlayFuzzFile(t, lower, "/safe/target.txt", []byte("target\n"))
		writeOverlayFuzzFile(t, lower, "/safe/upper.txt", []byte("upper\n"))

		linkPath := "/safe/link.txt"
		aliasPath := "/safe/alias.txt"
		targetCases := []string{"target.txt", "./target.txt", "../safe/target.txt", "/safe/target.txt", aliasPath, "/safe/missing.txt", linkPath}
		aliasCases := []string{"target.txt", "/safe/target.txt"}

		linkTarget := targetCases[cursor.Intn(len(targetCases))]
		if linkTarget == aliasPath {
			if err := lower.Symlink(context.Background(), aliasCases[cursor.Intn(len(aliasCases))], aliasPath); err != nil {
				t.Fatalf("Symlink(alias) error = %v", err)
			}
		}
		if err := lower.Symlink(context.Background(), linkTarget, linkPath); err != nil {
			t.Fatalf("Symlink(link) error = %v", err)
		}

		overlay := NewOverlay(lower)
		if cursor.Intn(2) == 1 {
			if err := overlay.Symlink(context.Background(), "upper.txt", linkPath); err != nil {
				t.Fatalf("Symlink(upper override) error = %v", err)
			}
		}

		realpath, err := overlay.Realpath(context.Background(), linkPath)
		if err == nil {
			if got := Clean(realpath); got != realpath {
				t.Fatalf("Realpath() = %q, want clean absolute path", realpath)
			}
			if !strings.HasPrefix(realpath, "/") || strings.Contains(realpath, "..") {
				t.Fatalf("Realpath() = %q, want sandbox path", realpath)
			}
		}
	})
}

type overlayFuzzCursor struct {
	data []byte
	idx  int
}

func newOverlayFuzzCursor(data []byte) *overlayFuzzCursor {
	return &overlayFuzzCursor{data: data}
}

func (c *overlayFuzzCursor) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	if len(c.data) == 0 {
		c.idx++
		return c.idx % n
	}
	value := int(c.data[c.idx%len(c.data)])
	c.idx++
	return value % n
}

func writeOverlayFuzzFile(t *testing.T, fsys FileSystem, name string, data []byte) {
	t.Helper()

	file, err := fsys.OpenFile(context.Background(), name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile(%q) error = %v", name, err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(data); err != nil {
		t.Fatalf("Write(%q) error = %v", name, err)
	}
}
