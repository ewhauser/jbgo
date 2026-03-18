package runtime

import (
	"github.com/ewhauser/gbash/commands"
	"github.com/ewhauser/gbash/network"
	"github.com/ewhauser/gbash/policy"
)

// Option configures a runtime before initialization.
type Option func(*Config) error

// WithConfig overlays non-zero values from cfg onto the runtime config.
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
			target.BaseEnv = cfg.BaseEnv
		}
		if cfg.Network != nil {
			target.Network = cfg.Network
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

func WithFileSystem(cfg FileSystemConfig) Option {
	return func(target *Config) error {
		target.FileSystem = cfg
		return nil
	}
}

func WithRegistry(registry commands.CommandRegistry) Option {
	return func(target *Config) error {
		target.Registry = registry
		return nil
	}
}

func WithPolicy(p policy.Policy) Option {
	return func(target *Config) error {
		target.Policy = p
		return nil
	}
}

func WithBaseEnv(env map[string]string) Option {
	return func(target *Config) error {
		target.BaseEnv = env
		return nil
	}
}

func WithNetworkConfig(cfg *network.Config) Option {
	return func(target *Config) error {
		target.Network = cfg
		return nil
	}
}

func WithNetworkClient(client network.Client) Option {
	return func(target *Config) error {
		target.NetworkClient = client
		return nil
	}
}

func WithTracing(cfg TraceConfig) Option {
	return func(target *Config) error {
		target.Tracing = cfg
		return nil
	}
}

func WithLogger(callback LogCallback) Option {
	return func(target *Config) error {
		target.Logger = callback
		return nil
	}
}

func resolveConfig(opts []Option) (Config, error) {
	var cfg Config
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&cfg); err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}
