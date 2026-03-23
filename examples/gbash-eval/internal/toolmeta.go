package gbasheval

import (
	"fmt"
	"sort"
	"strings"
)

func normalizeSchema(schema map[string]any) map[string]any {
	if len(schema) == 0 {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}
	if _, ok := schema["type"]; !ok {
		schema = cloneMap(schema)
		if schema == nil {
			schema = map[string]any{}
		}
		schema["type"] = "object"
	}
	if _, ok := schema["properties"]; !ok {
		schema = cloneMap(schema)
		if schema == nil {
			schema = map[string]any{}
		}
		schema["properties"] = map[string]any{}
	}
	return schema
}

func schemaProperties(schema map[string]any) map[string]any {
	properties, _ := schema["properties"].(map[string]any)
	if properties == nil {
		return map[string]any{}
	}
	return properties
}

func usageFromSchema(schema map[string]any) string {
	props := schemaProperties(normalizeSchema(schema))
	if len(props) == 0 {
		return ""
	}
	keys := make([]string, 0, len(props))
	for key := range props {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		prop := asObject(props[key])
		typ := asString(prop["type"])
		if typ == "" {
			typ = "value"
		}
		parts = append(parts, fmt.Sprintf("--%s <%s>", key, typ))
	}
	return strings.Join(parts, " ")
}
