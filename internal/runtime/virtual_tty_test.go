package runtime

import (
	"io"
	stdfs "io/fs"
	"testing"
	"time"
)

type fakeTTYReader struct {
	path string
	mode stdfs.FileMode
}

func (r fakeTTYReader) Read([]byte) (int, error) { return 0, io.EOF }

func (r fakeTTYReader) RedirectPath() string { return r.path }

func (r fakeTTYReader) RedirectFlags() int { return 0 }

func (r fakeTTYReader) RedirectOffset() int64 { return 0 }

func (r fakeTTYReader) Stat() (stdfs.FileInfo, error) {
	return fakeTTYInfo{mode: r.mode}, nil
}

type fakeStatOnlyTTYReader struct {
	mode stdfs.FileMode
}

func (r fakeStatOnlyTTYReader) Read([]byte) (int, error) { return 0, io.EOF }

func (r fakeStatOnlyTTYReader) Stat() (stdfs.FileInfo, error) {
	return fakeTTYInfo(r), nil
}

type fakeFDTTYReader struct {
	mode stdfs.FileMode
	fd   uintptr
}

func (r fakeFDTTYReader) Read([]byte) (int, error) { return 0, io.EOF }

func (r fakeFDTTYReader) Stat() (stdfs.FileInfo, error) {
	return fakeTTYInfo{mode: r.mode}, nil
}

func (r fakeFDTTYReader) Fd() uintptr { return r.fd }

type fakeTTYInfo struct {
	mode stdfs.FileMode
}

func (i fakeTTYInfo) Name() string         { return "" }
func (i fakeTTYInfo) Size() int64          { return 0 }
func (i fakeTTYInfo) Mode() stdfs.FileMode { return i.mode }
func (i fakeTTYInfo) ModTime() time.Time   { return time.Time{} }
func (i fakeTTYInfo) IsDir() bool          { return false }
func (i fakeTTYInfo) Sys() any             { return nil }

func TestReaderLooksLikeTTYRecognizesTTYRedirectPath(t *testing.T) {
	t.Parallel()

	if !readerLooksLikeTTY(fakeTTYReader{path: "/dev/tty", mode: stdfs.ModeCharDevice | 0o666}) {
		t.Fatal("readerLooksLikeTTY(/dev/tty) = false, want true")
	}
}

func TestReaderLooksLikeTTYRejectsNullDeviceRedirect(t *testing.T) {
	t.Parallel()

	if readerLooksLikeTTY(fakeTTYReader{path: "/dev/null", mode: stdfs.ModeCharDevice | 0o666}) {
		t.Fatal("readerLooksLikeTTY(/dev/null) = true, want false")
	}
}

func TestReaderLooksLikeTTYFallsBackToCharDeviceStatWithoutRedirectMetadata(t *testing.T) {
	t.Parallel()

	if !readerLooksLikeTTY(fakeStatOnlyTTYReader{mode: stdfs.ModeCharDevice | 0o600}) {
		t.Fatal("readerLooksLikeTTY(stat-only tty reader) = false, want true")
	}
}

func TestReaderLooksLikeTTYDoesNotFallBackWhenFDIsPresent(t *testing.T) {
	t.Parallel()

	if readerLooksLikeTTY(fakeFDTTYReader{mode: stdfs.ModeCharDevice | 0o600, fd: 0}) {
		t.Fatal("readerLooksLikeTTY(fd-backed non-terminal reader) = true, want false")
	}
}
