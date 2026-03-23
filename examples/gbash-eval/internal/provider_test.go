package gbasheval

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAnthropicProviderRetriesAndParsesResponse(t *testing.T) {
	t.Parallel()

	var attempts int
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %q, want /v1/messages", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "anthropic-test" {
			t.Fatalf("x-api-key = %q, want anthropic-test", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if attempts == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"retry later"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"run shell"},{"type":"tool_use","id":"call_1","name":"bash","input":{"commands":"pwd"}}],"stop_reason":"tool_use","usage":{"input_tokens":11,"output_tokens":7}}`))
	}))
	defer server.Close()

	var retryLog bytes.Buffer
	provider := &anthropicProvider{
		client:      server.Client(),
		apiKey:      "anthropic-test",
		model:       "claude-test",
		baseURL:     server.URL,
		retryDelays: []time.Duration{0},
		retryWriter: &retryLog,
	}

	resp, err := provider.Chat(context.Background(), []message{{
		Role: roleUser,
		Content: []contentBlock{{
			Type: "text",
			Text: "show cwd",
		}},
	}}, []toolDefinition{bashToolDefinition()}, "system text")
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if !strings.Contains(retryLog.String(), "[retry] Anthropic 429") {
		t.Fatalf("retry log = %q, want Anthropic retry message", retryLog.String())
	}
	if got := asString(captured["model"]); got != "claude-test" {
		t.Fatalf("request model = %q, want claude-test", got)
	}
	if got := asString(captured["system"]); got != "system text" {
		t.Fatalf("request system = %q, want system text", got)
	}
	if got := asString(asObject(asArray(captured["messages"])[0])["role"]); got != "user" {
		t.Fatalf("request message role = %q, want user", got)
	}
	if resp.Stop {
		t.Fatal("resp.Stop = true, want false when tool_use is returned")
	}
	if resp.InputTokens != 11 || resp.OutputTokens != 7 {
		t.Fatalf("tokens = %d/%d, want 11/7", resp.InputTokens, resp.OutputTokens)
	}
	if len(resp.Message.Content) != 2 || resp.Message.Content[1].Type != "tool_use" {
		t.Fatalf("response content = %#v, want text + tool_use", resp.Message.Content)
	}
}

func TestOpenAIChatProviderNormalizesMessagesAndParsesToolCalls(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer openai-test" {
			t.Fatalf("authorization = %q, want Bearer openai-test", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"content":"done","tool_calls":[{"id":"tc_1","type":"function","function":{"name":"bash","arguments":"{\"commands\":\"echo hi\"}"}}]}}],"usage":{"prompt_tokens":13,"completion_tokens":9}}`))
	}))
	defer server.Close()

	provider := &openAIChatProvider{
		client:      server.Client(),
		apiKey:      "openai-test",
		model:       "gpt-test",
		baseURL:     server.URL,
		retryDelays: []time.Duration{},
	}

	resp, err := provider.Chat(context.Background(), []message{
		{
			Role: roleUser,
			Content: []contentBlock{{
				Type: "text",
				Text: "first",
			}},
		},
		{
			Role: roleAssistant,
			Content: []contentBlock{
				{Type: "text", Text: "working"},
				toolUseBlock("call_1", "bash", map[string]any{"commands": "pwd"}),
			},
		},
		{
			Role: roleToolResult,
			Content: []contentBlock{{
				Type:      "tool_result",
				ToolUseID: "call_1",
				Content:   "/home/agent",
			}},
		},
	}, []toolDefinition{bashToolDefinition()}, "system text")
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	messages := asArray(captured["messages"])
	if len(messages) != 4 {
		t.Fatalf("len(messages) = %d, want 4", len(messages))
	}
	if got := asString(asObject(messages[0])["role"]); got != "system" {
		t.Fatalf("messages[0].role = %q, want system", got)
	}
	if got := asString(asObject(messages[3])["role"]); got != "tool" {
		t.Fatalf("messages[3].role = %q, want tool", got)
	}
	if resp.Stop {
		t.Fatal("resp.Stop = true, want false when tool_calls are returned")
	}
	if resp.InputTokens != 13 || resp.OutputTokens != 9 {
		t.Fatalf("tokens = %d/%d, want 13/9", resp.InputTokens, resp.OutputTokens)
	}
	if len(resp.Message.Content) != 2 || resp.Message.Content[1].Name != "bash" {
		t.Fatalf("response content = %#v, want text + bash tool call", resp.Message.Content)
	}
}

func TestOpenAIChatProviderRejectsMalformedToolArguments(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[{"id":"tc_1","type":"function","function":{"name":"bash","arguments":"{\"commands\":"}}]}}]}`))
	}))
	defer server.Close()

	provider := &openAIChatProvider{
		client:      server.Client(),
		apiKey:      "openai-test",
		model:       "gpt-test",
		baseURL:     server.URL,
		retryDelays: []time.Duration{},
	}

	_, err := provider.Chat(context.Background(), []message{{
		Role: roleUser,
		Content: []contentBlock{{
			Type: "text",
			Text: "run bash",
		}},
	}}, []toolDefinition{bashToolDefinition()}, "system text")
	if err == nil {
		t.Fatal("Chat() error = nil, want malformed tool arguments error")
	}
	if !strings.Contains(err.Error(), `decode tool arguments for "bash"`) {
		t.Fatalf("error = %q, want decode tool arguments context", err)
	}
}

func TestOpenAIResponsesProviderBuildsInputAndParsesFunctionCalls(t *testing.T) {
	t.Parallel()

	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("path = %q, want /v1/responses", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Need shell"}]},{"type":"function_call","call_id":"fc_1","name":"bash","arguments":"{\"commands\":\"echo hi\"}"}],"usage":{"input_tokens":5,"output_tokens":9}}`))
	}))
	defer server.Close()

	provider := &openAIResponsesProvider{
		client:      server.Client(),
		apiKey:      "openai-test",
		model:       "gpt-5-codex",
		baseURL:     server.URL,
		retryDelays: []time.Duration{},
	}

	resp, err := provider.Chat(context.Background(), []message{
		{
			Role: roleUser,
			Content: []contentBlock{{
				Type: "text",
				Text: "first",
			}},
		},
		{
			Role: roleAssistant,
			Content: []contentBlock{
				{Type: "text", Text: "working"},
				toolUseBlock("call_1", "bash", map[string]any{"commands": "pwd"}),
			},
		},
		{
			Role: roleToolResult,
			Content: []contentBlock{{
				Type:      "tool_result",
				ToolUseID: "call_1",
				Content:   "/home/agent",
			}},
		},
	}, []toolDefinition{bashToolDefinition()}, "system text")
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if got := asString(asObject(captured["reasoning"])["effort"]); got != "high" {
		t.Fatalf("reasoning effort = %q, want high", got)
	}
	items := asArray(captured["input"])
	if len(items) != 4 {
		t.Fatalf("len(input items) = %d, want 4", len(items))
	}
	if got := asString(asObject(items[1])["type"]); got != "message" {
		t.Fatalf("items[1].type = %q, want message", got)
	}
	if got := asString(asObject(items[2])["type"]); got != "function_call" {
		t.Fatalf("items[2].type = %q, want function_call", got)
	}
	if resp.Stop {
		t.Fatal("resp.Stop = true, want false when a function_call is returned")
	}
	if len(resp.Message.Content) != 2 || resp.Message.Content[1].ID != "fc_1" {
		t.Fatalf("response content = %#v, want text + function_call", resp.Message.Content)
	}
}

func TestOpenAIResponsesProviderRejectsMalformedToolArguments(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"function_call","call_id":"fc_1","name":"bash","arguments":"{\"commands\":"}]}`))
	}))
	defer server.Close()

	provider := &openAIResponsesProvider{
		client:      server.Client(),
		apiKey:      "openai-test",
		model:       "gpt-5-codex",
		baseURL:     server.URL,
		retryDelays: []time.Duration{},
	}

	_, err := provider.Chat(context.Background(), []message{{
		Role: roleUser,
		Content: []contentBlock{{
			Type: "text",
			Text: "run bash",
		}},
	}}, []toolDefinition{bashToolDefinition()}, "system text")
	if err == nil {
		t.Fatal("Chat() error = nil, want malformed tool arguments error")
	}
	if !strings.Contains(err.Error(), `decode tool arguments for "bash"`) {
		t.Fatalf("error = %q, want decode tool arguments context", err)
	}
}
