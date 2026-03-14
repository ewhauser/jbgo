package cli

import (
	"io"
	"strings"

	"github.com/ewhauser/gbash"
)

// Config controls how [Run] presents and configures the shared gbash CLI
// frontend.
type Config struct {
	// Name is the binary name shown in help text, version output, and runtime
	// error messages.
	Name string

	// Build contains the embedded build metadata shown by --version.
	Build *BuildInfo

	// BaseOptions are always applied when constructing the gbash runtime before
	// any CLI filesystem flags are interpreted.
	BaseOptions []gbash.Option

	// TTYDetector overrides how stdin terminal detection is performed. When nil,
	// [Run] uses the default os.File-based detector.
	TTYDetector func(io.Reader) bool
}

// BuildInfo describes the build metadata rendered by --version.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
	BuiltBy string
}

func normalizeConfig(cfg Config) Config {
	cfg.Name = strings.TrimSpace(cfg.Name)
	if cfg.Name == "" {
		cfg.Name = "gbash"
	}
	return cfg
}
