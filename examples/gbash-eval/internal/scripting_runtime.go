//nolint:gocritic // Internal evaluator wiring favors simpler value semantics than pointer-heavy call signatures.
package gbasheval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/commands"
	"github.com/ewhauser/gbash/contrib/extras"
)

type ScriptedCommandKind string

const (
	ScriptedCommandKindTool     ScriptedCommandKind = "tool"
	ScriptedCommandKindHelp     ScriptedCommandKind = "help"
	ScriptedCommandKindDiscover ScriptedCommandKind = "discover"
)

type ScriptedCommandInvocation struct {
	Name     string              `json:"name"`
	Kind     ScriptedCommandKind `json:"kind"`
	Args     []string            `json:"args"`
	ExitCode int                 `json:"exit_code"`
}

type scriptedExecutionState struct {
	mu      sync.Mutex
	current []ScriptedCommandInvocation
}

func (s *scriptedExecutionState) beginExec() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = []ScriptedCommandInvocation{}
}

func (s *scriptedExecutionState) record(name string, kind ScriptedCommandKind, args []string, exitCode int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = append(s.current, ScriptedCommandInvocation{
		Name:     name,
		Kind:     kind,
		Args:     append([]string(nil), args...),
		ExitCode: exitCode,
	})
}

func (s *scriptedExecutionState) finishExec() []ScriptedCommandInvocation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]ScriptedCommandInvocation(nil), s.current...)
	s.current = nil
	return out
}

func newScriptedSession(ctx context.Context, task ScriptingEvalTask) (*gbash.Session, *scriptedExecutionState, error) {
	registry := extras.FullRegistry()
	stockHelp, _ := registry.Lookup("help")

	state := &scriptedExecutionState{}
	tools := append([]MockToolDef(nil), task.Tools...)
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	for _, tool := range tools {
		if err := registry.Register(newMockToolCommand(tool, state)); err != nil {
			return nil, nil, fmt.Errorf("register mock tool %s: %w", tool.Name, err)
		}
	}
	if err := registry.Register(newDiscoverCommand(tools, state)); err != nil {
		return nil, nil, fmt.Errorf("register discover command: %w", err)
	}
	if err := registry.Register(newEvalHelpCommand(tools, stockHelp, state)); err != nil {
		return nil, nil, fmt.Errorf("register help command: %w", err)
	}

	gb, err := gbash.New( //nolint:contextcheck // gbash.New does not accept a context.
		gbash.WithRegistry(registry),
		gbash.WithFileSystem(seedFiles(task.Files)),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create runtime: %w", err)
	}
	session, err := gb.NewSession(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("create session: %w", err)
	}
	return session, state, nil
}

type mockToolCommand struct {
	tool  MockToolDef
	state *scriptedExecutionState
}

func newMockToolCommand(tool MockToolDef, state *scriptedExecutionState) commands.Command {
	return &mockToolCommand{tool: tool, state: state}
}

func (c *mockToolCommand) Name() string {
	return c.tool.Name
}

func (c *mockToolCommand) Run(ctx context.Context, inv *commands.Invocation) error {
	exitCode := 0
	defer func() {
		c.state.record(c.tool.Name, ScriptedCommandKindTool, inv.Args, exitCode)
	}()

	params, err := parseFlagArgs(inv.Args, c.tool.Schema)
	if err != nil {
		exitCode = 2
		return commands.Exitf(inv, exitCode, "%s", err)
	}

	stdin, err := commands.ReadAllStdin(ctx, inv)
	if err != nil {
		if code, ok := commands.ExitCode(err); ok {
			exitCode = code
		} else {
			exitCode = 1
		}
		return err
	}

	output, err := c.tool.Mock.execute(mockCallInput{
		Params: params,
		Stdin:  string(stdin),
	})
	if err != nil {
		exitCode = 1
		return commands.Exitf(inv, exitCode, "%s", err)
	}
	_, writeErr := io.WriteString(inv.Stdout, output)
	if writeErr != nil {
		exitCode = 1
		return writeErr
	}
	return nil
}

type evalHelpCommand struct {
	tools      []MockToolDef
	toolByName map[string]MockToolDef
	fallback   commands.Command
	state      *scriptedExecutionState
}

func newEvalHelpCommand(tools []MockToolDef, fallback commands.Command, state *scriptedExecutionState) commands.Command {
	index := make(map[string]MockToolDef, len(tools))
	for _, tool := range tools {
		index[tool.Name] = tool
	}
	return &evalHelpCommand{
		tools:      tools,
		toolByName: index,
		fallback:   fallback,
		state:      state,
	}
}

func (c *evalHelpCommand) Name() string {
	return "help"
}

func (c *evalHelpCommand) Run(ctx context.Context, inv *commands.Invocation) error {
	exitCode := 0
	defer func() {
		c.state.record("help", ScriptedCommandKindHelp, inv.Args, exitCode)
	}()

	if len(inv.Args) == 0 || (len(inv.Args) == 1 && inv.Args[0] == "--list") {
		for _, tool := range c.tools {
			if _, err := fmt.Fprintf(inv.Stdout, "%-20s %s\n", tool.Name, tool.Description); err != nil {
				exitCode = 1
				return err
			}
		}
		return nil
	}

	jsonMode := false
	var topic string
	for _, arg := range inv.Args {
		if arg == "--json" {
			jsonMode = true
			continue
		}
		if !strings.HasPrefix(arg, "--") && topic == "" {
			topic = arg
		}
	}
	if topic == "" {
		exitCode = 1
		return commands.Exitf(inv, exitCode, "usage: help [--list] [<tool>] [--json]")
	}

	if tool, ok := c.toolByName[topic]; ok {
		if jsonMode {
			payload := map[string]any{
				"name":         tool.Name,
				"description":  tool.Description,
				"input_schema": tool.Schema,
			}
			_, err := io.WriteString(inv.Stdout, prettyJSON(payload)+"\n")
			if err != nil {
				exitCode = 1
			}
			return err
		}
		if _, err := fmt.Fprintf(inv.Stdout, "%s - %s\n", tool.Name, tool.Description); err != nil {
			exitCode = 1
			return err
		}
		if usage := usageFromSchema(tool.Schema); usage != "" {
			if _, err := fmt.Fprintf(inv.Stdout, "Usage: %s %s\n", tool.Name, usage); err != nil {
				exitCode = 1
				return err
			}
		}
		return nil
	}

	err := commands.RunCommand(ctx, c.fallback, inv)
	if err != nil {
		if code, ok := commands.ExitCode(err); ok {
			exitCode = code
		} else {
			exitCode = 1
		}
		return err
	}
	return nil
}

type discoverCommand struct {
	tools []MockToolDef
	state *scriptedExecutionState
}

func newDiscoverCommand(tools []MockToolDef, state *scriptedExecutionState) commands.Command {
	return &discoverCommand{tools: tools, state: state}
}

func (c *discoverCommand) Name() string {
	return "discover"
}

func (c *discoverCommand) Run(_ context.Context, inv *commands.Invocation) error {
	exitCode := 0
	defer func() {
		c.state.record("discover", ScriptedCommandKindDiscover, inv.Args, exitCode)
	}()

	if len(inv.Args) == 0 {
		exitCode = 1
		return commands.Exitf(inv, exitCode, "usage: discover --categories | --category <name> | --tag <tag> | --search <keyword> [--json]")
	}

	jsonMode := containsArg(inv.Args, "--json")
	if containsArg(inv.Args, "--categories") {
		counts := map[string]int{}
		for _, tool := range c.tools {
			if tool.Category != "" {
				counts[tool.Category]++
			}
		}
		keys := make([]string, 0, len(counts))
		for key := range counts {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if jsonMode {
			var payload []map[string]any
			for _, key := range keys {
				payload = append(payload, map[string]any{"category": key, "count": counts[key]})
			}
			_, err := io.WriteString(inv.Stdout, prettyJSON(payload)+"\n")
			if err != nil {
				exitCode = 1
			}
			return err
		}
		for _, key := range keys {
			label := "tools"
			if counts[key] == 1 {
				label = "tool"
			}
			if _, err := fmt.Fprintf(inv.Stdout, "%s (%d %s)\n", key, counts[key], label); err != nil {
				exitCode = 1
				return err
			}
		}
		return nil
	}

	filtered := c.tools
	if value, ok := argValue(inv.Args, "--category"); ok {
		filtered = filterTools(c.tools, func(tool MockToolDef) bool { return tool.Category == value })
	} else if value, ok := argValue(inv.Args, "--tag"); ok {
		filtered = filterTools(c.tools, func(tool MockToolDef) bool {
			return slices.Contains(tool.Tags, value)
		})
	} else if value, ok := argValue(inv.Args, "--search"); ok {
		needle := strings.ToLower(value)
		filtered = filterTools(c.tools, func(tool MockToolDef) bool {
			return strings.Contains(strings.ToLower(tool.Name), needle) || strings.Contains(strings.ToLower(tool.Description), needle)
		})
	}

	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Name < filtered[j].Name })
	if jsonMode {
		payload := make([]map[string]any, 0, len(filtered))
		for _, tool := range filtered {
			item := map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
			}
			if tool.Category != "" {
				item["category"] = tool.Category
			}
			if len(tool.Tags) > 0 {
				item["tags"] = append([]string(nil), tool.Tags...)
			}
			payload = append(payload, item)
		}
		_, err := io.WriteString(inv.Stdout, prettyJSON(payload)+"\n")
		if err != nil {
			exitCode = 1
		}
		return err
	}

	for _, tool := range filtered {
		if _, err := fmt.Fprintf(inv.Stdout, "%-20s %s\n", tool.Name, tool.Description); err != nil {
			exitCode = 1
			return err
		}
	}
	return nil
}

func parseFlagArgs(rawArgs []string, schema map[string]any) (map[string]any, error) {
	properties := schemaProperties(normalizeSchema(schema))
	result := make(map[string]any)
	for i := 0; i < len(rawArgs); {
		arg := rawArgs[i]
		flag := strings.TrimPrefix(arg, "--")
		if flag == arg {
			return nil, fmt.Errorf("expected --flag, got: %s", arg)
		}

		if key, rawValue, ok := strings.Cut(flag, "="); ok {
			result[key] = coerceFlagValue(rawValue, asObject(properties[key]))
			i++
			continue
		}

		key := flag
		prop := asObject(properties[key])
		isBoolean := asString(prop["type"]) == "boolean"
		if isBoolean {
			result[key] = true
			i++
			continue
		}
		if i+1 < len(rawArgs) && !strings.HasPrefix(rawArgs[i+1], "--") {
			result[key] = coerceFlagValue(rawArgs[i+1], prop)
			i += 2
			continue
		}
		result[key] = true
		i++
	}
	return result, nil
}

func coerceFlagValue(raw string, schema map[string]any) any {
	switch asString(schema["type"]) {
	case "integer":
		var value int64
		if err := json.Unmarshal([]byte(raw), &value); err == nil {
			return value
		}
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return parsed
		}
	case "number":
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil {
			return parsed
		}
	case "boolean":
		switch raw {
		case "true", "1", "yes":
			return true
		case "false", "0", "no":
			return false
		}
	}
	return raw
}

func containsArg(args []string, target string) bool {
	return slices.Contains(args, target)
}

func argValue(args []string, flag string) (string, bool) {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func filterTools(tools []MockToolDef, keep func(MockToolDef) bool) []MockToolDef {
	filtered := make([]MockToolDef, 0, len(tools))
	for _, tool := range tools {
		if keep(tool) {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

func scriptedToolDefinition(name string) toolDefinition {
	return toolDefinition{
		Name:        name,
		Description: "Run bash scripts that orchestrate registered tool commands.",
		InputSchema: cloneMap(evalBashTool().InputSchema()),
	}
}

func scriptedSystemPrompt(task ScriptingEvalTask) string {
	if task.System != "" {
		return task.System
	}

	descriptions := make([]string, 0, len(task.Tools))
	for _, tool := range task.Tools {
		if usage := usageFromSchema(tool.Schema); usage != "" {
			descriptions = append(descriptions, fmt.Sprintf("%s [%s]", tool.Name, usage))
		} else {
			descriptions = append(descriptions, tool.Name)
		}
	}
	sort.Strings(descriptions)
	return renderScriptedSystemPrompt(task, strings.Join(descriptions, ", "))
}
