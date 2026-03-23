//nolint:forbidigo // The standalone evaluator intentionally reads datasets from the host filesystem.
package gbasheval

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type Expectation struct {
	Check  string  `json:"check"`
	Weight float64 `json:"weight"`
}

type EvalTask struct {
	ID           string            `json:"id"`
	Category     string            `json:"category"`
	Description  string            `json:"description"`
	System       string            `json:"system,omitempty"`
	Prompt       string            `json:"prompt"`
	Files        map[string]string `json:"files,omitempty"`
	Expectations []Expectation     `json:"expectations"`
}

func (e *Expectation) normalize() {
	if e.Weight == 0 {
		e.Weight = 1
	}
}

func (t *EvalTask) normalize() {
	if t.Files == nil {
		t.Files = map[string]string{}
	}
	for i := range t.Expectations {
		t.Expectations[i].normalize()
	}
}

func loadDataset(path string) ([]EvalTask, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read dataset %q: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	var tasks []EvalTask
	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}

		var task EvalTask
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
