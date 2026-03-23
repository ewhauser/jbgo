package gbasheval

import (
	"bytes"
	"encoding/json"
	"fmt"
)

func decodeJSONObject(data []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	return nil
}

func asObject(value any) map[string]any {
	obj, _ := value.(map[string]any)
	return obj
}

func asArray(value any) []any {
	array, _ := value.([]any)
	return array
}

func asString(value any) string {
	s, _ := value.(string)
	return s
}

func toJSONString(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func prettyJSON(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(data)
}
