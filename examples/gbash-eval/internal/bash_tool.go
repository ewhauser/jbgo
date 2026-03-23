package gbasheval

import (
	"sync"

	"github.com/ewhauser/gbash/contrib/bashtool"
)

var evalBashToolOnce = sync.OnceValue(func() *bashtool.Tool {
	return bashtool.New(bashtool.Config{
		Profile: bashtool.CommandProfileExtras,
	})
})

func evalBashTool() *bashtool.Tool {
	return evalBashToolOnce()
}

func bashToolDefinition() toolDefinition {
	def := evalBashTool().ToolDefinition()
	return toolDefinition{
		Name:        def.Name,
		Description: def.Description,
		InputSchema: cloneMap(def.InputSchema),
	}
}

func extractCommands(input map[string]any) string {
	req, err := bashtool.ParseRequest(input)
	if err == nil {
		return req.ResolvedCommands()
	}
	if input == nil {
		return ""
	}
	if text := asString(input["commands"]); text != "" {
		return text
	}
	return asString(input["script"])
}
