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

type anthropicProvider struct {
	client      *http.Client
	apiKey      string
	model       string
	baseURL     string
	retryDelays []time.Duration
	retryWriter io.Writer
}

func (p *anthropicProvider) Name() string  { return "anthropic" }
func (p *anthropicProvider) Model() string { return p.model }

func (p *anthropicProvider) Chat(ctx context.Context, messages []message, tools []toolDefinition, system string) (providerResponse, error) {
	body := p.buildRequestBody(messages, tools, system)
	url := strings.TrimRight(p.baseURL, "/") + "/v1/messages"

	for attempt := 0; ; attempt++ {
		respBody, status, err := doJSONRequest(ctx, p.client, http.MethodPost, url, map[string]string{
			"x-api-key":         p.apiKey,
			"anthropic-version": "2023-06-01",
			"content-type":      "application/json",
		}, body)
		if err != nil {
			return providerResponse{}, fmt.Errorf("send request to Anthropic API: %w", err)
		}
		if status >= 200 && status < 300 {
			return p.parseResponse(respBody)
		}

		retryable := status == 429 || status == 529
		if retryable && attempt < len(p.retryDelays) {
			delay := p.retryDelays[attempt]
			retryMessage(p.retryWriter, "Anthropic", status, delay, attempt+1, len(p.retryDelays))
			if delay > 0 {
				select {
				case <-ctx.Done():
					return providerResponse{}, ctx.Err()
				case <-time.After(delay):
				}
			}
			continue
		}

		return providerResponse{}, fmt.Errorf("anthropic API error (%d): %s", status, asString(asObject(respBody["error"])["message"]))
	}
}

func (p *anthropicProvider) buildRequestBody(messages []message, tools []toolDefinition, system string) map[string]any {
	apiMessages := make([]any, 0, len(messages))
	for _, msg := range messages {
		roleName := "assistant"
		switch msg.Role {
		case roleUser:
			roleName = "user"
		case roleAssistant:
			roleName = "assistant"
		case roleToolResult:
			roleName = "user"
		}

		content := make([]any, 0, len(msg.Content))
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				content = append(content, map[string]any{"type": "text", "text": block.Text})
			case "tool_use":
				content = append(content, map[string]any{
					"type":  "tool_use",
					"id":    block.ID,
					"name":  block.Name,
					"input": cloneMap(block.Input),
				})
			case "tool_result":
				content = append(content, map[string]any{
					"type":        "tool_result",
					"tool_use_id": block.ToolUseID,
					"content":     block.Content,
					"is_error":    block.IsError,
				})
			}
		}
		apiMessages = append(apiMessages, map[string]any{
			"role":    roleName,
			"content": content,
		})
	}

	apiTools := make([]any, 0, len(tools))
	for _, tool := range tools {
		apiTools = append(apiTools, map[string]any{
			"name":         tool.Name,
			"description":  tool.Description,
			"input_schema": cloneMap(tool.InputSchema),
		})
	}

	return map[string]any{
		"model":      p.model,
		"max_tokens": 4096,
		"system":     system,
		"messages":   apiMessages,
		"tools":      apiTools,
	}
}

func (p *anthropicProvider) parseResponse(body map[string]any) (providerResponse, error) {
	blocks := make([]contentBlock, 0)
	for _, item := range asArray(body["content"]) {
		obj := asObject(item)
		switch asString(obj["type"]) {
		case "text":
			blocks = appendTextBlocks(blocks, asString(obj["text"]))
		case "tool_use":
			blocks = append(blocks, toolUseBlock(asString(obj["id"]), asString(obj["name"]), asObject(obj["input"])))
		}
	}

	usage := asObject(body["usage"])
	return providerResponse{
		Message: message{
			Role:    roleAssistant,
			Content: blocks,
		},
		Stop:         asString(body["stop_reason"]) == "end_turn",
		InputTokens:  asUint32(usage["input_tokens"]),
		OutputTokens: asUint32(usage["output_tokens"]),
	}, nil
}
