//nolint:gocritic // Prompt rendering stays simpler with value parameters in this internal example package.
package gbasheval

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed prompts/baseline_system_prompt.tmpl prompts/scripted_system_prompt.tmpl
var promptFS embed.FS

var (
	scriptedPromptTemplate = mustParsePromptTemplate("prompts/scripted_system_prompt.tmpl")
	baselinePromptTemplate = mustParsePromptTemplate("prompts/baseline_system_prompt.tmpl")
)

type scriptedPromptData struct {
	TaskID          string
	DiscoveryMode   bool
	CommandSummary  string
	ExtrasSummary   string
	ShortDescSuffix string
}

type baselinePromptData struct {
	Tools []MockToolDef
}

func baselineSystemPrompt(tools []MockToolDef) string {
	return renderPromptTemplate(baselinePromptTemplate, baselinePromptData{
		Tools: append([]MockToolDef(nil), tools...),
	})
}

func renderScriptedSystemPrompt(task ScriptingEvalTask, commandSummary string) string {
	return renderPromptTemplate(scriptedPromptTemplate, scriptedPromptData{
		TaskID:          task.ID,
		DiscoveryMode:   task.DiscoveryMode,
		CommandSummary:  commandSummary,
		ExtrasSummary:   "Standard shell commands plus stable contrib commands are available: awk, html-to-markdown, jq, sqlite3, yq.",
		ShortDescSuffix: "Scripted tool eval",
	})
}

func mustParsePromptTemplate(name string) *template.Template {
	data, err := promptFS.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("read embedded template %q: %v", name, err))
	}
	tmpl, err := template.New(name).Parse(string(data))
	if err != nil {
		panic(fmt.Sprintf("parse embedded template %q: %v", name, err))
	}
	return tmpl
}

func renderPromptTemplate(tmpl *template.Template, data any) string {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		panic(fmt.Sprintf("render embedded template %q: %v", tmpl.Name(), err))
	}
	return strings.TrimSpace(buf.String())
}
