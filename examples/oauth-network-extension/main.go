package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/network"
	"github.com/ewhauser/gbash/trace"
)

const (
	demoRequestURL       = "https://crm.example.test/v1/profile"
	demoScriptPath       = "examples/oauth-network-extension/demo.sh"
	requestID            = "sandbox-demo-42"
	overrideAttemptID    = "sandbox-spoof-43"
	sandboxForgedAuth    = "Bearer sandbox-forged-token"
	tokenRef             = "vault://crm-api/oauth"
	baselineScenarioName = "host injects oauth"
	overrideScenarioName = "sandbox authorization is overridden"
)

//go:embed demo.sh
var demoScript string

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, stdout io.Writer) error {
	report, err := runDemo(ctx)
	if err != nil {
		return err
	}
	return renderReport(stdout, report)
}

func runDemo(ctx context.Context) (*demoReport, error) {
	vault := newDemoVault()
	server := newDemoAPIServer(vault)
	defer server.Close()

	client, err := newOAuthInjectingClient(server.URL(), vault)
	if err != nil {
		return nil, fmt.Errorf("create oauth client: %w", err)
	}

	rt, err := gbash.New(
		gbash.WithNetworkClient(client),
		gbash.WithTracing(gbash.TraceConfig{Mode: gbash.TraceRaw}),
	)
	if err != nil {
		return nil, fmt.Errorf("create runtime: %w", err)
	}

	specs := []scenarioSpec{
		{Name: baselineScenarioName, RequestID: requestID},
		{Name: overrideScenarioName, RequestID: overrideAttemptID},
	}

	result, err := rt.Run(ctx, &gbash.ExecutionRequest{
		Name:   "oauth-network-extension",
		Script: demoScript,
	})
	if err != nil {
		return nil, fmt.Errorf("run demo script: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("demo script exited with %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}

	responseRecords, err := decodeAPIResponses(result.Stdout)
	if err != nil {
		return nil, err
	}
	traceArgvByRequestID, err := collectCurlArgvByRequestID(result.Events)
	if err != nil {
		return nil, err
	}
	auditByRequestID := make(map[string]requestAudit)
	for _, audit := range client.History() {
		auditByRequestID[audit.RequestID] = audit
	}
	serverByRequestID := make(map[string]serverRequestAudit)
	for _, req := range server.History() {
		serverByRequestID[req.RequestID] = req
	}
	if len(responseRecords) != len(specs) {
		return nil, fmt.Errorf("decoded %d response records, want %d", len(responseRecords), len(specs))
	}

	scenarios := make([]scenarioReport, 0, len(specs))
	for _, spec := range specs {
		scenario, err := buildScenarioReport(spec, responseRecords, traceArgvByRequestID, auditByRequestID, serverByRequestID, vault.mustSecret().Token)
		if err != nil {
			return nil, err
		}
		scenarios = append(scenarios, scenario)
	}

	override := scenarios[1]
	if !override.Audit.SandboxAuthorizationIn {
		return nil, errors.New("demo failed: override scenario did not observe the sandbox authorization header")
	}
	if !override.Audit.AuthorizationOverrideApplied {
		return nil, errors.New("demo failed: extension did not override the sandbox authorization header")
	}

	return &demoReport{
		ScriptPath:   demoScriptPath,
		ScriptSource: strings.TrimSpace(demoScript),
		Scenarios:    scenarios,
	}, nil
}

type scenarioSpec struct {
	Name      string
	RequestID string
}

type apiResponseRecord struct {
	Raw      string
	Response apiResponse
}

func buildScenarioReport(
	spec scenarioSpec,
	responseRecords map[string]apiResponseRecord,
	traceArgvByRequestID map[string][]string,
	auditByRequestID map[string]requestAudit,
	serverByRequestID map[string]serverRequestAudit,
	token string,
) (scenarioReport, error) {
	responseRecord, ok := responseRecords[spec.RequestID]
	if !ok {
		return scenarioReport{}, fmt.Errorf("%s: missing response for request id %q", spec.Name, spec.RequestID)
	}
	traceArgv, ok := traceArgvByRequestID[spec.RequestID]
	if !ok {
		return scenarioReport{}, fmt.Errorf("%s: missing curl trace argv for request id %q", spec.Name, spec.RequestID)
	}
	audit, ok := auditByRequestID[spec.RequestID]
	if !ok {
		return scenarioReport{}, fmt.Errorf("%s: missing audit record for request id %q", spec.Name, spec.RequestID)
	}
	serverRequest, ok := serverByRequestID[spec.RequestID]
	if !ok {
		return scenarioReport{}, fmt.Errorf("%s: missing server record for request id %q", spec.Name, spec.RequestID)
	}

	report := scenarioReport{
		Name:                  spec.Name,
		CurlStdout:            responseRecord.Raw,
		TraceArgv:             traceArgv,
		Response:              responseRecord.Response,
		Audit:                 audit,
		ServerRequest:         serverRequest,
		SecretVisibleInStdout: strings.Contains(responseRecord.Raw, token),
		SecretVisibleInTrace:  strings.Contains(strings.Join(traceArgv, " "), token),
	}

	if report.Response.RequestID != spec.RequestID {
		return scenarioReport{}, fmt.Errorf("%s: response request id = %q, want %q", spec.Name, report.Response.RequestID, spec.RequestID)
	}
	if report.ServerRequest.RequestID != spec.RequestID {
		return scenarioReport{}, fmt.Errorf("%s: server request id = %q, want %q", spec.Name, report.ServerRequest.RequestID, spec.RequestID)
	}
	if !report.Audit.AuthorizationInjected {
		return scenarioReport{}, fmt.Errorf("%s: oauth header was not injected", spec.Name)
	}
	if !report.ServerRequest.AuthorizationValid {
		return scenarioReport{}, fmt.Errorf("%s: server did not receive the injected oauth token", spec.Name)
	}
	if report.SecretVisibleInStdout || report.SecretVisibleInTrace {
		return scenarioReport{}, fmt.Errorf("%s: oauth token leaked back into sandbox-visible output", spec.Name)
	}

	return report, nil
}

type demoVault struct {
	secrets map[string]oauthSecret
}

type oauthSecret struct {
	Token   string
	Subject string
	Scope   string
}

func newDemoVault() *demoVault {
	return &demoVault{
		secrets: map[string]oauthSecret{
			tokenRef: {
				Token:   "demo-oauth-access-token",
				Subject: "demo-service-account",
				Scope:   "crm.read",
			},
		},
	}
}

func (v *demoVault) mustSecret() oauthSecret {
	secret, ok := v.secrets[tokenRef]
	if !ok {
		panic("missing demo vault secret: " + tokenRef)
	}
	return secret
}

type demoAPIServer struct {
	server  *httptest.Server
	vault   *demoVault
	mu      sync.Mutex
	history []serverRequestAudit
}

type serverRequestAudit struct {
	Method               string
	Path                 string
	RequestID            string
	AuthorizationPresent bool
	AuthorizationValid   bool
}

func newDemoAPIServer(vault *demoVault) *demoAPIServer {
	demo := &demoAPIServer{vault: vault}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/profile", demo.handleProfile)
	demo.server = httptest.NewServer(mux)
	return demo
}

func (s *demoAPIServer) URL() string {
	return s.server.URL
}

func (s *demoAPIServer) Close() {
	s.server.Close()
}

func (s *demoAPIServer) handleProfile(w http.ResponseWriter, r *http.Request) {
	expectedAuth := "Bearer " + s.vault.mustSecret().Token
	gotAuth := r.Header.Get("Authorization")

	s.mu.Lock()
	s.history = append(s.history, serverRequestAudit{
		Method:               r.Method,
		Path:                 r.URL.Path,
		RequestID:            r.Header.Get("X-Request-ID"),
		AuthorizationPresent: gotAuth != "",
		AuthorizationValid:   gotAuth == expectedAuth,
	})
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if gotAuth != expectedAuth {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(apiResponse{
			OK:              false,
			Error:           "missing or invalid bearer token",
			RequestID:       r.Header.Get("X-Request-ID"),
			AuthenticatedAs: "",
		})
		return
	}

	secret := s.vault.mustSecret()
	_ = json.NewEncoder(w).Encode(apiResponse{
		OK:                  true,
		Service:             "crm",
		AuthenticatedAs:     secret.Subject,
		AuthorizationSource: "host-extension-vault",
		RequestID:           r.Header.Get("X-Request-ID"),
		Scope:               secret.Scope,
	})
}

func (s *demoAPIServer) History() []serverRequestAudit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]serverRequestAudit(nil), s.history...)
}

type apiResponse struct {
	OK                  bool   `json:"ok"`
	Service             string `json:"service,omitempty"`
	AuthenticatedAs     string `json:"authenticated_as,omitempty"`
	AuthorizationSource string `json:"authorization_source,omitempty"`
	RequestID           string `json:"request_id,omitempty"`
	Scope               string `json:"scope,omitempty"`
	Error               string `json:"error,omitempty"`
}

func decodeAPIResponses(stdout string) (map[string]apiResponseRecord, error) {
	scanner := bufio.NewScanner(strings.NewReader(stdout))
	records := make(map[string]apiResponseRecord)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var response apiResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			return nil, fmt.Errorf("decode curl stdout line as json: %w", err)
		}
		if response.RequestID == "" {
			return nil, errors.New("decoded response without request_id")
		}
		records[response.RequestID] = apiResponseRecord{
			Raw:      line,
			Response: response,
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan curl stdout: %w", err)
	}
	return records, nil
}

type oauthInjectingClient struct {
	httpClient *http.Client
	serverURL  *url.URL
	vault      *demoVault
	mu         sync.Mutex
	history    []requestAudit
}

type requestAudit struct {
	RequestID                    string
	LogicalURL                   string
	ForwardedURL                 string
	AuthorizationInjected        bool
	AuthorizationSource          string
	SandboxAuthorizationIn       bool
	AuthorizationOverrideApplied bool
}

func newOAuthInjectingClient(serverURL string, vault *demoVault) (*oauthInjectingClient, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("parse server url: %w", err)
	}
	return &oauthInjectingClient{
		httpClient: &http.Client{},
		serverURL:  parsed,
		vault:      vault,
	}, nil
}

func (c *oauthInjectingClient) Do(ctx context.Context, req *network.Request) (*network.Response, error) {
	if req == nil {
		return nil, errors.New("network request was nil")
	}

	logicalURL, err := url.Parse(req.URL)
	if err != nil {
		return nil, fmt.Errorf("parse logical request url: %w", err)
	}
	if logicalURL.Host != "crm.example.test" {
		return nil, &network.AccessDeniedError{
			URL:    req.URL,
			Reason: "host not registered by oauth network extension",
		}
	}

	method := strings.TrimSpace(req.Method)
	if method == "" {
		method = http.MethodGet
	}

	forwarded := *c.serverURL
	forwarded.Path = logicalURL.Path
	forwarded.RawQuery = logicalURL.RawQuery

	requestCtx := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithTimeout(ctx, req.Timeout.Round(time.Millisecond))
		defer cancel()
	}

	httpReq, err := http.NewRequestWithContext(requestCtx, method, forwarded.String(), bytes.NewReader(req.Body))
	if err != nil {
		return nil, fmt.Errorf("create forwarded request: %w", err)
	}
	for name, value := range req.Headers {
		httpReq.Header.Set(name, value)
	}

	secret := c.vault.mustSecret()
	incomingAuthorization := headerValue(req.Headers, "Authorization")
	requestID := headerValue(req.Headers, "X-Request-ID")
	httpReq.Header.Set("Authorization", "Bearer "+secret.Token)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("perform forwarded request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	c.mu.Lock()
	c.history = append(c.history, requestAudit{
		RequestID:                    requestID,
		LogicalURL:                   req.URL,
		ForwardedURL:                 forwarded.String(),
		AuthorizationInjected:        true,
		AuthorizationSource:          tokenRef,
		SandboxAuthorizationIn:       incomingAuthorization != "",
		AuthorizationOverrideApplied: incomingAuthorization != "",
	})
	c.mu.Unlock()

	return &network.Response{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Headers:    flattenHeaders(resp.Header),
		Body:       body,
		URL:        req.URL,
	}, nil
}

func (c *oauthInjectingClient) History() []requestAudit {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]requestAudit(nil), c.history...)
}

func flattenHeaders(header http.Header) map[string]string {
	out := make(map[string]string, len(header))
	for name, values := range header {
		out[name] = strings.Join(values, ", ")
	}
	return out
}

func headerValue(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

type demoReport struct {
	ScriptPath   string
	ScriptSource string
	Scenarios    []scenarioReport
}

type scenarioReport struct {
	Name                  string
	CurlStdout            string
	TraceArgv             []string
	Response              apiResponse
	Audit                 requestAudit
	ServerRequest         serverRequestAudit
	SecretVisibleInStdout bool
	SecretVisibleInTrace  bool
}

func renderReport(w io.Writer, report *demoReport) error {
	if _, err := fmt.Fprintln(w, "gbash oauth network extension demo"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Script source (%s):\n", report.ScriptPath); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, report.ScriptSource); err != nil {
		return err
	}
	for i := range report.Scenarios {
		scenario := report.Scenarios[i]
		traceJSON, _ := json.Marshal(scenario.TraceArgv)

		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "Scenario %d: %s\n", i+1, scenario.Name); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w, "Curl stdout:"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  %s\n", strings.TrimSpace(scenario.CurlStdout)); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w, "Trace argv:"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  %s\n", traceJSON); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w, "Host-side audit:"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  logical_url=%s\n", scenario.Audit.LogicalURL); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  forwarded_url=%s\n", scenario.Audit.ForwardedURL); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  authorization_source=%s\n", scenario.Audit.AuthorizationSource); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  authorization_injected=%t\n", scenario.Audit.AuthorizationInjected); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  sandbox_sent_authorization=%t\n", scenario.Audit.SandboxAuthorizationIn); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  authorization_override_applied=%t\n", scenario.Audit.AuthorizationOverrideApplied); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w, "Server-side verification:"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  request_id=%s\n", scenario.ServerRequest.RequestID); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  authorization_present=%t\n", scenario.ServerRequest.AuthorizationPresent); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  authorization_valid=%t\n", scenario.ServerRequest.AuthorizationValid); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w, "Leak checks:"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  secret_visible_in_stdout=%t\n", scenario.SecretVisibleInStdout); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  secret_visible_in_trace=%t\n", scenario.SecretVisibleInTrace); err != nil {
			return err
		}
	}
	return nil
}

func collectCurlArgvByRequestID(events []trace.Event) (map[string][]string, error) {
	out := make(map[string][]string)
	for i := range events {
		event := events[i]
		if event.Kind != trace.EventCallExpanded || event.Command == nil {
			continue
		}
		if event.Command.Name != "curl" {
			continue
		}

		requestID, ok := requestIDFromArgv(event.Command.Argv)
		if !ok {
			return nil, fmt.Errorf("trace did not include X-Request-ID in curl argv: %q", event.Command.Argv)
		}
		out[requestID] = append([]string(nil), event.Command.Argv...)
	}
	if len(out) == 0 {
		return nil, errors.New("trace did not include curl argv")
	}
	return out, nil
}

func requestIDFromArgv(argv []string) (string, bool) {
	for i := range argv {
		if argv[i] != "-H" && argv[i] != "--header" {
			continue
		}
		if i+1 >= len(argv) {
			break
		}
		header := argv[i+1]
		name, value, ok := strings.Cut(header, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "X-Request-ID") {
			continue
		}
		return strings.TrimSpace(value), true
	}
	return "", false
}
