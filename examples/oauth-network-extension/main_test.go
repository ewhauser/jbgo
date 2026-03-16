package main

import (
	"context"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/network"
)

func TestRunDemoInjectsOAuthWithoutLeakingSecret(t *testing.T) {
	t.Parallel()
	report, err := runDemo(context.Background())
	if err != nil {
		t.Fatalf("runDemo() error = %v", err)
	}

	if got, want := len(report.Scenarios), 2; got != want {
		t.Fatalf("scenario count = %d, want %d", got, want)
	}
	if got, want := report.ScriptPath, demoScriptPath; got != want {
		t.Fatalf("ScriptPath = %q, want %q", got, want)
	}
	if !strings.Contains(report.ScriptSource, "curl -fsS \\") {
		t.Fatalf("ScriptSource = %q, want embedded shell script", report.ScriptSource)
	}

	baseline := report.Scenarios[0]
	if got, want := baseline.Name, baselineScenarioName; got != want {
		t.Fatalf("baseline name = %q, want %q", got, want)
	}
	if !baseline.Response.OK {
		t.Fatalf("baseline Response.OK = false, want true")
	}
	if got, want := baseline.Response.AuthenticatedAs, "demo-service-account"; got != want {
		t.Fatalf("baseline AuthenticatedAs = %q, want %q", got, want)
	}
	if got, want := baseline.Response.RequestID, requestID; got != want {
		t.Fatalf("baseline RequestID = %q, want %q", got, want)
	}
	if !baseline.Audit.AuthorizationInjected {
		t.Fatal("baseline AuthorizationInjected = false, want true")
	}
	if baseline.Audit.SandboxAuthorizationIn {
		t.Fatal("baseline SandboxAuthorizationIn = true, want false")
	}
	if baseline.Audit.AuthorizationOverrideApplied {
		t.Fatal("baseline AuthorizationOverrideApplied = true, want false")
	}
	if got, want := baseline.Audit.AuthorizationSource, tokenRef; got != want {
		t.Fatalf("baseline AuthorizationSource = %q, want %q", got, want)
	}
	if !baseline.ServerRequest.AuthorizationPresent {
		t.Fatal("baseline AuthorizationPresent = false, want true")
	}
	if !baseline.ServerRequest.AuthorizationValid {
		t.Fatal("baseline AuthorizationValid = false, want true")
	}
	if got, want := baseline.ServerRequest.RequestID, requestID; got != want {
		t.Fatalf("baseline server request id = %q, want %q", got, want)
	}
	if baseline.SecretVisibleInStdout {
		t.Fatal("baseline SecretVisibleInStdout = true, want false")
	}
	if baseline.SecretVisibleInTrace {
		t.Fatal("baseline SecretVisibleInTrace = true, want false")
	}
	assertTraceArgv(t, baseline.TraceArgv, []string{"curl", "-fsS", "-H", "X-Request-ID: " + requestID, demoRequestURL})

	override := report.Scenarios[1]
	if got, want := override.Name, overrideScenarioName; got != want {
		t.Fatalf("override name = %q, want %q", got, want)
	}
	if !override.Response.OK {
		t.Fatalf("override Response.OK = false, want true")
	}
	if got, want := override.Response.AuthenticatedAs, "demo-service-account"; got != want {
		t.Fatalf("override AuthenticatedAs = %q, want %q", got, want)
	}
	if got, want := override.Response.RequestID, overrideAttemptID; got != want {
		t.Fatalf("override RequestID = %q, want %q", got, want)
	}
	if !override.Audit.AuthorizationInjected {
		t.Fatal("override AuthorizationInjected = false, want true")
	}
	if !override.Audit.SandboxAuthorizationIn {
		t.Fatal("override SandboxAuthorizationIn = false, want true")
	}
	if !override.Audit.AuthorizationOverrideApplied {
		t.Fatal("override AuthorizationOverrideApplied = false, want true")
	}
	if got, want := override.Audit.AuthorizationSource, tokenRef; got != want {
		t.Fatalf("override AuthorizationSource = %q, want %q", got, want)
	}
	if !override.ServerRequest.AuthorizationPresent {
		t.Fatal("override AuthorizationPresent = false, want true")
	}
	if !override.ServerRequest.AuthorizationValid {
		t.Fatal("override AuthorizationValid = false, want true")
	}
	if got, want := override.ServerRequest.RequestID, overrideAttemptID; got != want {
		t.Fatalf("override server request id = %q, want %q", got, want)
	}
	if override.SecretVisibleInStdout {
		t.Fatal("override SecretVisibleInStdout = true, want false")
	}
	if override.SecretVisibleInTrace {
		t.Fatal("override SecretVisibleInTrace = true, want false")
	}
	assertTraceArgv(t, override.TraceArgv, []string{"curl", "-fsS", "-H", "Authorization: " + sandboxForgedAuth, "-H", "X-Request-ID: " + overrideAttemptID, demoRequestURL})
}

func TestOAuthInjectingClientAuditsLowercaseAuthorizationHeader(t *testing.T) {
	t.Parallel()
	vault := newDemoVault()
	server := newDemoAPIServer(vault)
	defer server.Close()

	client, err := newOAuthInjectingClient(server.URL(), vault)
	if err != nil {
		t.Fatalf("newOAuthInjectingClient() error = %v", err)
	}

	resp, err := client.Do(context.Background(), &network.Request{
		URL: demoRequestURL,
		Headers: map[string]string{
			"authorization": "Bearer sandbox-lowercase-token",
			"x-request-id":  "sandbox-lowercase-44",
		},
	})
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if got, want := resp.StatusCode, 200; got != want {
		t.Fatalf("StatusCode = %d, want %d", got, want)
	}

	history := client.History()
	if got, want := len(history), 1; got != want {
		t.Fatalf("client audit history length = %d, want %d", got, want)
	}
	if !history[0].SandboxAuthorizationIn {
		t.Fatal("SandboxAuthorizationIn = false, want true")
	}
	if !history[0].AuthorizationOverrideApplied {
		t.Fatal("AuthorizationOverrideApplied = false, want true")
	}
	if got, want := history[0].RequestID, "sandbox-lowercase-44"; got != want {
		t.Fatalf("RequestID = %q, want %q", got, want)
	}

	serverHistory := server.History()
	if got, want := len(serverHistory), 1; got != want {
		t.Fatalf("server history length = %d, want %d", got, want)
	}
	if !serverHistory[0].AuthorizationValid {
		t.Fatal("AuthorizationValid = false, want true")
	}
	if got, want := serverHistory[0].RequestID, "sandbox-lowercase-44"; got != want {
		t.Fatalf("server request id = %q, want %q", got, want)
	}
}

func assertTraceArgv(t *testing.T, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("TraceArgv length = %d, want %d (%q)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("TraceArgv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
