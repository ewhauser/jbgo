package runtime

import (
	"fmt"

	"github.com/ewhauser/gbash/commands"
	"github.com/ewhauser/gbash/network"
	"github.com/ewhauser/gbash/policy"
	"github.com/ewhauser/gbash/shell"
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
		if cfg.Engine != nil {
			target.Engine = cfg.Engine
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

func WithEngine(engine shell.Engine) Option {
	return func(target *Config) error {
		target.Engine = engine
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

func resolveConfig(args []any) (Config, error) {
	var cfg Config
	for _, arg := range args {
		switch value := arg.(type) {
		case nil:
			continue
		case Option:
			if err := value(&cfg); err != nil {
				return Config{}, err
			}
		case *Config:
			if err := WithConfig(value)(&cfg); err != nil {
				return Config{}, err
			}
		default:
			return Config{}, fmt.Errorf("runtime: unsupported New option %T", arg)
		}
	}
	return cfg, nil
}
