package runtime

import (
	"context"
	"io"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ewhauser/gbash/commands"
	pubhost "github.com/ewhauser/gbash/host"
	"github.com/ewhauser/gbash/internal/shellstate"
)

type fakeHostAdapter struct {
	defaults  pubhost.Defaults
	platform  pubhost.Platform
	meta      pubhost.ExecutionMeta
	pipeCalls atomic.Int32
}

func (f *fakeHostAdapter) Defaults(context.Context) (pubhost.Defaults, error) {
	return pubhost.Defaults{Env: copyStringMap(f.defaults.Env)}, nil
}

func (f *fakeHostAdapter) Platform() pubhost.Platform {
	platform := f.platform
	platform.PathExtensions = append([]string(nil), platform.PathExtensions...)
	return platform
}

func (f *fakeHostAdapter) ExecutionMeta(context.Context) (pubhost.ExecutionMeta, error) {
	return f.meta, nil
}

func (f *fakeHostAdapter) NewPipe() (io.ReadCloser, io.WriteCloser, error) {
	f.pipeCalls.Add(1)
	return os.Pipe()
}

func TestHostAdapterBaseEnvPrecedenceAndReplaceEnv(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{
		Host: &fakeHostAdapter{
			defaults: pubhost.Defaults{Env: map[string]string{
				"HOME":      "/host-home",
				"FROM_HOST": "host",
			}},
			platform: pubhost.Platform{
				OS:                   "linux",
				Arch:                 "x86_64",
				OSType:               "linux-gnu",
				RequireExecutableBit: true,
				Uname: pubhost.Uname{
					SysName:         "Linux",
					NodeName:        "fake-linux",
					Release:         "6.0.0",
					Version:         "fake-version",
					Machine:         "x86_64",
					OperatingSystem: "GNU/Linux",
				},
			},
			meta: pubhost.ExecutionMeta{PID: 41, PPID: 7, ProcessGroup: 99},
		},
		BaseEnv: map[string]string{
			"HOME":        "/config-home",
			"FROM_CONFIG": "config",
		},
	})

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Env: map[string]string{
			"HOME":     "/request-home",
			"FROM_REQ": "request",
		},
		Script: "printf '%s|%s|%s|%s\\n' \"$HOME\" \"$FROM_HOST\" \"$FROM_CONFIG\" \"$FROM_REQ\"\n",
	})
	if err != nil {
		t.Fatalf("Exec(precedence) error = %v", err)
	}
	if got, want := result.Stdout, "/request-home|host|config|request\n"; got != want {
		t.Fatalf("precedence stdout = %q, want %q", got, want)
	}

	result, err = session.Exec(context.Background(), &ExecutionRequest{
		ReplaceEnv: true,
		Env: map[string]string{
			"HOME": "/clean-home",
		},
		Script: "printf '%s|%s|%s|%s|%s\\n' \"$HOME\" \"${FROM_HOST-}\" \"${FROM_CONFIG-}\" \"$PATH\" \"$SHELL\"\n",
	})
	if err != nil {
		t.Fatalf("Exec(replace env) error = %v", err)
	}
	if got, want := result.Stdout, "/clean-home|||/usr/bin:/bin|/bin/sh\n"; got != want {
		t.Fatalf("replace-env stdout = %q, want %q", got, want)
	}
}

func TestHostAdapterControlsPlatformAndProcessMetadata(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{
		Host: fakeWindowsHost(),
		Registry: registryWithCommands(t,
			commands.DefineCommand("pgrp-probe", func(ctx context.Context, inv *commands.Invocation) error {
				pgrp, ok := shellstate.ProcessGroupFromContext(ctx)
				if !ok {
					_, err := io.WriteString(inv.Stdout, "missing\n")
					return err
				}
				_, err := io.WriteString(inv.Stdout, strconv.Itoa(pgrp)+"\n")
				return err
			}),
		),
	})

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script: "" +
			"printf '%s\\n' \"$mixed\" \"$OSTYPE\" \"$(uname -s)\" \"$(uname -m)\" \"$(hostname)\" \"$(arch)\" \"$$\" \"$PPID\"\n" +
			"pgrp-probe\n",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if got, want := lines, []string{
		"case-folded",
		"msys",
		"Windows_NT",
		"x86_64",
		"fake-win-host",
		"x86_64",
		"42",
		"7",
		"99",
	}; len(got) != len(want) {
		t.Fatalf("stdout lines = %#v, want %#v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("stdout line %d = %q, want %q; full stdout=%q", i, got[i], want[i], result.Stdout)
			}
		}
	}
}

func TestHostAdapterControlsLookupAndPipeFactories(t *testing.T) {
	t.Parallel()

	windowsHost := fakeWindowsHost()
	registry := registryWithCommands(t,
		commands.DefineCommand("plain", func(_ context.Context, inv *commands.Invocation) error {
			_, err := io.WriteString(inv.Stdout, "plain\n")
			return err
		}),
		commands.DefineCommand("ext.cmd", func(_ context.Context, inv *commands.Invocation) error {
			_, err := io.WriteString(inv.Stdout, "ext\n")
			return err
		}),
	)
	windowsSession := newSession(t, &Config{
		Host:     windowsHost,
		Registry: registry,
		BaseEnv: map[string]string{
			"PATH":    "/host-bin",
			"PATHEXT": ".CMD",
		},
	})
	writeStubCommandFile(t, windowsSession, "/host-bin/plain", "plain", 0o644)
	writeStubCommandFile(t, windowsSession, "/host-bin/ext.cmd", "ext.cmd", 0o644)

	result, err := windowsSession.Exec(context.Background(), &ExecutionRequest{
		Script: "" +
			"plain\n" +
			"ext\n",
	})
	if err != nil {
		t.Fatalf("Exec(windows host) error = %v", err)
	}
	if got, want := result.Stdout, "plain\next\n"; got != want {
		t.Fatalf("windows stdout = %q, want %q", got, want)
	}

	stdinHost := fakeWindowsHost()
	stdinSession := newSession(t, &Config{Host: stdinHost})
	if _, err := stdinSession.Exec(context.Background(), &ExecutionRequest{
		Stdin:  strings.NewReader("stdin\n"),
		Script: "cat >/dev/null\n",
	}); err != nil {
		t.Fatalf("Exec(stdin) error = %v", err)
	}
	if got := stdinHost.pipeCalls.Load(); got == 0 {
		t.Fatalf("stdin execution did not use host pipe factory")
	}

	pipelineHost := fakeWindowsHost()
	pipelineSession := newSession(t, &Config{Host: pipelineHost})
	if _, err := pipelineSession.Exec(context.Background(), &ExecutionRequest{
		Script: "printf 'pipe\\n' | cat >/dev/null\n",
	}); err != nil {
		t.Fatalf("Exec(pipeline) error = %v", err)
	}
	if got := pipelineHost.pipeCalls.Load(); got == 0 {
		t.Fatalf("pipeline execution did not use host pipe factory")
	}

	procSubstHost := fakeWindowsHost()
	procSubstSession := newSession(t, &Config{Host: procSubstHost})
	if _, err := procSubstSession.Exec(context.Background(), &ExecutionRequest{
		Script: "cat < <(printf 'procsubst\\n') >/dev/null\n",
	}); err != nil {
		t.Fatalf("Exec(process substitution) error = %v", err)
	}
	if got := procSubstHost.pipeCalls.Load(); got == 0 {
		t.Fatalf("process substitution did not use host pipe factory")
	}

	linuxSession := newSession(t, &Config{
		Host: &fakeHostAdapter{
			defaults: pubhost.Defaults{Env: map[string]string{
				"HOME": "/home/fake",
				"PATH": defaultPath,
			}},
			platform: pubhost.Platform{
				OS:                   "linux",
				Arch:                 "x86_64",
				OSType:               "linux-gnu",
				RequireExecutableBit: true,
				Uname: pubhost.Uname{
					SysName:         "Linux",
					NodeName:        "fake-linux",
					Release:         "6.0.0",
					Version:         "fake-version",
					Machine:         "x86_64",
					OperatingSystem: "GNU/Linux",
				},
			},
			meta: pubhost.ExecutionMeta{PID: 8, PPID: 3, ProcessGroup: 11},
		},
		Registry: registryWithCommands(t,
			commands.DefineCommand("plain", func(_ context.Context, inv *commands.Invocation) error {
				_, err := io.WriteString(inv.Stdout, "plain\n")
				return err
			}),
		),
		BaseEnv: map[string]string{
			"PATH": "/host-bin",
		},
	})
	writeStubCommandFile(t, linuxSession, "/host-bin/plain", "plain", 0o644)

	result, err = linuxSession.Exec(context.Background(), &ExecutionRequest{
		Script: "plain\n",
	})
	if err != nil {
		t.Fatalf("Exec(linux host) error = %v", err)
	}
	if result.ExitCode == 0 {
		t.Fatalf("linux host unexpectedly executed non-executable command; stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}

func fakeWindowsHost() *fakeHostAdapter {
	return &fakeHostAdapter{
		defaults: pubhost.Defaults{Env: map[string]string{
			"HOME":  "/windows-home",
			"PATH":  defaultPath,
			"MIXED": "case-folded",
		}},
		platform: pubhost.Platform{
			OS:                   "windows",
			Arch:                 "x86_64",
			OSType:               "msys",
			EnvCaseInsensitive:   true,
			PathExtensions:       []string{".cmd"},
			RequireExecutableBit: false,
			Uname: pubhost.Uname{
				SysName:         "Windows_NT",
				NodeName:        "fake-win-host",
				Release:         "10.0.22621",
				Version:         "build-lab",
				Machine:         "x86_64",
				OperatingSystem: "MS/Windows",
			},
		},
		meta: pubhost.ExecutionMeta{PID: 42, PPID: 7, ProcessGroup: 99},
	}
}

func writeStubCommandFile(t testing.TB, session *Session, path, name string, mode os.FileMode) {
	t.Helper()

	writeSessionFile(t, session, path, []byte("# gbash virtual command stub: "+name+"\n"))
	if err := session.FileSystem().Chmod(context.Background(), path, mode); err != nil {
		t.Fatalf("Chmod(%q) error = %v", path, err)
	}
}
