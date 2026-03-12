package shell

import (
	"context"
	"errors"
	"io"
	"maps"
)

type ResolverMode string

const (
	ResolverRegistryOnly             ResolverMode = "registry-only"
	ResolverRegistryThenHostFallback ResolverMode = "registry-then-host-fallback"
)

var ErrHostCommandNotFound = errors.New("host command not found")

type HostExecutionRequest struct {
	Path   string
	Args   []string
	Env    map[string]string
	Dir    string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type HostExecutionResult struct {
	ExitCode     int
	ResolvedPath string
}

type HostExecutor interface {
	Run(context.Context, *HostExecutionRequest) (*HostExecutionResult, error)
}

func CloneReservedNames(values map[string]struct{}) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	maps.Copy(out, values)
	return out
}
