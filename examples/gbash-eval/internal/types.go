package gbasheval

import (
	"encoding/json"
	"fmt"
)

type role string

const (
	roleUser       role = "user"
	roleAssistant  role = "assistant"
	roleToolResult role = "tool_result"
)

type contentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   string         `json:"content,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
}

type message struct {
	Role    role           `json:"role"`
	Content []contentBlock `json:"content"`
}

type toolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type providerResponse struct {
	Message      message
	Stop         bool
	InputTokens  uint32
	OutputTokens uint32
}

type toolCallResult struct {
	Commands string `json:"commands"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

func failureScore(taskID string, expectations []Expectation, err error) TaskScore {
	results := make([]ScoreResult, 0, len(expectations))
	detail := fmt.Sprintf("task execution failed: %v", err)
	if len(expectations) == 0 {
		results = append(results, ScoreResult{
			Check:  "task_error",
			Passed: false,
			Detail: detail,
			Weight: 0,
		})
	}
	for _, exp := range expectations {
		results = append(results, ScoreResult{
			Check:  exp.Check,
			Passed: false,
			Detail: detail,
			Weight: exp.Weight,
		})
	}

	var maxScore float64
	for _, result := range results {
		maxScore += result.Weight
	}

	return TaskScore{
		TaskID:   taskID,
		Results:  results,
		Score:    0,
		MaxScore: maxScore,
	}
}

type agentTrace struct {
	Messages          []message        `json:"messages"`
	ToolCalls         []toolCallResult `json:"tool_calls"`
	ToolCallCount     int              `json:"tool_call_count"`
	Turns             int              `json:"turns"`
	LastToolResponse  *toolCallResult  `json:"last_tool_response,omitempty"`
	NaturalStop       bool             `json:"natural_stop"`
	TotalInputTokens  uint32           `json:"total_input_tokens"`
	TotalOutputTokens uint32           `json:"total_output_tokens"`
	DurationMS        uint64           `json:"duration_ms"`
}

func cloneMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = deepCloneJSONValue(value)
	}
	return out
}

func deepCloneJSONValue(value any) any {
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return value
	}
	return out
}
