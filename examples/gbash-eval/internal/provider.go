//nolint:forbidigo // The standalone evaluator intentionally uses host env vars and raw HTTP clients.
package gbasheval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type Provider interface {
	Chat(ctx context.Context, messages []message, tools []toolDefinition, system string) (providerResponse, error)
	Name() string
	Model() string
}

func createProvider(name, model string, retryWriter io.Writer) (Provider, error) {
	switch name {
	case "anthropic":
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY env var not set")
		}
		return &anthropicProvider{
			client:      &http.Client{Timeout: 60 * time.Second},
			apiKey:      apiKey,
			model:       model,
			baseURL:     "https://api.anthropic.com",
			retryDelays: defaultRetryDelays(),
			retryWriter: retryWriter,
		}, nil
	case "openai":
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY env var not set")
		}
		return &openAIChatProvider{
			client:      &http.Client{Timeout: 60 * time.Second},
			apiKey:      apiKey,
			model:       model,
			baseURL:     "https://api.openai.com",
			retryDelays: defaultRetryDelays(),
			retryWriter: retryWriter,
		}, nil
	case "openresponses":
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY env var not set")
		}
		return &openAIResponsesProvider{
			client:      &http.Client{Timeout: 60 * time.Second},
			apiKey:      apiKey,
			model:       model,
			baseURL:     "https://api.openai.com",
			retryDelays: defaultRetryDelays(),
			retryWriter: retryWriter,
		}, nil
	default:
		return nil, fmt.Errorf("unknown provider %q (use anthropic, openai, or openresponses)", name)
	}
}

func defaultRetryDelays() []time.Duration {
	return []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
}

func doJSONRequest(ctx context.Context, client *http.Client, method, url string, headers map[string]string, body any) (map[string]any, int, error) {
	reqBody, err := json.Marshal(body)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal request body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response body: %w", err)
	}
	if len(raw) == 0 {
		return map[string]any{}, resp.StatusCode, nil
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("decode response json: %w", err)
	}
	return decoded, resp.StatusCode, nil
}

func asUint32(value any) uint32 {
	switch typed := value.(type) {
	case float64:
		return uint32(typed)
	case float32:
		return uint32(typed)
	case int:
		return uint32(typed)
	case int64:
		return uint32(typed)
	case json.Number:
		v, _ := typed.Int64()
		return uint32(v)
	default:
		return 0
	}
}

func appendTextBlocks(blocks []contentBlock, text string) []contentBlock {
	text = strings.TrimSpace(text)
	if text == "" {
		return blocks
	}
	return append(blocks, contentBlock{Type: "text", Text: text})
}

func appendOpenAITextContent(blocks []contentBlock, content any) []contentBlock {
	switch typed := content.(type) {
	case string:
		return appendTextBlocks(blocks, typed)
	case []any:
		for _, item := range typed {
			obj := asObject(item)
			if asString(obj["type"]) == "text" {
				blocks = appendTextBlocks(blocks, asString(obj["text"]))
			}
		}
	}
	return blocks
}

func toolUseBlock(id, name string, input map[string]any) contentBlock {
	return contentBlock{
		Type:  "tool_use",
		ID:    id,
		Name:  name,
		Input: input,
	}
}

func parseToolArguments(raw string) map[string]any {
	if raw == "" {
		return map[string]any{}
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return map[string]any{}
	}
	return decoded
}

func retryMessage(w io.Writer, provider string, status int, delay time.Duration, attempt, total int) {
	if w == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "  [retry] %s %d - waiting %s (attempt %d/%d)\n", provider, status, delay, attempt, total)
}
