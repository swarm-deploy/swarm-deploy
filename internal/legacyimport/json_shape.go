package legacyimport

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// A valid JSON object with a misspelled wrapper must not silently import an
// empty repository. Unknown additional fields remain allowed for compatibility.
func validateJSONShape(name string, payload []byte) error {
	required := map[string]string{
		"alerts.state.json":          "alerts",
		"secrets.state.json":         "secrets",
		"recommendations.state.json": "list",
		"index.json":                 "chats",
	}
	key, wrapped := required[name]
	if !wrapped && name != "controller.state.json" {
		return nil
	}
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("%s must contain an object", name)
	}
	if !wrapped {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return fmt.Errorf("decode %s: %w", name, err)
	}
	if _, ok := fields[key]; !ok {
		return fmt.Errorf("%s is missing required field %q", name, key)
	}
	return nil
}
