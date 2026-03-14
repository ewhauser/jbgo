package commandutil

import (
	"io"
	stdfs "io/fs"
	"os"

	gbfs "github.com/ewhauser/gbash/fs"
)

type RedirectMetadata interface {
	RedirectPath() string
	RedirectFlags() int
	RedirectOffset() int64
}

type redirectedFile struct {
	file   gbfs.File
	path   string
	flag   int
	offset int64
}

func WrapRedirectedFile(file gbfs.File, path string, flag int) io.ReadWriteCloser {
	if file == nil {
		return nil
	}
	return &redirectedFile{
		file: file,
		path: path,
		flag: flag,
	}
}

func (f *redirectedFile) Read(p []byte) (int, error) {
	n, err := f.file.Read(p)
	f.offset += int64(n)
	return n, err
}

func (f *redirectedFile) Write(p []byte) (int, error) {
	if f.flag&os.O_APPEND != 0 {
		if info, err := f.file.Stat(); err == nil {
			f.offset = info.Size()
		}
	}
	n, err := f.file.Write(p)
	f.offset += int64(n)
	return n, err
}

func (f *redirectedFile) Close() error {
	return f.file.Close()
}

func (f *redirectedFile) Stat() (stdfs.FileInfo, error) {
	return f.file.Stat()
}

func (f *redirectedFile) RedirectPath() string {
	return f.path
}

func (f *redirectedFile) RedirectFlags() int {
	return f.flag
}

func (f *redirectedFile) RedirectOffset() int64 {
	return f.offset
}

var _ gbfs.File = (*redirectedFile)(nil)
var _ RedirectMetadata = (*redirectedFile)(nil)
