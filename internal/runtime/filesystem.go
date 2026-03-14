package runtime

import (
	"strings"

	gbfs "github.com/ewhauser/gbash/fs"
)

// FileSystemConfig describes how a runtime session gets its sandbox filesystem
// and what working directory it should start in.
type FileSystemConfig struct {
	Factory    gbfs.Factory
	WorkingDir string
}

// HostProjectOptions configures the high-level host-project sandbox helper.
type HostProjectOptions struct {
	VirtualRoot      string
	MaxFileReadBytes int64
}

// ReadWriteDirectoryOptions configures a host directory mounted as the mutable
// sandbox root.
type ReadWriteDirectoryOptions struct {
	MaxFileReadBytes int64
}

// InMemoryFileSystem returns the default session filesystem setup.
func InMemoryFileSystem() FileSystemConfig {
	return FileSystemConfig{
		Factory:    gbfs.Memory(),
		WorkingDir: defaultHomeDir,
	}
}

// CustomFileSystem wires an arbitrary filesystem factory into the runtime.
func CustomFileSystem(factory gbfs.Factory, workingDir string) FileSystemConfig {
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
		virtualRoot = gbfs.DefaultHostVirtualRoot
	}
	return FileSystemConfig{
		Factory: gbfs.Overlay(gbfs.Host(gbfs.HostOptions{
			Root:             root,
			VirtualRoot:      virtualRoot,
			MaxFileReadBytes: opts.MaxFileReadBytes,
		})),
		WorkingDir: virtualRoot,
	}
}

// ReadWriteDirectoryFileSystem mounts root as the mutable sandbox root.
//
// This mirrors just-bash's direct read-write host filesystem mode: sandbox
// paths are rooted at "/", and mutations persist back to the host directory.
func ReadWriteDirectoryFileSystem(root string, opts ReadWriteDirectoryOptions) FileSystemConfig {
	return FileSystemConfig{
		Factory: gbfs.ReadWrite(gbfs.ReadWriteOptions{
			Root:             root,
			MaxFileReadBytes: opts.MaxFileReadBytes,
		}),
		WorkingDir: "/",
	}
}

func (cfg FileSystemConfig) resolved() FileSystemConfig {
	if cfg.Factory == nil {
		cfg.Factory = gbfs.Memory()
	}
	cfg.WorkingDir = strings.TrimSpace(cfg.WorkingDir)
	if cfg.WorkingDir == "" {
		cfg.WorkingDir = defaultHomeDir
	}
	cfg.WorkingDir = gbfs.Clean(cfg.WorkingDir)
	return cfg
}
