package runtime

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/network"
)

func TestCurlIsUnavailableByDefault(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "curl https://example.com\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 127 {
		t.Fatalf("ExitCode = %d, want 127", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "command not found") {
		t.Fatalf("Stderr = %q, want command-not-found error", result.Stderr)
	}
}

func TestCurlAllowsConfiguredOrigin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello from network"))
	}))
	defer server.Close()

	rt := newRuntime(t, &Config{
		Network: &network.Config{
			AllowedURLPrefixes: []string{server.URL},
			DenyPrivateRanges:  false,
		},
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "curl " + server.URL + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "hello from network"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCurlBlocksDisallowedOrigin(t *testing.T) {
	rt := newRuntime(t, &Config{
		Network: &network.Config{
			AllowedURLPrefixes: []string{"https://api.example.com"},
		},
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "curl https://other.example.com/data\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "allowlist") {
		t.Fatalf("Stderr = %q, want allowlist denial", result.Stderr)
	}
}

func TestCurlBlocksDisallowedMethodByDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Method))
	}))
	defer server.Close()

	rt := newRuntime(t, &Config{
		Network: &network.Config{
			AllowedURLPrefixes: []string{server.URL},
			DenyPrivateRanges:  false,
		},
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "curl -X POST -d body " + server.URL + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 3 {
		t.Fatalf("ExitCode = %d, want 3", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "not allowed") {
		t.Fatalf("Stderr = %q, want method denial", result.Stderr)
	}
}

func TestCurlAllowsConfiguredPostMethod(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, _ = w.Write([]byte(r.Method + ":" + string(body)))
	}))
	defer server.Close()

	rt := newRuntime(t, &Config{
		Network: &network.Config{
			AllowedURLPrefixes: []string{server.URL},
			AllowedMethods:     []network.Method{network.MethodGet, network.MethodHead, network.MethodPost},
			DenyPrivateRanges:  false,
		},
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "curl -X POST -d body " + server.URL + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "POST:body"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCurlRevalidatesRedirectTargets(t *testing.T) {
	redirectTarget := "https://other.example.com/blocked"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget, http.StatusFound)
	}))
	defer server.Close()

	rt := newRuntime(t, &Config{
		Network: &network.Config{
			AllowedURLPrefixes: []string{server.URL},
			DenyPrivateRanges:  false,
		},
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "curl " + server.URL + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 47 {
		t.Fatalf("ExitCode = %d, want 47", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "Redirect target not in allow-list") {
		t.Fatalf("Stderr = %q, want redirect denial", result.Stderr)
	}
}

func TestCurlEnforcesResponseSizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", 64)))
	}))
	defer server.Close()

	rt := newRuntime(t, &Config{
		Network: &network.Config{
			AllowedURLPrefixes: []string{server.URL},
			MaxResponseBytes:   8,
			DenyPrivateRanges:  false,
		},
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "curl " + server.URL + "\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "too large") {
		t.Fatalf("Stderr = %q, want response-size error", result.Stderr)
	}
}

func TestCurlCanWriteResponseToSandboxFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("saved"))
	}))
	defer server.Close()

	rt := newRuntime(t, &Config{
		Network: &network.Config{
			AllowedURLPrefixes: []string{server.URL},
			DenyPrivateRanges:  false,
		},
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "curl -o /tmp/out.txt " + server.URL + "\n cat /tmp/out.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "saved"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
