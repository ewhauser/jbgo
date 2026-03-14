package runtime

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/ewhauser/gbash/commands"
	"github.com/ewhauser/gbash/network"
)

type stubNetworkClient struct{}

func (stubNetworkClient) Do(_ context.Context, req *network.Request) (*network.Response, error) {
	return &network.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Headers:    map[string]string{"Content-Type": "text/plain"},
		Body:       []byte("fetched"),
		URL:        req.URL,
	}, nil
}

func TestCustomCommandInvocationCapabilities(t *testing.T) {
	registry := commands.DefaultRegistry()
	if err := registry.Register(commands.DefineCommand("capsprobe", func(ctx context.Context, inv *commands.Invocation) error {
		if inv.Cwd == "" {
			return fmt.Errorf("missing cwd")
		}
		if inv.Fetch == nil {
			return fmt.Errorf("missing fetch")
		}
		if inv.Limits.MaxStdoutBytes == 0 {
			return fmt.Errorf("missing limits")
		}

		resp, err := inv.Fetch(ctx, &commands.FetchRequest{
			Method: "GET",
			URL:    "https://example.test/status",
		})
		if err != nil {
			return err
		}

		file, err := inv.FS.OpenFile(ctx, "artifact.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		if _, err := file.Write([]byte(inv.Args[0] + "\n")); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}

		_, err = fmt.Fprintf(inv.Stdout, "%s|%s|%s|registered=%t\n",
			inv.Env["FOO"],
			inv.Cwd,
			string(resp.Body),
			slices.Contains(inv.GetRegisteredCommands(), "capsprobe"),
		)
		return err
	})); err != nil {
		t.Fatalf("Register(capsprobe) error = %v", err)
	}

	session := newSession(t, &Config{
		Registry:      registry,
		NetworkClient: stubNetworkClient{},
	})

	result := mustExecSession(t, session, "FOO=bar capsprobe payload\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "bar|/home/agent|fetched|registered=true\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := string(readSessionFile(t, session, "/home/agent/artifact.txt")), "payload\n"; got != want {
		t.Fatalf("artifact = %q, want %q", got, want)
	}
}
