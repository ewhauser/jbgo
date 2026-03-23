//nolint:gocritic // Internal evaluator wiring favors simpler value semantics than pointer-heavy call signatures.
package gbasheval

import (
	"context"
	"fmt"
	"time"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/contrib/bashtool"
)

type ScriptingToolCall struct {
	ToolName    string                      `json:"tool_name"`
	Input       map[string]any              `json:"input"`
	Output      string                      `json:"output"`
	Stderr      string                      `json:"stderr"`
	ExitCode    int                         `json:"exit_code"`
	Invocations []ScriptedCommandInvocation `json:"invocations"`
}

type ScriptingTrace struct {
	Messages            []message           `json:"messages"`
	ToolCalls           []ScriptingToolCall `json:"tool_calls"`
	ToolCallCount       int                 `json:"tool_call_count"`
	Turns               int                 `json:"turns"`
	NaturalStop         bool                `json:"natural_stop"`
	TotalInputTokens    uint32              `json:"total_input_tokens"`
	TotalOutputTokens   uint32              `json:"total_output_tokens"`
	DurationMS          uint64              `json:"duration_ms"`
	Baseline            bool                `json:"baseline"`
	RawToolOutputBytes  int                 `json:"raw_tool_output_bytes"`
	ToolOutputSentBytes int                 `json:"tool_output_sent_bytes"`
}

func (t ScriptingTrace) InnerCommandCount() int {
	total := 0
	for _, call := range t.ToolCalls {
		total += len(call.Invocations)
	}
	return total
}

func (t ScriptingTrace) InnerCommandCountByKind(kind ScriptedCommandKind) int {
	total := 0
	for _, call := range t.ToolCalls {
		for _, invocation := range call.Invocations {
			if invocation.Kind == kind {
				total++
			}
		}
	}
	return total
}

func runScriptedAgent(ctx context.Context, provider Provider, task ScriptingEvalTask, maxTurns int) (ScriptingTrace, error) {
	session, state, err := newScriptedSession(ctx, task)
	if err != nil {
		return ScriptingTrace{}, err
	}

	toolDef := scriptedToolDefinition(task.ID)
	messages := []message{{
		Role: roleUser,
		Content: []contentBlock{{
			Type: "text",
			Text: task.Prompt,
		}},
	}}

	var toolCalls []ScriptingToolCall
	var naturalStop bool
	var inputTokens uint32
	var outputTokens uint32
	var rawBytes int
	var sentBytes int
	start := time.Now()
	currentTrace := func() ScriptingTrace {
		return ScriptingTrace{
			Messages:            messages,
			ToolCalls:           toolCalls,
			ToolCallCount:       len(toolCalls),
			Turns:               countAssistantTurns(messages),
			NaturalStop:         naturalStop,
			TotalInputTokens:    inputTokens,
			TotalOutputTokens:   outputTokens,
			DurationMS:          uint64(time.Since(start).Milliseconds()),
			Baseline:            false,
			RawToolOutputBytes:  rawBytes,
			ToolOutputSentBytes: sentBytes,
		}
	}

	for range maxTurns {
		resp, err := provider.Chat(ctx, messages, []toolDefinition{toolDef}, scriptedSystemPrompt(task))
		if err != nil {
			return currentTrace(), fmt.Errorf("provider chat: %w", err)
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
			commands := extractCommands(use.Input)
			state.beginExec()
			result, err := session.Exec(ctx, &gbash.ExecutionRequest{Script: commands})
			invocations := state.finishExec()
			stdout := ""
			stderr := ""
			exitCode := 1
			if err != nil {
				stderr = err.Error()
			} else {
				stdout = result.Stdout
				stderr = result.Stderr
				exitCode = result.ExitCode
			}
			rawBytes += len(stdout) + len(stderr)
			content := bashtool.FormatToolResult(bashtool.Response{
				Stdout:   stdout,
				Stderr:   stderr,
				ExitCode: exitCode,
			})
			sentBytes += len(content)
			toolCalls = append(toolCalls, ScriptingToolCall{
				ToolName:    toolDef.Name,
				Input:       cloneMap(use.Input),
				Output:      stdout,
				Stderr:      stderr,
				ExitCode:    exitCode,
				Invocations: invocations,
			})
			resultBlocks = append(resultBlocks, contentBlock{
				Type:      "tool_result",
				ToolUseID: use.ID,
				Content:   content,
				IsError:   exitCode != 0,
			})
		}
		messages = append(messages, message{Role: roleToolResult, Content: resultBlocks})
	}

	return currentTrace(), nil
}

func runBaselineAgent(ctx context.Context, provider Provider, task ScriptingEvalTask, maxTurns int) (ScriptingTrace, error) {
	toolDefs := make([]toolDefinition, 0, len(task.Tools))
	toolByName := make(map[string]MockToolDef, len(task.Tools))
	for _, tool := range task.Tools {
		toolDefs = append(toolDefs, toolDefinition{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: normalizeSchema(tool.Schema),
		})
		toolByName[tool.Name] = tool
	}

	system := task.System
	if system == "" {
		system = baselineSystemPrompt(task.Tools)
	}

	messages := []message{{
		Role: roleUser,
		Content: []contentBlock{{
			Type: "text",
			Text: task.Prompt,
		}},
	}}

	var toolCalls []ScriptingToolCall
	var naturalStop bool
	var inputTokens uint32
	var outputTokens uint32
	var rawBytes int
	var sentBytes int
	start := time.Now()
	currentTrace := func() ScriptingTrace {
		return ScriptingTrace{
			Messages:            messages,
			ToolCalls:           toolCalls,
			ToolCallCount:       len(toolCalls),
			Turns:               countAssistantTurns(messages),
			NaturalStop:         naturalStop,
			TotalInputTokens:    inputTokens,
			TotalOutputTokens:   outputTokens,
			DurationMS:          uint64(time.Since(start).Milliseconds()),
			Baseline:            true,
			RawToolOutputBytes:  rawBytes,
			ToolOutputSentBytes: sentBytes,
		}
	}

	for range maxTurns {
		resp, err := provider.Chat(ctx, messages, toolDefs, system)
		if err != nil {
			return currentTrace(), fmt.Errorf("provider chat: %w", err)
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
			tool, ok := toolByName[use.Name]
			stdout := ""
			stderr := ""
			exitCode := 0
			if !ok {
				stderr = "unknown tool: " + use.Name
				exitCode = 1
			} else {
				output, err := tool.Mock.execute(mockCallInput{Params: use.Input})
				if err != nil {
					stderr = err.Error()
					exitCode = 1
				} else {
					stdout = output
				}
			}
			rawBytes += len(stdout) + len(stderr)
			content := bashtool.FormatToolResult(bashtool.Response{
				Stdout:   stdout,
				Stderr:   stderr,
				ExitCode: exitCode,
			})
			sentBytes += len(content)
			toolCalls = append(toolCalls, ScriptingToolCall{
				ToolName: use.Name,
				Input:    cloneMap(use.Input),
				Output:   stdout,
				Stderr:   stderr,
				ExitCode: exitCode,
				Invocations: []ScriptedCommandInvocation{{
					Name:     use.Name,
					Kind:     ScriptedCommandKindTool,
					Args:     nil,
					ExitCode: exitCode,
				}},
			})
			resultBlocks = append(resultBlocks, contentBlock{
				Type:      "tool_result",
				ToolUseID: use.ID,
				Content:   content,
				IsError:   exitCode != 0,
			})
		}
		messages = append(messages, message{Role: roleToolResult, Content: resultBlocks})
	}

	return currentTrace(), nil
}
