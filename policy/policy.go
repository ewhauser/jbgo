package policy

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
)

type FileAction string

const (
	FileActionRead     FileAction = "read"
	FileActionWrite    FileAction = "write"
	FileActionStat     FileAction = "stat"
	FileActionLstat    FileAction = "lstat"
	FileActionReadlink FileAction = "readlink"
	FileActionReadDir  FileAction = "readdir"
	FileActionMkdir    FileAction = "mkdir"
	FileActionRemove   FileAction = "remove"
	FileActionRename   FileAction = "rename"
)

type NetworkMode string

const (
	NetworkDisabled NetworkMode = "disabled"
)

type SymlinkMode string

const (
	SymlinkDeny   SymlinkMode = "deny"
	SymlinkFollow SymlinkMode = "follow"
)

type Limits struct {
	MaxCommandCount      int64
	MaxGlobOperations    int64
	MaxLoopIterations    int64
	MaxSubstitutionDepth int64
	MaxStdoutBytes       int64
	MaxStderrBytes       int64
	MaxFileBytes         int64
}

type Policy interface {
	AllowCommand(ctx context.Context, name string, argv []string) error
	AllowBuiltin(ctx context.Context, name string, argv []string) error
	AllowPath(ctx context.Context, action FileAction, target string) error
	Limits() Limits
	SymlinkMode() SymlinkMode
}

type Config struct {
	AllowedCommands []string
	AllowedBuiltins []string
	ReadRoots       []string
	WriteRoots      []string
	Limits          Limits
	NetworkMode     NetworkMode
	SymlinkMode     SymlinkMode
}

type Static struct {
	allowedCommands []string
	allowedBuiltins []string
	readRoots       []string
	writeRoots      []string
	limits          Limits
	networkMode     NetworkMode
	symlinkMode     SymlinkMode
}

func NewStatic(cfg *Config) *Static {
	if cfg == nil {
		cfg = &Config{}
	}
	readRoots := normalizeRoots(cfg.ReadRoots)
	if len(readRoots) == 0 {
		readRoots = []string{"/"}
	}

	writeRoots := normalizeRoots(cfg.WriteRoots)
	if len(writeRoots) == 0 {
		writeRoots = []string{"/"}
	}
	symlinkMode := cfg.SymlinkMode
	if symlinkMode == "" {
		symlinkMode = SymlinkDeny
	}

	return &Static{
		allowedCommands: normalizeNames(cfg.AllowedCommands),
		allowedBuiltins: normalizeNames(cfg.AllowedBuiltins),
		readRoots:       readRoots,
		writeRoots:      writeRoots,
		limits:          cfg.Limits,
		networkMode:     cfg.NetworkMode,
		symlinkMode:     symlinkMode,
	}
}

func (p *Static) AllowCommand(_ context.Context, name string, _ []string) error {
	if len(p.allowedCommands) == 0 {
		return nil
	}

	if slices.Contains(p.allowedCommands, name) {
		return nil
	}

	return &DeniedError{
		Subject: fmt.Sprintf("command %q", name),
		Reason:  "not in allowlist",
	}
}

func (p *Static) AllowBuiltin(_ context.Context, name string, _ []string) error {
	if len(p.allowedBuiltins) == 0 {
		return nil
	}

	if slices.Contains(p.allowedBuiltins, name) {
		return nil
	}

	return &DeniedError{
		Subject: fmt.Sprintf("builtin %q", name),
		Reason:  "not in allowlist",
	}
}

func (p *Static) AllowPath(_ context.Context, action FileAction, target string) error {
	target = cleanAbs(target)

	var roots []string
	switch action {
	case FileActionWrite, FileActionMkdir, FileActionRemove, FileActionRename:
		roots = p.writeRoots
	default:
		roots = p.readRoots
	}

	for _, root := range roots {
		if underRoot(root, target) {
			return nil
		}
	}

	return &DeniedError{
		Subject: fmt.Sprintf("%s %q", action, target),
		Reason:  "outside allowed roots",
	}
}

func (p *Static) Limits() Limits {
	return p.limits
}

func (p *Static) SymlinkMode() SymlinkMode {
	return p.symlinkMode
}

type DeniedError struct {
	Subject string
	Reason  string
}

func (e *DeniedError) Error() string {
	return fmt.Sprintf("%s denied: %s", e.Subject, e.Reason)
}

func IsDenied(err error) bool {
	var denied *DeniedError
	return errors.As(err, &denied)
}

func normalizeNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}

	out := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

func normalizeRoots(roots []string) []string {
	if len(roots) == 0 {
		return nil
	}

	out := make([]string, 0, len(roots))
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		root = cleanAbs(root)
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		out = append(out, root)
	}
	slices.Sort(out)
	return out
}

func cleanAbs(name string) string {
	if name == "" {
		return "/"
	}
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	name = path.Clean(name)
	if name == "." {
		return "/"
	}
	return name
}

func underRoot(root, target string) bool {
	if root == "/" {
		return true
	}
	return target == root || strings.HasPrefix(target, root+"/")
}
