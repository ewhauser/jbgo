package gbasheval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type fakeProvider struct {
	t         *testing.T
	name      string
	model     string
	responses []providerResponse
	errs      []error
	hook      func(call int, messages []message, tools []toolDefinition, system string)
	calls     int
}

func (p *fakeProvider) Chat(_ context.Context, messages []message, tools []toolDefinition, system string) (providerResponse, error) {
	p.t.Helper()
	if p.hook != nil {
		p.hook(p.calls, messages, tools, system)
	}
	if p.calls >= len(p.responses) {
		p.t.Fatalf("unexpected provider call %d", p.calls+1)
	}
	resp := p.responses[p.calls]
	var err error
	if p.calls < len(p.errs) {
		err = p.errs[p.calls]
	}
	p.calls++
	return resp, err
}

func (p *fakeProvider) Name() string {
	if p.name != "" {
		return p.name
	}
	return "fake"
}

func (p *fakeProvider) Model() string {
	if p.model != "" {
		return p.model
	}
	return "fake-model"
}

func assistantToolResponse(id, name string, input map[string]any) providerResponse {
	return providerResponse{
		Message: message{
			Role: roleAssistant,
			Content: []contentBlock{
				toolUseBlock(id, name, input),
			},
		},
		InputTokens:  10,
		OutputTokens: 5,
	}
}

func assistantStopResponse() providerResponse {
	return providerResponse{
		Message: message{
			Role: roleAssistant,
			Content: []contentBlock{{
				Type: "text",
				Text: "done",
			}},
		},
		Stop:         true,
		InputTokens:  3,
		OutputTokens: 2,
	}
}

func writeTempFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	return path
}

func mustReadOSFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	return string(data)
}

func singleJSONLTask(task any) string {
	return fmt.Sprintf("%s\n", toJSONString(task))
}
