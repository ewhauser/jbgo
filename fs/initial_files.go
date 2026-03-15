package fs

import (
	"context"
	stdfs "io/fs"
	"time"
)

// LazyFileProvider returns file contents on first materialization.
type LazyFileProvider func(context.Context) ([]byte, error)

// InitialFile describes an eagerly or lazily seeded in-memory file.
//
// When Lazy is nil, Content is used as the initial file contents. A nil Content
// value in that case represents an empty file.
type InitialFile struct {
	Content []byte
	Lazy    LazyFileProvider
	Mode    stdfs.FileMode
	ModTime time.Time
}

// InitialFiles maps sandbox paths to seeded file contents.
type InitialFiles map[string]InitialFile

type seededMemoryFactory struct {
	files InitialFiles
}

func (f seededMemoryFactory) New(context.Context) (FileSystem, error) {
	mem := NewMemory()
	if err := mem.seedInitialFiles(f.files, time.Now().UTC()); err != nil {
		return nil, err
	}
	return mem, nil
}

// SeededMemory returns a factory that creates a fresh in-memory filesystem
// preloaded with the provided initial files.
func SeededMemory(files InitialFiles) Factory {
	copied := make(InitialFiles, len(files))
	for name, file := range files {
		copied[name] = InitialFile{
			Content: append([]byte(nil), file.Content...),
			Lazy:    file.Lazy,
			Mode:    file.Mode,
			ModTime: file.ModTime,
		}
	}
	return seededMemoryFactory{files: copied}
}
