// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT
//
// Ported from PiG internal/codingagent/tools/edit.go at v0.5.3 (Hewlett Packard Enterprise Development LP, MIT; derived from Pi, Mario Zechner, MIT).

package actionfusion

import "encoding/json"

// PrepareEditArguments is the built-in edit's prepareArguments: edits as a JSON string or a single object, and the legacy top-level
// {oldText, newText}, become edits:[…]. Other members, then_run included, and unusable shapes are left for schema validation.
func PrepareEditArguments(params map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(params)+1)
	for k, v := range params {
		out[k] = v
	}
	switch edits := out["edits"].(type) {
	case string:
		var inner any
		if json.Unmarshal([]byte(edits), &inner) == nil {
			if _, isArray := inner.([]any); isArray {
				out["edits"] = inner
			} else if isSingleEdit(inner) {
				out["edits"] = []any{inner}
			}
		}
	case map[string]any:
		if isSingleEdit(edits) {
			out["edits"] = []any{edits}
		}
	}
	oldText, oldOK := out["oldText"].(string)
	newText, newOK := out["newText"].(string)
	if oldOK && newOK {
		edits, _ := out["edits"].([]any)
		delete(out, "oldText")
		delete(out, "newText")
		out["edits"] = append(edits, map[string]any{"oldText": oldText, "newText": newText})
	}
	return out, nil
}

func isSingleEdit(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, oldOK := m["oldText"].(string)
	_, newOK := m["newText"].(string)
	return oldOK && newOK
}
