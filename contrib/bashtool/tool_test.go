package bashtool

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/commands"
)

func TestDefaultToolDefinitionAndSchemas(t *testing.T) {
	t.Parallel()

	tool := New(Config{})
	def := tool.ToolDefinition()

	if def.Name != "bash" {
		t.Fatalf("ToolDefinition().Name = %q, want bash", def.Name)
	}
	if got := def.Description; !strings.Contains(got, "isolated virtual filesystem") {
		t.Fatalf("ToolDefinition().Description = %q", got)
	}
	if got := def.InputSchema["type"]; got != "object" {
		t.Fatalf("InputSchema.type = %#v, want object", got)
	}
	props, _ := def.InputSchema["properties"].(map[string]any)
	if _, ok := props["commands"]; !ok {
		t.Fatalf("InputSchema.properties missing commands: %#v", props)
	}
	if _, ok := props["script"]; !ok {
		t.Fatalf("InputSchema.properties missing script: %#v", props)
	}
	for _, forbidden := range []string{"oneOf", "anyOf", "allOf", "enum", "not"} {
		if _, ok := def.InputSchema[forbidden]; ok {
			t.Fatalf("InputSchema must not include top-level %s: %#v", forbidden, def.InputSchema)
		}
	}
	if got := tool.OutputSchema()["type"]; got != "object" {
		t.Fatalf("OutputSchema.type = %#v, want object", got)
	}
}

func TestSystemPromptTracksUpstreamWordingWithGbashIdentity(t *testing.T) {
	t.Parallel()

	tool := New(Config{})
	got := tool.SystemPrompt()

	if !strings.Contains(got, "Returns JSON with stdout, stderr, exit_code.") {
		t.Fatalf("SystemPrompt() = %q, want JSON contract", got)
	}
	if !strings.Contains(got, "Home /home/agent.") {
		t.Fatalf("SystemPrompt() = %q, want gbash home", got)
	}
	if !strings.Contains(got, "Use bash syntax; do not assume /bin/sh portability") {
		t.Fatalf("SystemPrompt() = %q, want upstream syntax warning", got)
	}
	if !strings.Contains(got, "perl, python/python3, ruby, node/nodejs not available.") {
		t.Fatalf("SystemPrompt() = %q, want language warning", got)
	}
}

func TestSystemPromptSupportsAppend(t *testing.T) {
	t.Parallel()

	tool := New(Config{
		SystemPromptAppend: "Always prefer jq for JSON reshaping when available.",
	})

	got := tool.SystemPrompt()
	if !strings.HasSuffix(got, "Always prefer jq for JSON reshaping when available.") {
		t.Fatalf("SystemPrompt() = %q, want appended guidance suffix", got)
	}
	if !strings.Contains(got, "Returns JSON with stdout, stderr, exit_code.") {
		t.Fatalf("SystemPrompt() = %q, want base prompt preserved", got)
	}
}

func TestLanguageWarningSuppression(t *testing.T) {
	t.Parallel()

	registry := commands.NewRegistry(
		commands.DefineCommand("perl", nil),
		commands.DefineCommand("python", nil),
		commands.DefineCommand("ruby", nil),
		commands.DefineCommand("node", nil),
	)
	tool := New(Config{
		Profile:  CommandProfileCustom,
		Registry: registry,
	})

	if got := tool.languageWarning(); got != "" {
		t.Fatalf("languageWarning() = %q, want empty", got)
	}
}

func TestLanguageWarningPartialSuppression(t *testing.T) {
	t.Parallel()

	registry := commands.NewRegistry(
		commands.DefineCommand("python3", nil),
		commands.DefineCommand("nodejs", nil),
	)
	tool := New(Config{
		Profile:  CommandProfileCustom,
		Registry: registry,
	})

	if got, want := tool.languageWarning(), "perl, ruby not available."; got != want {
		t.Fatalf("languageWarning() = %q, want %q", got, want)
	}
}

func TestHelpIncludesExtrasNotes(t *testing.T) {
	t.Parallel()

	tool := New(Config{Profile: CommandProfileExtras})
	help := tool.Help()

	if !strings.Contains(help, "## Parameters") {
		t.Fatalf("Help() missing parameters section: %q", help)
	}
	if !strings.Contains(help, "## Result") {
		t.Fatalf("Help() missing result section: %q", help)
	}
	if !strings.Contains(help, "Stable contrib commands available: awk, html-to-markdown, jq, sqlite3, yq.") {
		t.Fatalf("Help() missing extras note: %q", help)
	}
	if !strings.Contains(help, "`awk`, `html-to-markdown`, `jq`, `sqlite3`, `yq`") {
		t.Fatalf("Help() missing custom commands list: %q", help)
	}
}

func TestParseRequestSupportsScriptAliasAndTimeout(t *testing.T) {
	t.Parallel()

	req, err := ParseRequest(map[string]any{
		"script":     "echo hi",
		"timeout_ms": json.Number("2500"),
	})
	if err != nil {
		t.Fatalf("ParseRequest() error = %v", err)
	}
	if got := req.ResolvedCommands(); got != "echo hi" {
		t.Fatalf("ResolvedCommands() = %q, want echo hi", got)
	}
	if got := req.Timeout().Milliseconds(); got != 2500 {
		t.Fatalf("Timeout() = %dms, want 2500ms", got)
	}
}

func TestTimeoutClampsInsteadOfOverflowing(t *testing.T) {
	t.Parallel()

	tooLarge := uint64(math.MaxUint64)
	req := Request{TimeoutMS: &tooLarge}

	if got := req.Timeout(); got != time.Duration(math.MaxInt64) {
		t.Fatalf("Timeout() = %v, want clamped max duration", got)
	}
	if got := req.Timeout(); got <= 0 {
		t.Fatalf("Timeout() = %v, want positive duration", got)
	}
}

func TestFormatToolResultMatchesEvaluatorShape(t *testing.T) {
	t.Parallel()

	got := FormatToolResult(Response{
		Stdout:   "out",
		Stderr:   "err",
		ExitCode: 1,
	})
	want := "out\nSTDERR: err\nExit code: 1"
	if got != want {
		t.Fatalf("FormatToolResult() = %q, want %q", got, want)
	}
}

func TestExecuteUsesDefaultRegistry(t *testing.T) {
	t.Parallel()

	tool := New(Config{})
	resp := tool.Execute(context.Background(), Request{Commands: "echo hello\n"})
	if resp.ExitCode != 0 {
		t.Fatalf("Execute() exit = %d, want 0; stderr=%q", resp.ExitCode, resp.Stderr)
	}
	if got := resp.Stdout; got != "hello\n" {
		t.Fatalf("Execute() stdout = %q, want hello\\n", got)
	}
}

func TestExecuteUsesExtrasRegistry(t *testing.T) {
	t.Parallel()

	tool := New(Config{Profile: CommandProfileExtras})
	resp := tool.Execute(context.Background(), Request{
		Commands: `printf '{"name":"alice"}' | jq -r '.name'`,
	})
	if resp.ExitCode != 0 {
		t.Fatalf("Execute() exit = %d, want 0; stderr=%q", resp.ExitCode, resp.Stderr)
	}
	if got := strings.TrimSpace(resp.Stdout); got != "alice" {
		t.Fatalf("Execute() stdout = %q, want alice", got)
	}
}

func TestExecutePassesRuntimeOptions(t *testing.T) {
	t.Parallel()

	tool := New(Config{
		RuntimeOptions: []gbash.Option{
			gbash.WithWorkingDir("/tmp"),
		},
	})
	resp := tool.Execute(context.Background(), Request{Commands: "pwd\n"})
	if resp.ExitCode != 0 {
		t.Fatalf("Execute() exit = %d, want 0; stderr=%q", resp.ExitCode, resp.Stderr)
	}
	if got := strings.TrimSpace(resp.Stdout); got != "/tmp" {
		t.Fatalf("Execute() stdout = %q, want /tmp", got)
	}
}

func TestExecuteUsesConfiguredHomeDirAsWorkingDir(t *testing.T) {
	t.Parallel()

	tool := New(Config{HomeDir: "/tmp"})
	resp := tool.Execute(context.Background(), Request{Commands: "pwd\n"})
	if resp.ExitCode != 0 {
		t.Fatalf("Execute() exit = %d, want 0; stderr=%q", resp.ExitCode, resp.Stderr)
	}
	if got := strings.TrimSpace(resp.Stdout); got != "/tmp" {
		t.Fatalf("Execute() stdout = %q, want /tmp", got)
	}
}

func TestExecuteUsesVirtualPipes(t *testing.T) {
	t.Parallel()

	tool := New(Config{})
	resp := tool.Execute(context.Background(), Request{
		Commands: "yes | head -n 1\n",
	})
	if resp.ExitCode != 0 {
		t.Fatalf("Execute() exit = %d, want 0; stderr=%q", resp.ExitCode, resp.Stderr)
	}
	if got := resp.Stdout; got != "y\n" {
		t.Fatalf("Execute() stdout = %q, want y\\n", got)
	}
}

func TestResponseFromErrorUsesExternalContextTimeout(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	contextTimeout := deadlineTimeout(ctx)

	time.Sleep(140 * time.Millisecond)

	resp := responseFromError(ctx, context.DeadlineExceeded, 0, contextTimeout)
	if resp.Error != "timeout" {
		t.Fatalf("responseFromError() error = %q, want timeout", resp.Error)
	}
	if resp.ExitCode != 124 {
		t.Fatalf("responseFromError() exit = %d, want 124", resp.ExitCode)
	}
	if strings.Contains(resp.Stderr, "after 0.0s") {
		t.Fatalf("responseFromError() stderr = %q, want caller deadline reflected", resp.Stderr)
	}
	if !strings.Contains(resp.Stderr, "execution timed out after") {
		t.Fatalf("responseFromError() stderr = %q, want timeout message", resp.Stderr)
	}
}
