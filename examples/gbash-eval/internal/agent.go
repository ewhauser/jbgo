//nolint:gocritic // Internal evaluator wiring favors simpler value semantics than pointer-heavy call signatures.
package gbasheval

import (
	"context"
	"fmt"
	"time"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/contrib/bashtool"
	"github.com/ewhauser/gbash/contrib/extras"
	gbfs "github.com/ewhauser/gbash/fs"
)

func runAgentLoop(ctx context.Context, provider Provider, task EvalTask, maxTurns int) (agentTrace, gbfs.FileSystem, error) {
	gb, err := gbash.New( //nolint:contextcheck // gbash.New does not accept a context.
		gbash.WithRegistry(extras.FullRegistry()),
		gbash.WithFileSystem(seedFiles(task.Files)),
	)
	if err != nil {
		return agentTrace{}, nil, fmt.Errorf("create runtime: %w", err)
	}
	session, err := gb.NewSession(ctx)
	if err != nil {
		return agentTrace{}, nil, fmt.Errorf("create session: %w", err)
	}

	messages := []message{{
		Role: roleUser,
		Content: []contentBlock{{
			Type: "text",
			Text: task.Prompt,
		}},
	}}

	var toolCalls []toolCallResult
	var lastToolResponse *toolCallResult
	var naturalStop bool
	var inputTokens uint32
	var outputTokens uint32
	start := time.Now()
	currentTrace := func() agentTrace {
		return agentTrace{
			Messages:          messages,
			ToolCalls:         toolCalls,
			ToolCallCount:     len(toolCalls),
			Turns:             countAssistantTurns(messages),
			LastToolResponse:  lastToolResponse,
			NaturalStop:       naturalStop,
			TotalInputTokens:  inputTokens,
			TotalOutputTokens: outputTokens,
			DurationMS:        uint64(time.Since(start).Milliseconds()),
		}
	}

	tool := evalBashTool()
	toolDef := bashToolDefinition()
	system := tool.SystemPrompt()
	if task.System != "" {
		system = task.System
	}

	for range maxTurns {
		resp, err := provider.Chat(ctx, messages, []toolDefinition{toolDef}, system)
		if err != nil {
			return currentTrace(), session.FileSystem(), fmt.Errorf("provider chat: %w", err)
		}
		inputTokens += resp.InputTokens
		outputTokens += resp.OutputTokens
		messages = append(messages, resp.Message)
		if resp.Stop {
			naturalStop = true
			break
		}

		toolUses := extractToolUses(resp.Message)
		if len(toolUses) == 0 {
			naturalStop = true
			break
		}

		var resultBlocks []contentBlock
		for _, use := range toolUses {
			req, parseErr := bashtool.ParseRequest(use.Input)
			commands := ""
			resp := bashtool.Response{
				Stdout:   "",
				Stderr:   "",
				ExitCode: 0,
			}
			if parseErr != nil {
				resp.Stderr = parseErr.Error()
				resp.ExitCode = 1
				resp.Error = "parse_error"
			} else {
				commands = req.ResolvedCommands()
				result, err := session.Exec(ctx, &gbash.ExecutionRequest{
					Script:  commands,
					Timeout: req.Timeout(),
				})
				if err != nil {
					resp.Stderr = err.Error()
					resp.ExitCode = 1
					resp.Error = "execution_error"
				} else {
					resp.Stdout = result.Stdout
					resp.Stderr = result.Stderr
					resp.ExitCode = result.ExitCode
					resp.StdoutTruncated = result.StdoutTruncated
					resp.StderrTruncated = result.StderrTruncated
					resp.FinalEnv = result.FinalEnv
				}
			}
			call := toolCallResult{
				Commands: commands,
				Stdout:   resp.Stdout,
				Stderr:   resp.Stderr,
				ExitCode: resp.ExitCode,
			}
			toolCalls = append(toolCalls, call)
			last := call
			lastToolResponse = &last
			resultBlocks = append(resultBlocks, contentBlock{
				Type:      "tool_result",
				ToolUseID: use.ID,
				Content:   bashtool.FormatToolResult(resp),
				IsError:   resp.ExitCode != 0,
			})
		}
		messages = append(messages, message{Role: roleToolResult, Content: resultBlocks})
	}

	return currentTrace(), session.FileSystem(), nil
}

func extractToolUses(msg message) []contentBlock {
	var out []contentBlock
	for _, block := range msg.Content {
		if block.Type == "tool_use" {
			out = append(out, block)
		}
	}
	return out
}

func countAssistantTurns(messages []message) int {
	count := 0
	for _, msg := range messages {
		if msg.Role == roleAssistant {
			count++
		}
	}
	return count
}

func seedFiles(files map[string]string) gbash.FileSystemConfig {
	if len(files) == 0 {
		return gbash.InMemoryFileSystem()
	}
	initial := make(gbfs.InitialFiles, len(files))
	for path, content := range files {
		initial[path] = gbfs.InitialFile{Content: []byte(content)}
	}
	return gbash.SeededInMemoryFileSystem(initial)
}
