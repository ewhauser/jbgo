package gbash

import (
	"fmt"
	"strings"

	"github.com/ewhauser/gbash/commands"
	"github.com/ewhauser/gbash/host"
	"github.com/ewhauser/gbash/network"
	"github.com/ewhauser/gbash/policy"
)

// WithConfig overlays non-zero fields from cfg onto the runtime configuration.
//
// This is useful when configuration is assembled ahead of time and then applied
// as one option to [New].
func WithConfig(cfg *Config) Option {
	return func(target *Config) error {
		if cfg == nil {
			return nil
		}
		if cfg.FileSystem.Factory != nil || cfg.FileSystem.WorkingDir != "" {
			target.FileSystem = cfg.FileSystem
		}
		if cfg.Registry != nil {
			target.Registry = cfg.Registry
		}
		if cfg.Policy != nil {
			target.Policy = cfg.Policy
		}
		if cfg.BaseEnv != nil {
			target.BaseEnv = copyStringMap(cfg.BaseEnv)
		}
		if cfg.Host != nil {
			target.Host = cfg.Host
		}
		if cfg.Network != nil {
			networkCopy := *cfg.Network
			networkCopy.AllowedURLPrefixes = append([]string(nil), cfg.Network.AllowedURLPrefixes...)
			networkCopy.AllowedMethods = append([]Method(nil), cfg.Network.AllowedMethods...)
			target.Network = &networkCopy
		}
		if cfg.NetworkClient != nil {
			target.NetworkClient = cfg.NetworkClient
		}
		if cfg.Tracing.Mode != TraceOff || cfg.Tracing.OnEvent != nil {
			target.Tracing = cfg.Tracing
		}
		if cfg.Logger != nil {
			target.Logger = cfg.Logger
		}
		return nil
	}
}

// WithFileSystem replaces the session filesystem configuration.
func WithFileSystem(cfg FileSystemConfig) Option {
	return func(target *Config) error {
		target.FileSystem = cfg
		return nil
	}
}

// WithWorkspace mounts dir into the sandbox under a writable in-memory overlay
// and starts new sessions in that mounted directory.
//
// This is the intended happy-path option for embedding gbash against a real
// codebase or workspace on disk.
func WithWorkspace(dir string) Option {
	return func(target *Config) error {
		if strings.TrimSpace(dir) == "" {
			return fmt.Errorf("workspace directory must not be empty")
		}
		target.FileSystem = HostDirectoryFileSystem(dir, HostDirectoryOptions{})
		return nil
	}
}

// WithWorkingDir overrides the initial working directory for new sessions.
//
// This option composes with both the default in-memory sandbox and any explicit
// filesystem option such as [WithWorkspace] or [WithFileSystem].
func WithWorkingDir(dir string) Option {
	return func(target *Config) error {
		if strings.TrimSpace(dir) == "" {
			return fmt.Errorf("working directory must not be empty")
		}
		target.FileSystem.WorkingDir = dir
		return nil
	}
}

// WithRegistry replaces the command registry visible inside the sandbox.
func WithRegistry(registry commands.CommandRegistry) Option {
	return func(target *Config) error {
		target.Registry = registry
		return nil
	}
}

// WithPolicy replaces the sandbox policy implementation.
func WithPolicy(p policy.Policy) Option {
	return func(target *Config) error {
		target.Policy = p
		return nil
	}
}

// WithBaseEnv replaces the base environment inherited by each execution.
func WithBaseEnv(env map[string]string) Option {
	return func(target *Config) error {
		target.BaseEnv = copyStringMap(env)
		return nil
	}
}

// WithHost replaces the host adapter used by the runtime.
func WithHost(adapter host.Adapter) Option {
	return func(target *Config) error {
		target.Host = adapter
		return nil
	}
}

// WithHTTPAccess enables curl with a URL allowlist and the default HTTP policy.
//
// This is the simplest way to turn on network access. Requests are restricted
// to the provided URL prefixes, and all other HTTP settings use the defaults
// from the network package.
func WithHTTPAccess(prefixes ...string) Option {
	return func(target *Config) error {
		if len(prefixes) == 0 {
			return fmt.Errorf("at least one allowed URL prefix is required")
		}
		cleaned := make([]string, 0, len(prefixes))
		for _, prefix := range prefixes {
			if strings.TrimSpace(prefix) == "" {
				continue
			}
			cleaned = append(cleaned, prefix)
		}
		if len(cleaned) == 0 {
			return fmt.Errorf("at least one allowed URL prefix is required")
		}
		target.Network = &NetworkConfig{
			AllowedURLPrefixes: cleaned,
		}
		return nil
	}
}

// WithNetwork replaces the built-in HTTP client configuration used by curl.
//
// Use this option when you want to customize methods, limits, timeouts, or
// private-range denial behavior while still relying on the standard gbash HTTP
// client implementation.
func WithNetwork(cfg *NetworkConfig) Option {
	return func(target *Config) error {
		if cfg == nil {
			target.Network = nil
			return nil
		}
		cfgCopy := *cfg
		cfgCopy.AllowedURLPrefixes = append([]string(nil), cfg.AllowedURLPrefixes...)
		cfgCopy.AllowedMethods = append([]Method(nil), cfg.AllowedMethods...)
		target.Network = &cfgCopy
		return nil
	}
}

// WithNetworkClient injects a fully custom HTTP client for curl.
//
// This is the advanced escape hatch for tests and unusual embedding scenarios
// where the built-in allowlist-based client is not sufficient.
func WithNetworkClient(client network.Client) Option {
	return func(target *Config) error {
		target.NetworkClient = client
		return nil
	}
}

// WithTracing replaces the runtime tracing configuration.
func WithTracing(cfg TraceConfig) Option {
	return func(target *Config) error {
		target.Tracing = cfg
		return nil
	}
}

// WithLogger installs a top-level execution lifecycle log callback.
func WithLogger(callback LogCallback) Option {
	return func(target *Config) error {
		target.Logger = callback
		return nil
	}
}
