package runtime

import (
	"strings"

	jbfs "github.com/ewhauser/jbgo/fs"
)

// FileSystemConfig describes how a runtime session gets its sandbox filesystem
// and what working directory it should start in.
type FileSystemConfig struct {
	Factory    jbfs.Factory
	WorkingDir string
}

// HostProjectOptions configures the high-level host-project sandbox helper.
type HostProjectOptions struct {
	VirtualRoot      string
	MaxFileReadBytes int64
}

// InMemoryFileSystem returns the default session filesystem setup.
func InMemoryFileSystem() FileSystemConfig {
	return FileSystemConfig{
		Factory:    jbfs.Memory(),
		WorkingDir: defaultHomeDir,
	}
}

// CustomFileSystem wires an arbitrary filesystem factory into the runtime.
func CustomFileSystem(factory jbfs.Factory, workingDir string) FileSystemConfig {
	return FileSystemConfig{
		Factory:    factory,
		WorkingDir: workingDir,
	}
}

// HostProjectFileSystem mounts root as a read-only project tree underneath an
// in-memory overlay and starts the session in that mounted directory.
func HostProjectFileSystem(root string, opts HostProjectOptions) FileSystemConfig {
	virtualRoot := strings.TrimSpace(opts.VirtualRoot)
	if virtualRoot == "" {
		virtualRoot = jbfs.DefaultHostVirtualRoot
	}
	return FileSystemConfig{
		Factory: jbfs.Overlay(jbfs.Host(jbfs.HostOptions{
			Root:             root,
			VirtualRoot:      virtualRoot,
			MaxFileReadBytes: opts.MaxFileReadBytes,
		})),
		WorkingDir: virtualRoot,
	}
}

func (cfg FileSystemConfig) resolved() FileSystemConfig {
	if cfg.Factory == nil {
		cfg.Factory = jbfs.Memory()
	}
	cfg.WorkingDir = strings.TrimSpace(cfg.WorkingDir)
	if cfg.WorkingDir == "" {
		cfg.WorkingDir = defaultHomeDir
	}
	cfg.WorkingDir = jbfs.Clean(cfg.WorkingDir)
	return cfg
}
