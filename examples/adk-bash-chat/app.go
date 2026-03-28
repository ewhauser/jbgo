package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ewhauser/gbash/contrib/bashtool"
	"github.com/google/jsonschema-go/jsonschema"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	adksession "google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/genai"
)

const (
	appName       = "gbash_adk_bash_chat"
	defaultUserID = "demo-user"
	bashToolName  = "bash"
)

type chatApp struct {
	modelName string
	backend   resolvedBackend

	sessionService adksession.Service
	runner         *runner.Runner
	bashTool       *persistentBashTool
	chatSessionID  string
}

func newChatApp(ctx context.Context, llm model.LLM, modelName string, backend resolvedBackend) (*chatApp, error) {
	registry := newBashRegistry()

	bashToolContract := newChatBashToolContract(registry)

	bashToolRunner, err := newPersistentBashTool(ctx, registry)
	if err != nil {
		return nil, err
	}

	inputSchema, err := jsonSchemaFromMap(bashToolContract.InputSchema())
	if err != nil {
		return nil, fmt.Errorf("build bash tool input schema: %w", err)
	}
	outputSchema, err := jsonSchemaFromMap(bashToolContract.OutputSchema())
	if err != nil {
		return nil, fmt.Errorf("build bash tool output schema: %w", err)
	}

	bashFunctionTool, err := functiontool.New(functiontool.Config{
		Name:         bashToolContract.Name(),
		Description:  bashToolContract.Description() + ". Files, the working directory, and exported environment variables persist across calls within the current chat session.",
		InputSchema:  inputSchema,
		OutputSchema: outputSchema,
	}, bashToolRunner.Run)
	if err != nil {
		return nil, fmt.Errorf("create bash function tool: %w", err)
	}

	agentInstruction := strings.Join([]string{
		bashToolContract.SystemPrompt(),
		"You are an operations data lab assistant working inside a persistent sandbox.",
		"Use the bash tool for any inspection, filtering, or report generation.",
		"The seeded dataset lives in /home/agent/lab and reusable artifacts belong in /home/agent/work.",
		"Prefer small, auditable shell commands over large scripts.",
		"Before recomputing, check whether a useful artifact already exists in /home/agent/work and reuse or update it.",
		"When you answer, mention any files you created or reused.",
	}, " ")

	rootAgent, err := llmagent.New(llmagent.Config{
		Name:                  "ops_data_lab",
		Model:                 llm,
		Description:           "A chatbot that investigates seeded ops data with a persistent bash tool.",
		Instruction:           agentInstruction,
		GenerateContentConfig: thinkingDisabledConfig(),
		Tools:                 []tool.Tool{bashFunctionTool},
	})
	if err != nil {
		return nil, fmt.Errorf("create ADK agent: %w", err)
	}

	sessionService := adksession.InMemoryService()
	r, err := runner.New(runner.Config{
		AppName:        appName,
		Agent:          rootAgent,
		SessionService: sessionService,
	})
	if err != nil {
		return nil, fmt.Errorf("create ADK runner: %w", err)
	}

	app := &chatApp{
		modelName:      modelName,
		backend:        backend,
		sessionService: sessionService,
		runner:         r,
		bashTool:       bashToolRunner,
	}
	if err := app.resetChatSession(ctx); err != nil {
		return nil, err
	}
	return app, nil
}

func (a *chatApp) run(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
	if a == nil {
		return errors.New("chat app is nil")
	}

	printWelcome(stdout, a.modelName, a.backend.mode)

	scanner := bufio.NewScanner(stdin)
	for {
		_, _ = fmt.Fprint(stdout, "You> ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("read input: %w", err)
			}
			_, _ = fmt.Fprintln(stdout)
			return nil
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		switch strings.ToLower(line) {
		case "exit", "quit":
			return nil
		case "/help":
			printHelp(stdout)
			continue
		case "/reset":
			if err := a.bashTool.Reset(ctx); err != nil {
				return fmt.Errorf("reset bash tool: %w", err)
			}
			if err := a.resetChatSession(ctx); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(stdout, "Reset the ADK conversation and reseeded /home/agent/lab.")
			continue
		}

		if err := a.runTurn(ctx, line, stdout); err != nil {
			_, _ = fmt.Fprintf(stderr, "turn failed: %v\n", err)
		}
	}
}

func (a *chatApp) resetChatSession(ctx context.Context) error {
	created, err := a.sessionService.Create(ctx, &adksession.CreateRequest{
		AppName: appName,
		UserID:  defaultUserID,
	})
	if err != nil {
		return fmt.Errorf("create ADK session: %w", err)
	}
	a.chatSessionID = created.Session.ID()
	return nil
}

func (a *chatApp) runTurn(ctx context.Context, input string, stdout io.Writer) error {
	msg := genai.NewContentFromText(input, genai.RoleUser)

	var firstErr error
	for event, err := range a.runner.Run(ctx, defaultUserID, a.chatSessionID, msg, agent.RunConfig{
		StreamingMode: agent.StreamingModeNone,
	}) {
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		printEvent(stdout, event)
	}

	if firstErr != nil {
		return firstErr
	}
	return nil
}

func printWelcome(w io.Writer, modelName string, backend backendMode) {
	_, _ = fmt.Fprintf(w, "gbash ADK Bash Chat\nModel: %s\nBackend: %s\n", modelName, backend)
	_, _ = fmt.Fprintf(w, "Seeded lab: %s\nWorkspace: %s\n", labDir, workDir)
	_, _ = fmt.Fprintln(w, "Commands: /help, /reset, exit")
	_, _ = fmt.Fprintln(w, "Try: Which service looks most suspicious after the last deploy? Save a markdown summary in /home/agent/work/summary.md.")
}

func printHelp(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Use normal chat input to ask questions about the seeded ops dataset.")
	_, _ = fmt.Fprintln(w, "The app will print each bash tool call and tool result inline so you can see persistent shell activity.")
	_, _ = fmt.Fprintln(w, "/reset recreates the ADK conversation and reseeds the sandbox.")
}

func printEvent(w io.Writer, event *adksession.Event) {
	if event == nil || event.Content == nil {
		return
	}

	for _, part := range event.Content.Parts {
		switch {
		case part.FunctionCall != nil && part.FunctionCall.Name == bashToolName:
			_, _ = fmt.Fprintln(w, "\n[bash script]")
			_, _ = fmt.Fprintln(w, strings.TrimSpace(bashToolCommands(part.FunctionCall.Args)))
		case part.FunctionResponse != nil && part.FunctionResponse.Name == bashToolName:
			_, _ = fmt.Fprintln(w, "\n[bash result]")
			if parsed, ok := decodeBashToolResponse(part.FunctionResponse.Response); ok {
				_, _ = fmt.Fprintf(w, "exit=%d", parsed.ExitCode)
				if pwd := strings.TrimSpace(parsed.FinalEnv["PWD"]); pwd != "" {
					_, _ = fmt.Fprintf(w, " pwd=%s", pwd)
				}
				if parsed.Error != "" {
					_, _ = fmt.Fprintf(w, " error=%s", parsed.Error)
				}
				_, _ = fmt.Fprintln(w)
				if parsed.Stdout != "" {
					_, _ = fmt.Fprintln(w, "stdout:")
					_, _ = fmt.Fprint(w, parsed.Stdout)
					if !strings.HasSuffix(parsed.Stdout, "\n") {
						_, _ = fmt.Fprintln(w)
					}
				}
				if parsed.Stderr != "" {
					_, _ = fmt.Fprintln(w, "stderr:")
					_, _ = fmt.Fprint(w, parsed.Stderr)
					if !strings.HasSuffix(parsed.Stderr, "\n") {
						_, _ = fmt.Fprintln(w)
					}
				}
				if parsed.StdoutTruncated || parsed.StderrTruncated {
					_, _ = fmt.Fprintf(w, "truncated stdout=%t stderr=%t\n", parsed.StdoutTruncated, parsed.StderrTruncated)
				}
			} else {
				encoded, _ := json.MarshalIndent(part.FunctionResponse.Response, "", "  ")
				_, _ = fmt.Fprintln(w, string(encoded))
			}
		case strings.TrimSpace(part.Text) != "":
			_, _ = fmt.Fprintln(w, "\n[assistant]")
			_, _ = fmt.Fprintln(w, strings.TrimSpace(part.Text))
		}
	}
}

func bashToolCommands(args map[string]any) string {
	if text := strings.TrimSpace(fmt.Sprint(args["commands"])); text != "" && text != "<nil>" {
		return text
	}
	if text := strings.TrimSpace(fmt.Sprint(args["script"])); text != "" && text != "<nil>" {
		return text
	}
	return ""
}

func decodeBashToolResponse(response any) (bashtool.Response, bool) {
	bytes, err := json.Marshal(response)
	if err != nil {
		return bashtool.Response{}, false
	}

	var parsed bashtool.Response
	if err := json.Unmarshal(bytes, &parsed); err != nil {
		return bashtool.Response{}, false
	}
	return parsed, true
}

func thinkingDisabledConfig() *genai.GenerateContentConfig {
	return &genai.GenerateContentConfig{
		ThinkingConfig: &genai.ThinkingConfig{
			ThinkingBudget: new(int32),
		},
	}
}

func jsonSchemaFromMap(raw map[string]any) (*jsonschema.Schema, error) {
	if raw == nil {
		return nil, nil
	}
	bytes, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(bytes, &schema); err != nil {
		return nil, err
	}
	return &schema, nil
}
