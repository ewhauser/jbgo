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

func parseEvalBashRequest(input map[string]any) (bashtool.Request, error) {
	return bashtool.ParseRequest(input)
}
