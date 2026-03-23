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

type openAIChatProvider struct {
	client      *http.Client
	apiKey      string
	model       string
	baseURL     string
	retryDelays []time.Duration
	retryWriter io.Writer
}

func (p *openAIChatProvider) Name() string  { return "openai" }
func (p *openAIChatProvider) Model() string { return p.model }

func (p *openAIChatProvider) Chat(ctx context.Context, messages []message, tools []toolDefinition, system string) (providerResponse, error) {
	body := p.buildRequestBody(messages, tools, system)
	url := strings.TrimRight(p.baseURL, "/") + "/v1/chat/completions"

	for attempt := 0; ; attempt++ {
		respBody, status, err := doJSONRequest(ctx, p.client, http.MethodPost, url, map[string]string{
			"Authorization": "Bearer " + p.apiKey,
			"content-type":  "application/json",
		}, body)
		if err != nil {
			return providerResponse{}, fmt.Errorf("send request to OpenAI Chat Completions API: %w", err)
		}
		if status >= 200 && status < 300 {
			return p.parseResponse(respBody)
		}

		retryable := status == 429 || status >= 500
		if retryable && attempt < len(p.retryDelays) {
			delay := p.retryDelays[attempt]
			retryMessage(p.retryWriter, "OpenAI", status, delay, attempt+1, len(p.retryDelays))
			if delay > 0 {
				select {
				case <-ctx.Done():
					return providerResponse{}, ctx.Err()
				case <-time.After(delay):
				}
			}
			continue
		}

		return providerResponse{}, fmt.Errorf("OpenAI API error (%d): %s", status, asString(asObject(respBody["error"])["message"]))
	}
}

func (p *openAIChatProvider) buildRequestBody(messages []message, tools []toolDefinition, system string) map[string]any {
	apiMessages := []any{map[string]any{"role": "system", "content": system}}
	for _, msg := range messages {
		switch msg.Role {
		case roleUser:
			var parts []string
			for _, block := range msg.Content {
				if block.Type == "text" {
					parts = append(parts, block.Text)
				}
			}
			apiMessages = append(apiMessages, map[string]any{"role": "user", "content": strings.Join(parts, "\n")})
		case roleAssistant:
			item := map[string]any{"role": "assistant"}
			var parts []string
			var toolCalls []any
			for _, block := range msg.Content {
				switch block.Type {
				case "text":
					parts = append(parts, block.Text)
				case "tool_use":
					toolCalls = append(toolCalls, map[string]any{
						"id":   block.ID,
						"type": "function",
						"function": map[string]any{
							"name":      block.Name,
							"arguments": toJSONString(block.Input),
						},
					})
				}
			}
			if len(parts) > 0 {
				item["content"] = strings.Join(parts, "\n")
			}
			if len(toolCalls) > 0 {
				item["tool_calls"] = toolCalls
			}
			apiMessages = append(apiMessages, item)
		case roleToolResult:
			for _, block := range msg.Content {
				if block.Type != "tool_result" {
					continue
				}
				apiMessages = append(apiMessages, map[string]any{
					"role":         "tool",
					"tool_call_id": block.ToolUseID,
					"content":      block.Content,
				})
			}
		}
	}

	apiTools := make([]any, 0, len(tools))
	for _, tool := range tools {
		apiTools = append(apiTools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  cloneMap(tool.InputSchema),
			},
		})
	}

	return map[string]any{
		"model":    p.model,
		"messages": apiMessages,
		"tools":    apiTools,
	}
}

func (p *openAIChatProvider) parseResponse(body map[string]any) (providerResponse, error) {
	choices := asArray(body["choices"])
	if len(choices) == 0 {
		return providerResponse{}, fmt.Errorf("no choices in OpenAI response")
	}
	choice := asObject(choices[0])
	msgObj := asObject(choice["message"])
	blocks := appendOpenAITextContent(nil, msgObj["content"])
	for _, item := range asArray(msgObj["tool_calls"]) {
		call := asObject(item)
		fn := asObject(call["function"])
		input, err := parseToolArguments(asString(fn["arguments"]))
		if err != nil {
			return providerResponse{}, fmt.Errorf("decode tool arguments for %q: %w", asString(fn["name"]), err)
		}
		blocks = append(blocks, toolUseBlock(asString(call["id"]), asString(fn["name"]), input))
	}

	usage := asObject(body["usage"])
	return providerResponse{
		Message:      message{Role: roleAssistant, Content: blocks},
		Stop:         asString(choice["finish_reason"]) == "stop",
		InputTokens:  asUint32(usage["prompt_tokens"]),
		OutputTokens: asUint32(usage["completion_tokens"]),
	}, nil
}
