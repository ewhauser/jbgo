package bashtool

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/commands"
	"github.com/ewhauser/gbash/contrib/extras"
	"github.com/ewhauser/gbash/host"
	"github.com/ewhauser/gbash/internal/shell/interp"
)

// CommandProfile describes the command surface the tool should advertise.
type CommandProfile string

const (
	// CommandProfileDefault exposes plain gbash builtins only.
	CommandProfileDefault CommandProfile = "gbash"
	// CommandProfileExtras exposes the stable gbash contrib registry bundle.
	CommandProfileExtras CommandProfile = "gbash-extras"
	// CommandProfileCustom leaves command notes to the embedder.
	CommandProfileCustom CommandProfile = "custom"
)

const (
	defaultToolName = "bash"
	defaultUsername = "agent"
	defaultHomeDir  = "/home/agent"
	defaultHostname = "gbash"
	defaultPATH     = "/usr/bin:/bin"
	defaultShell    = "/bin/bash"
	defaultVersion  = "devel"
	maxTimeoutMS    = uint64(math.MaxInt64 / int64(time.Millisecond))
)

// Config controls the public bash-tool contract and one-shot execution helper.
type Config struct {
	Name               string
	Username           string
	HomeDir            string
	Hostname           string
	Profile            CommandProfile
	CommandNotes       []string
	SystemPromptAppend string
	Registry           commands.CommandRegistry
	RuntimeOptions     []gbash.Option
}

// Request is the tool-call input contract.
type Request struct {
	Commands  string  `json:"commands,omitempty"`
	Script    string  `json:"script,omitempty"`
	TimeoutMS *uint64 `json:"timeout_ms,omitempty"`
}

// ResolvedCommands returns the command payload, preferring commands over script.
func (r Request) ResolvedCommands() string {
	if r.Commands != "" || r.Script == "" {
		return r.Commands
	}
	return r.Script
}

// Timeout returns the configured per-call timeout.
func (r Request) Timeout() time.Duration {
	if r.TimeoutMS == nil {
		return 0
	}
	if *r.TimeoutMS >= maxTimeoutMS {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(*r.TimeoutMS) * time.Millisecond
}

// Response is the tool execution result contract.
type Response struct {
	Stdout          string            `json:"stdout"`
	Stderr          string            `json:"stderr"`
	ExitCode        int               `json:"exit_code"`
	Error           string            `json:"error,omitempty"`
	StdoutTruncated bool              `json:"stdout_truncated,omitempty"`
	StderrTruncated bool              `json:"stderr_truncated,omitempty"`
	FinalEnv        map[string]string `json:"final_env,omitempty"`
}

// ToolDefinition is a provider-neutral function tool definition.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type promptData struct {
	ToolName        string
	HomeDir         string
	CommandNotes    []string
	LanguageWarning string
}

type normalizedConfig struct {
	name               string
	username           string
	homeDir            string
	hostname           string
	profile            CommandProfile
	commandNotes       []string
	systemPromptAppend string
	runtimeOpts        []gbash.Option
}

// Tool owns bash-tool metadata, prompt generation, and one-shot execution.
type Tool struct {
	cfg                 normalizedConfig
	registry            commands.CommandRegistry
	defaultCommandNames []string
	customCommandNames  []string
	commandNotes        []string
}

//go:embed prompts/system_prompt.tmpl
var promptFS embed.FS

var systemPromptTemplate = mustParsePromptTemplate("prompts/system_prompt.tmpl")

// New constructs a reusable bash tool contract.
//
//nolint:gocritic // Config is a public value type for ergonomic construction.
func New(cfg Config) *Tool {
	normalized := normalizeConfig(&cfg)
	registry := normalizedRegistry(cfg.Registry, normalized.profile)
	defaultNames := defaultRegistryNames()
	customNames := diffCommandNames(defaultNames, registry.Names())
	commandNotes := append([]string(nil), normalized.commandNotes...)
	if note := profileNote(normalized.profile, customNames); note != "" {
		commandNotes = append(commandNotes, note)
	}
	return &Tool{
		cfg:                 normalized,
		registry:            registry,
		defaultCommandNames: defaultNames,
		customCommandNames:  customNames,
		commandNotes:        normalizeNotes(commandNotes),
	}
}

// Name returns the function-tool name.
func (t *Tool) Name() string {
	return t.cfg.name
}

// Description returns the upstream-style tool description.
func (t *Tool) Description() string {
	desc := "Run bash commands in an isolated virtual filesystem"
	if len(t.customCommandNames) == 0 {
		return desc
	}
	return desc + ". Custom commands: " + strings.Join(t.customCommandNames, ", ")
}

// InputSchema returns the input JSON schema for the tool call.
func (t *Tool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"commands": map[string]any{
				"type":        "string",
				"description": "Bash commands to execute. Preferred tool-call field.",
			},
			"script": map[string]any{
				"type":        "string",
				"description": "Alias for commands; accepted for compatibility with existing callers.",
			},
			"timeout_ms": map[string]any{
				"type":        "integer",
				"description": "Per-call timeout in milliseconds.",
			},
		},
		"additionalProperties": false,
	}
}

// OutputSchema returns the JSON schema for tool responses.
func (t *Tool) OutputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"stdout": map[string]any{
				"type": "string",
			},
			"stderr": map[string]any{
				"type": "string",
			},
			"exit_code": map[string]any{
				"type": "integer",
			},
			"error": map[string]any{
				"type": "string",
			},
			"stdout_truncated": map[string]any{
				"type": "boolean",
			},
			"stderr_truncated": map[string]any{
				"type": "boolean",
			},
			"final_env": map[string]any{
				"type":                 "object",
				"additionalProperties": map[string]any{"type": "string"},
			},
		},
		"required":             []string{"stdout", "stderr", "exit_code"},
		"additionalProperties": false,
	}
}

// Help returns an upstream-style Markdown help document.
func (t *Tool) Help() string {
	var doc strings.Builder
	doc.WriteString("# Bash\n\n")
	doc.WriteString(t.Description())
	doc.WriteString(".\n\n")
	fmt.Fprintf(&doc, "**Version:** %s\n**Name:** `%s`\n**Locale:** `en-US`\n\n", toolVersion(), t.Name())

	doc.WriteString("## Parameters\n\n")
	doc.WriteString("| Name | Type | Required | Default | Description |\n")
	doc.WriteString("|------|------|----------|---------|-------------|\n")
	doc.WriteString("| `commands` | string | yes* | — | Bash commands to execute |\n")
	doc.WriteString("| `script` | string | yes* | — | Alias for `commands` |\n")
	doc.WriteString("| `timeout_ms` | integer | no | — | Per-call timeout in milliseconds |\n\n")
	doc.WriteString("*Either `commands` or `script` must be provided.*\n\n")

	doc.WriteString("## Result\n\n")
	doc.WriteString("| Field | Type | Description |\n")
	doc.WriteString("|------|------|-------------|\n")
	doc.WriteString("| `stdout` | string | Standard output |\n")
	doc.WriteString("| `stderr` | string | Standard error |\n")
	doc.WriteString("| `exit_code` | integer | Shell exit code |\n")
	doc.WriteString("| `error` | string | Error category when tool execution fails |\n")
	doc.WriteString("| `stdout_truncated` | boolean | Whether stdout was truncated |\n")
	doc.WriteString("| `stderr_truncated` | boolean | Whether stderr was truncated |\n")
	doc.WriteString("| `final_env` | object | Final shell environment after execution |\n\n")

	doc.WriteString("## Examples\n\n")
	doc.WriteString("```json\n")
	doc.WriteString("{\"commands\":\"echo hello\"}\n")
	doc.WriteString("```\n\n")
	doc.WriteString("```json\n")
	doc.WriteString("{\"script\":\"echo data > /tmp/f.txt && cat /tmp/f.txt\",\"timeout_ms\":5000}\n")
	doc.WriteString("```\n\n")

	doc.WriteString("## Behavior\n\n")
	doc.WriteString("- Filesystem is virtual and isolated per execution.\n")
	doc.WriteString("- Standard bash syntax is supported, including pipes, redirects, loops, functions, and arrays.\n")
	doc.WriteString("- Builtins available by default: `")
	doc.WriteString(strings.Join(t.defaultCommandNames, "`, `"))
	doc.WriteString("`\n")
	if len(t.customCommandNames) > 0 {
		doc.WriteString("- Custom commands: `")
		doc.WriteString(strings.Join(t.customCommandNames, "`, `"))
		doc.WriteString("`\n")
	}
	fmt.Fprintf(&doc, "- User: `%s`\n", t.cfg.username)
	fmt.Fprintf(&doc, "- Host: `%s`\n", t.cfg.hostname)
	fmt.Fprintf(&doc, "- Home: `%s`\n", t.cfg.homeDir)
	if len(t.commandNotes) > 0 {
		doc.WriteString("\n## Notes\n\n")
		for _, note := range t.commandNotes {
			doc.WriteString("- ")
			doc.WriteString(note)
			doc.WriteString("\n")
		}
	}
	if warning := t.languageWarning(); warning != "" {
		doc.WriteString("\n## Warnings\n\n")
		doc.WriteString("- ")
		doc.WriteString(warning)
		doc.WriteString("\n")
	}
	return doc.String()
}

// SystemPrompt returns the upstream-style bash tool system prompt.
func (t *Tool) SystemPrompt() string {
	prompt := renderPromptTemplate(systemPromptTemplate, promptData{
		ToolName:        t.Name(),
		HomeDir:         t.cfg.homeDir,
		CommandNotes:    t.commandNotes,
		LanguageWarning: t.languageWarning(),
	})
	if appendText := strings.TrimSpace(t.cfg.systemPromptAppend); appendText != "" {
		prompt += " " + appendText
	}
	return prompt
}

// ToolDefinition returns the provider-neutral function definition.
func (t *Tool) ToolDefinition() ToolDefinition {
	return ToolDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: t.InputSchema(),
	}
}

// Execute runs a single bash request in a fresh runtime.
func (t *Tool) Execute(ctx context.Context, req Request) Response {
	commandsText := req.ResolvedCommands()
	if commandsText == "" {
		return Response{Stdout: "", Stderr: "", ExitCode: 0}
	}

	requestTimeout := req.Timeout()
	contextTimeout := deadlineTimeout(ctx)

	opts := []gbash.Option{gbash.WithWorkingDir(t.cfg.homeDir)}
	opts = append(opts, t.cfg.runtimeOpts...)
	opts = append(opts,
		gbash.WithRegistry(t.registry),
		gbash.WithHost(newVirtualHost(&t.cfg)),
	)
	rt, err := gbash.New(opts...) //nolint:contextcheck // constructor does not accept context
	if err != nil {
		return Response{
			Stderr:   err.Error(),
			ExitCode: 1,
			Error:    "internal_error",
		}
	}

	result, err := rt.Run(ctx, &gbash.ExecutionRequest{
		Script:  commandsText,
		Timeout: requestTimeout,
	})
	if err != nil {
		return responseFromError(ctx, err, requestTimeout, contextTimeout)
	}
	if result == nil {
		return Response{Stdout: "", Stderr: "", ExitCode: 0}
	}
	return Response{
		Stdout:          result.Stdout,
		Stderr:          result.Stderr,
		ExitCode:        result.ExitCode,
		StdoutTruncated: result.StdoutTruncated,
		StderrTruncated: result.StderrTruncated,
		FinalEnv:        maps.Clone(result.FinalEnv),
	}
}

// ParseRequest decodes a provider tool-call payload, supporting script as an
// alias for commands.
func ParseRequest(input map[string]any) (Request, error) {
	if input == nil {
		return Request{}, fmt.Errorf("tool arguments must be a JSON object")
	}

	var req Request
	if raw, ok := input["commands"]; ok {
		text, ok := raw.(string)
		if !ok {
			return Request{}, fmt.Errorf("`commands` must be a string")
		}
		req.Commands = text
	}
	if raw, ok := input["script"]; ok {
		text, ok := raw.(string)
		if !ok {
			return Request{}, fmt.Errorf("`script` must be a string")
		}
		req.Script = text
	}
	if req.Commands == "" && req.Script == "" {
		return Request{}, fmt.Errorf("`commands` or `script` is required")
	}
	if raw, ok := input["timeout_ms"]; ok {
		timeout, err := parseUint64(raw)
		if err != nil {
			return Request{}, fmt.Errorf("`timeout_ms` must be an integer")
		}
		req.TimeoutMS = &timeout
	}
	return req, nil
}

// FormatToolResult returns the upstream evaluator-style textual tool result.
func FormatToolResult(resp Response) string {
	var out string
	if resp.Stdout != "" {
		out += resp.Stdout
	}
	if resp.Stderr != "" {
		if out != "" {
			out += "\n"
		}
		out += "STDERR: " + resp.Stderr
	}
	if resp.ExitCode != 0 {
		if out != "" {
			out += "\n"
		}
		out += fmt.Sprintf("Exit code: %d", resp.ExitCode)
	}
	if out == "" {
		return "(no output)"
	}
	return out
}

func normalizeConfig(cfg *Config) normalizedConfig {
	if cfg == nil {
		cfg = &Config{}
	}
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		name = defaultToolName
	}
	username := strings.TrimSpace(cfg.Username)
	if username == "" {
		username = defaultUsername
	}
	homeDir := strings.TrimSpace(cfg.HomeDir)
	if homeDir == "" {
		homeDir = "/home/" + username
	}
	hostname := strings.TrimSpace(cfg.Hostname)
	if hostname == "" {
		hostname = defaultHostname
	}
	profile := cfg.Profile
	if profile == "" {
		profile = CommandProfileDefault
	}
	return normalizedConfig{
		name:               name,
		username:           username,
		homeDir:            homeDir,
		hostname:           hostname,
		profile:            profile,
		commandNotes:       append([]string(nil), cfg.CommandNotes...),
		systemPromptAppend: strings.TrimSpace(cfg.SystemPromptAppend),
		runtimeOpts:        append([]gbash.Option{}, cfg.RuntimeOptions...),
	}
}

func normalizedRegistry(registry commands.CommandRegistry, profile CommandProfile) commands.CommandRegistry {
	if registry != nil {
		return registry
	}
	switch profile {
	case CommandProfileExtras:
		return extras.FullRegistry()
	default:
		return gbash.DefaultRegistry()
	}
}

func defaultRegistryNames() []string {
	return gbash.DefaultRegistry().Names()
}

func diffCommandNames(base, selected []string) []string {
	baseSet := make(map[string]struct{}, len(base))
	for _, name := range base {
		baseSet[name] = struct{}{}
	}
	custom := make([]string, 0, len(selected))
	for _, name := range selected {
		if _, ok := baseSet[name]; ok {
			continue
		}
		custom = append(custom, name)
	}
	slices.Sort(custom)
	return custom
}

func profileNote(profile CommandProfile, customNames []string) string {
	if len(customNames) == 0 {
		return ""
	}
	switch profile {
	case CommandProfileExtras:
		return "Stable contrib commands available: " + strings.Join(customNames, ", ") + "."
	case CommandProfileCustom:
		return "Custom commands available: " + strings.Join(customNames, ", ") + "."
	default:
		return ""
	}
}

func normalizeNotes(notes []string) []string {
	if len(notes) == 0 {
		return nil
	}
	out := make([]string, 0, len(notes))
	for _, note := range notes {
		note = strings.TrimSpace(note)
		if note == "" {
			continue
		}
		if !strings.HasSuffix(note, ".") {
			note += "."
		}
		out = append(out, note)
	}
	return out
}

func (t *Tool) languageWarning() string {
	selectedNames := t.registry.Names()
	hasPerl := slices.Contains(selectedNames, "perl")
	hasPython := slices.Contains(selectedNames, "python") || slices.Contains(selectedNames, "python3")
	hasRuby := slices.Contains(selectedNames, "ruby")
	hasNode := slices.Contains(selectedNames, "node") || slices.Contains(selectedNames, "nodejs")

	missing := make([]string, 0, 4)
	if !hasPerl {
		missing = append(missing, "perl")
	}
	if !hasPython {
		missing = append(missing, "python/python3")
	}
	if !hasRuby {
		missing = append(missing, "ruby")
	}
	if !hasNode {
		missing = append(missing, "node/nodejs")
	}
	if len(missing) == 0 {
		return ""
	}
	return strings.Join(missing, ", ") + " not available."
}

func parseUint64(raw any) (uint64, error) {
	switch typed := raw.(type) {
	case uint64:
		return typed, nil
	case uint32:
		return uint64(typed), nil
	case uint:
		return uint64(typed), nil
	case int:
		if typed < 0 {
			return 0, fmt.Errorf("negative")
		}
		return uint64(typed), nil
	case int64:
		if typed < 0 {
			return 0, fmt.Errorf("negative")
		}
		return uint64(typed), nil
	case float64:
		if typed < 0 || typed != float64(uint64(typed)) {
			return 0, fmt.Errorf("not integer")
		}
		return uint64(typed), nil
	case json.Number:
		value, err := typed.Int64()
		if err != nil || value < 0 {
			return 0, fmt.Errorf("invalid")
		}
		return uint64(value), nil
	case string:
		value, err := strconv.ParseUint(typed, 10, 64)
		if err != nil {
			return 0, err
		}
		return value, nil
	default:
		return 0, fmt.Errorf("unsupported type %T", raw)
	}
}

func responseFromError(ctx context.Context, err error, requestTimeout, contextTimeout time.Duration) Response {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		if ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return timeoutResponse(contextTimeout)
		}
		return timeoutResponse(requestTimeout)
	case errors.Is(err, context.Canceled):
		return Response{
			Stderr:   err.Error(),
			ExitCode: 1,
			Error:    "canceled",
		}
	default:
		return Response{
			Stderr:   err.Error(),
			ExitCode: 1,
			Error:    "execution_error",
		}
	}
}

func deadlineTimeout(ctx context.Context) time.Duration {
	if ctx == nil {
		return 0
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	timeout := time.Until(deadline)
	if timeout < 0 {
		return 0
	}
	return timeout
}

func timeoutResponse(timeout time.Duration) Response {
	seconds := timeout.Seconds()
	if seconds <= 0 {
		seconds = 0
	}
	return Response{
		Stderr:   fmt.Sprintf("bash: execution timed out after %.1fs\n", seconds),
		ExitCode: 124,
		Error:    "timeout",
	}
}

func toolVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return defaultVersion
	}
	return info.Main.Version
}

type virtualHost struct {
	cfg *normalizedConfig
}

func newVirtualHost(cfg *normalizedConfig) host.Adapter {
	return &virtualHost{cfg: cfg}
}

func (h *virtualHost) Defaults(context.Context) (host.Defaults, error) {
	env := map[string]string{
		"HOME":    h.cfg.homeDir,
		"PATH":    defaultPATH,
		"SHELL":   defaultShell,
		"TMPDIR":  "/tmp",
		"TMP":     "/tmp",
		"TEMP":    "/tmp",
		"USER":    h.cfg.username,
		"LOGNAME": h.cfg.username,
		"GROUP":   h.cfg.username,
		"GROUPS":  h.cfg.username,
		"UID":     "1000",
		"EUID":    "1000",
		"GID":     "1000",
		"EGID":    "1000",
	}
	return host.Defaults{Env: env}, nil
}

func (h *virtualHost) Platform() host.Platform {
	osName := host.CurrentOS()
	defaults := osName.PlatformDefaults()
	machine := normalizeMachine(runtime.GOARCH)
	envCaseInsensitive := defaults.EnvCaseInsensitive
	requireExecutableBit := defaults.RequireExecutableBit
	return host.Platform{
		OS:                   osName,
		Arch:                 machine,
		OSType:               defaults.OSType,
		EnvCaseInsensitive:   &envCaseInsensitive,
		PathExtensions:       append([]string(nil), defaults.PathExtensions...),
		RequireExecutableBit: &requireExecutableBit,
		Uname: host.Uname{
			SysName:         defaults.KernelName,
			NodeName:        h.cfg.hostname,
			Release:         "unknown",
			Version:         "unknown",
			Machine:         machine,
			OperatingSystem: defaults.OperatingSystem,
		},
	}
}

func (h *virtualHost) ExecutionMeta(context.Context) (host.ExecutionMeta, error) {
	return host.ExecutionMeta{
		PID:          1,
		PPID:         0,
		ProcessGroup: 1,
	}, nil
}

func (h *virtualHost) NewPipe() (io.ReadCloser, io.WriteCloser, error) {
	reader, writer := interp.NewVirtualPipe()
	return reader, writer, nil
}

func normalizeMachine(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	default:
		return goarch
	}
}

func mustParsePromptTemplate(name string) *template.Template {
	data, err := promptFS.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("read embedded template %q: %v", name, err))
	}
	tmpl, err := template.New(name).Parse(string(data))
	if err != nil {
		panic(fmt.Sprintf("parse embedded template %q: %v", name, err))
	}
	return tmpl
}

func renderPromptTemplate(tmpl *template.Template, data any) string {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		panic(fmt.Sprintf("render embedded template %q: %v", tmpl.Name(), err))
	}
	return strings.TrimSpace(buf.String())
}
