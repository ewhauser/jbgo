package runtime

import (
	"errors"
	"io"
	stdfs "io/fs"
)

type capturePassthroughWriter struct {
	capture     io.Writer
	passthrough io.Writer
}

type redirectedPassthrough interface {
	RedirectPath() string
	RedirectFlags() int
	RedirectOffset() int64
}

type captureRedirectedWriter struct {
	*capturePassthroughWriter
	redirected redirectedPassthrough
}

func newCapturePassthroughWriter(capture *captureBuffer, passthrough io.Writer) io.Writer {
	if passthrough == nil {
		return capture
	}
	base := &capturePassthroughWriter{
		capture:     capture,
		passthrough: passthrough,
	}
	if redirected, ok := passthrough.(redirectedPassthrough); ok {
		return &captureRedirectedWriter{
			capturePassthroughWriter: base,
			redirected:               redirected,
		}
	}
	return base
}

func (w *capturePassthroughWriter) Write(p []byte) (int, error) {
	if _, err := w.capture.Write(p); err != nil {
		return 0, err
	}
	return w.passthrough.Write(p)
}

func (w *capturePassthroughWriter) Stat() (stdfs.FileInfo, error) {
	if file, ok := w.passthrough.(interface {
		Stat() (stdfs.FileInfo, error)
	}); ok {
		return file.Stat()
	}
	return nil, errors.New("stat unsupported")
}

func (w *capturePassthroughWriter) Seek(offset int64, whence int) (int64, error) {
	if seeker, ok := w.passthrough.(io.Seeker); ok {
		return seeker.Seek(offset, whence)
	}
	return 0, errors.New("seek unsupported")
}

func (w *capturePassthroughWriter) Fd() uintptr {
	if file, ok := w.passthrough.(interface{ Fd() uintptr }); ok {
		return file.Fd()
	}
	return 0
}

func (w *captureRedirectedWriter) RedirectPath() string {
	return w.redirected.RedirectPath()
}

func (w *captureRedirectedWriter) RedirectFlags() int {
	return w.redirected.RedirectFlags()
}

func (w *captureRedirectedWriter) RedirectOffset() int64 {
	return w.redirected.RedirectOffset()
}
