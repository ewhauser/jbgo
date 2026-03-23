//nolint:forbidigo // The standalone evaluator intentionally uses raw HTTP clients.
package gbasheval

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type openAIResponsesProvider struct {
	client      *http.Client
	apiKey      string
	model       string
	baseURL     string
	retryDelays []time.Duration
	retryWriter io.Writer
}

func (p *openAIResponsesProvider) Name() string  { return "openresponses" }
func (p *openAIResponsesProvider) Model() string { return p.model }

func (p *openAIResponsesProvider) Chat(ctx context.Context, messages []message, tools []toolDefinition, system string) (providerResponse, error) {
	body := p.buildRequestBody(messages, tools, system)
	url := strings.TrimRight(p.baseURL, "/") + "/v1/responses"

	for attempt := 0; ; attempt++ {
		respBody, status, err := doJSONRequest(ctx, p.client, http.MethodPost, url, map[string]string{
			"Authorization": "Bearer " + p.apiKey,
			"content-type":  "application/json",
		}, body)
		if err != nil {
			return providerResponse{}, fmt.Errorf("send request to OpenAI Responses API: %w", err)
		}
		if status >= 200 && status < 300 {
			return p.parseResponse(respBody)
		}

		retryable := status == 429 || status >= 500
		if retryable && attempt < len(p.retryDelays) {
			delay := p.retryDelays[attempt]
			retryMessage(p.retryWriter, "OpenAI Responses", status, delay, attempt+1, len(p.retryDelays))
			if delay > 0 {
				select {
				case <-ctx.Done():
					return providerResponse{}, ctx.Err()
				case <-time.After(delay):
				}
			}
			continue
		}

		return providerResponse{}, fmt.Errorf("OpenAI Responses API error (%d): %s", status, asString(asObject(respBody["error"])["message"]))
	}
}

func (p *openAIResponsesProvider) buildRequestBody(messages []message, tools []toolDefinition, system string) map[string]any {
	apiTools := make([]any, 0, len(tools))
	for _, tool := range tools {
		apiTools = append(apiTools, map[string]any{
			"type":        "function",
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  cloneMap(tool.InputSchema),
		})
	}

	body := map[string]any{
		"model":        p.model,
		"instructions": system,
		"input":        p.buildInputItems(messages),
		"tools":        apiTools,
		"store":        false,
	}
	if strings.Contains(p.model, "codex") {
		body["reasoning"] = map[string]any{"effort": "high"}
	}
	return body
}

func (p *openAIResponsesProvider) buildInputItems(messages []message) []any {
	items := make([]any, 0)
	for _, msg := range messages {
		switch msg.Role {
		case roleUser:
			var parts []string
			for _, block := range msg.Content {
				if block.Type == "text" {
					parts = append(parts, block.Text)
				}
			}
			items = append(items, map[string]any{
				"role":    "user",
				"content": strings.Join(parts, "\n"),
			})
		case roleAssistant:
			var content []any
			for _, block := range msg.Content {
				if block.Type == "text" {
					content = append(content, map[string]any{
						"type": "output_text",
						"text": block.Text,
					})
				}
			}
			if len(content) > 0 {
				items = append(items, map[string]any{
					"type":    "message",
					"role":    "assistant",
					"content": content,
				})
			}
			for _, block := range msg.Content {
				if block.Type != "tool_use" {
					continue
				}
				items = append(items, map[string]any{
					"type":      "function_call",
					"call_id":   block.ID,
					"name":      block.Name,
					"arguments": toJSONString(block.Input),
				})
			}
		case roleToolResult:
			for _, block := range msg.Content {
				if block.Type != "tool_result" {
					continue
				}
				items = append(items, map[string]any{
					"type":    "function_call_output",
					"call_id": block.ToolUseID,
					"output":  block.Content,
				})
			}
		}
	}
	return items
}

func (p *openAIResponsesProvider) parseResponse(body map[string]any) (providerResponse, error) {
	output := asArray(body["output"])
	if output == nil {
		return providerResponse{}, fmt.Errorf("no output array in OpenAI Responses response")
	}
	blocks := make([]contentBlock, 0)
	hasFunctionCalls := false
	for _, item := range output {
		obj := asObject(item)
		switch asString(obj["type"]) {
		case "message":
			for _, content := range asArray(obj["content"]) {
				part := asObject(content)
				if asString(part["type"]) == "output_text" {
					blocks = appendTextBlocks(blocks, asString(part["text"]))
				}
			}
		case "function_call":
			hasFunctionCalls = true
			input, err := parseToolArguments(asString(obj["arguments"]))
			if err != nil {
				return providerResponse{}, fmt.Errorf("decode tool arguments for %q: %w", asString(obj["name"]), err)
			}
			blocks = append(blocks, toolUseBlock(asString(obj["call_id"]), asString(obj["name"]), input))
		}
	}

	usage := asObject(body["usage"])
	status := asString(body["status"])
	return providerResponse{
		Message:      message{Role: roleAssistant, Content: blocks},
		Stop:         !hasFunctionCalls || status == "failed" || status == "canceled",
		InputTokens:  asUint32(usage["input_tokens"]),
		OutputTokens: asUint32(usage["output_tokens"]),
	}, nil
}
