//nolint:forbidigo // The standalone evaluator intentionally reads datasets from the host filesystem.
package gbasheval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type MockBehavior struct {
	Static    *string
	Param     string
	Responses map[string]string
	Default   *string
}

func (m *MockBehavior) UnmarshalJSON(data []byte) error {
	var static string
	if err := json.Unmarshal(data, &static); err == nil {
		m.Static = &static
		m.Param = ""
		m.Responses = nil
		m.Default = nil
		return nil
	}

	var byParam struct {
		Param     string            `json:"param"`
		Responses map[string]string `json:"responses"`
		Default   *string           `json:"default"`
	}
	if err := json.Unmarshal(data, &byParam); err != nil {
		return err
	}
	m.Static = nil
	m.Param = byParam.Param
	m.Responses = byParam.Responses
	m.Default = byParam.Default
	return nil
}

func (m MockBehavior) MarshalJSON() ([]byte, error) {
	if m.Static != nil {
		return json.Marshal(*m.Static)
	}
	return json.Marshal(struct {
		Param     string            `json:"param"`
		Responses map[string]string `json:"responses"`
		Default   *string           `json:"default,omitempty"`
	}{
		Param:     m.Param,
		Responses: m.Responses,
		Default:   m.Default,
	})
}

type MockToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Category    string         `json:"category,omitempty"`
	Mock        MockBehavior   `json:"mock"`
}

type ScriptingEvalTask struct {
	ID            string            `json:"id"`
	Category      string            `json:"category"`
	Description   string            `json:"description"`
	System        string            `json:"system,omitempty"`
	Prompt        string            `json:"prompt"`
	Tools         []MockToolDef     `json:"tools"`
	Files         map[string]string `json:"files,omitempty"`
	DiscoveryMode bool              `json:"discovery_mode,omitempty"`
	Expectations  []Expectation     `json:"expectations"`
}

func (t *ScriptingEvalTask) normalize() {
	if t.Files == nil {
		t.Files = map[string]string{}
	}
	for i := range t.Tools {
		t.Tools[i].Schema = normalizeSchema(t.Tools[i].Schema)
		if t.Tools[i].Tags == nil {
			t.Tools[i].Tags = []string{}
		}
	}
	for i := range t.Expectations {
		t.Expectations[i].normalize()
	}
}

func loadScriptingDataset(path string) ([]ScriptingEvalTask, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read dataset %q: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	var tasks []ScriptingEvalTask
	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		var task ScriptingEvalTask
		if err := decodeJSONObject([]byte(line), &task); err != nil {
			return nil, fmt.Errorf("parse dataset %q line %d: %w", path, lineNo, err)
		}
		task.normalize()
		tasks = append(tasks, task)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan dataset %q: %w", path, err)
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("dataset is empty: %s", path)
	}
	return tasks, nil
}

type mockCallInput struct {
	Params map[string]any
	Stdin  string
}

func (m MockBehavior) execute(input mockCallInput) (string, error) {
	if m.Static != nil {
		return *m.Static, nil
	}
	key := stringifyJSONValue(input.Params[m.Param])
	if response, ok := m.Responses[key]; ok {
		return response, nil
	}
	if m.Default != nil {
		return *m.Default, nil
	}
	return "", fmt.Errorf("no mock response for %s=%s", m.Param, key)
}

func stringifyJSONValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		return strings.TrimSpace(toJSONString(typed))
	}
}
