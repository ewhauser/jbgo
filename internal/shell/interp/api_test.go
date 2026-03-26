package interp

import (
	"bytes"
	"io"
	"testing"
	"time"
)

type seekableTestStdin struct {
	*bytes.Reader
}

func (seekableTestStdin) Close() error { return nil }

func (seekableTestStdin) SetReadDeadline(time.Time) error { return nil }

func TestRedirectedStdinReaderTracksOffsetAcrossSeek(t *testing.T) {
	t.Parallel()

	base := seekableTestStdin{Reader: bytes.NewReader([]byte("abcdef"))}
	wrapped := wrapRedirectedStdinReader(base, base, redirectedStdinMetadata{
		path:   "/tmp/in",
		offset: 0,
	})

	redirected, ok := wrapped.(interface {
		io.Reader
		RedirectOffset() int64
		Seek(offset int64, whence int) (int64, error)
	})
	if !ok {
		t.Fatalf("wrapRedirectedStdinReader() type = %T, want seekable redirected stdin", wrapped)
	}

	buf := make([]byte, 2)
	if n, err := redirected.Read(buf); err != nil || n != 2 {
		t.Fatalf("Read() = (%d, %v), want (2, nil)", n, err)
	}
	if got := redirected.RedirectOffset(); got != 2 {
		t.Fatalf("RedirectOffset() after read = %d, want 2", got)
	}

	if pos, err := redirected.Seek(4, io.SeekStart); err != nil || pos != 4 {
		t.Fatalf("Seek(start) = (%d, %v), want (4, nil)", pos, err)
	}
	if got := redirected.RedirectOffset(); got != 4 {
		t.Fatalf("RedirectOffset() after seek start = %d, want 4", got)
	}

	if pos, err := redirected.Seek(-1, io.SeekCurrent); err != nil || pos != 3 {
		t.Fatalf("Seek(current) = (%d, %v), want (3, nil)", pos, err)
	}
	if got := redirected.RedirectOffset(); got != 3 {
		t.Fatalf("RedirectOffset() after seek current = %d, want 3", got)
	}
}

func TestRedirectedStdinReaderDoesNotExposeSeekForPipeBackedInput(t *testing.T) {
	t.Parallel()

	pr, pw := NewVirtualPipe()
	defer pr.Close()
	defer pw.Close()

	base := seekableTestStdin{Reader: bytes.NewReader([]byte("abcdef"))}
	wrapped := wrapRedirectedStdinReader(pr, base, redirectedStdinMetadata{
		path:   "/tmp/in",
		offset: 0,
	})
	if _, ok := wrapped.(interface {
		Seek(offset int64, whence int) (int64, error)
	}); ok {
		t.Fatalf("wrapRedirectedStdinReader() type = %T, want non-seekable pipe-backed reader", wrapped)
	}
}
