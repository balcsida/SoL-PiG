// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

package testhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// BuiltinCall is one builtinTool.execute the extension made.
type BuiltinCall struct {
	Name, ToolCallID string
	Params           map[string]any
	Options          map[string]any
}

// BuiltinCalls returns every builtinTool.execute, in order.
func (h *Host) BuiltinCalls() []BuiltinCall {
	var out []BuiltinCall
	for _, c := range h.Calls("builtinTool.execute") {
		call := BuiltinCall{Params: map[string]any{}}
		call.Name, _ = c.Args["name"].(string)
		call.ToolCallID, _ = c.Args["toolCallId"].(string)
		if p, ok := c.Args["params"].(map[string]any); ok {
			call.Params = p
		}
		call.Options, _ = c.Args["options"].(map[string]any)
		out = append(out, call)
	}
	return out
}

const (
	editDescription = "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, non-overlapping region of the original file. If two changes affect the same block or nearby lines, merge them into one edit instead of emitting overlapping edits. Do not include large unchanged regions just to connect distant changes."
	bashSentence    = "Execute a bash command in the current working directory."
	bashDescription = bashSentence + " Returns stdout and stderr. Output is truncated to last 2000 lines or 50KB (whichever is hit first). If truncated, full output is saved to a temp file. Optionally provide a timeout in seconds."

	writeSchema = `{"type":"object","required":["path","content"],"properties":{
		"path":{"type":"string","description":"Path to the file to write (relative or absolute)"},
		"content":{"type":"string","description":"Content to write to the file"}}}`
	editSchema = `{"type":"object","required":["path","edits"],"properties":{
		"path":{"type":"string","description":"Path to the file to edit (relative or absolute)"},
		"edits":{"type":"array","items":{"type":"object","required":["oldText","newText"],"properties":{
			"oldText":{"type":"string","description":"Exact text for one targeted replacement. It must be unique in the original file and must not overlap with any other edits[].oldText in the same call."},
			"newText":{"type":"string","description":"Replacement text for this targeted edit."}}},"description":"One or more targeted replacements. Each edit is matched against the original file, not incrementally. Do not include overlapping or nested edits. If two changes touch the same block or nearby lines, merge them into one edit instead."}}}`
	bashSchema = `{"type":"object","required":["command"],"properties":{
		"command":{"type":"string","description":"Shell command to execute"},
		"timeout":{"type":"number","description":"Timeout in seconds (optional, no default timeout)"}}}`
	bashOutputSchema = `{"type":"object","required":["output","truncated","exit_code","wall_time_seconds"],"properties":{
		"output":{"type":"string","description":"Combined stdout and stderr, possibly truncated"},
		"truncated":{"type":"boolean"},
		"full_output_path":{"type":"string","description":"Full output, when truncated"},
		"exit_code":{"type":"number"},
		"wall_time_seconds":{"type":"number"}}}`
)

func (h *Host) builtinDefinition(args map[string]any) (any, error) {
	name, _ := args["name"].(string)
	def := map[string]any{
		"name": name, "label": name, "executionMode": "", "renderShell": "",
		"constrainedSampling": map[string]any{"type": "json_schema", "strict": "prefer"},
		"outputSchema":        nil,
	}
	var parameters string
	switch name {
	case "write":
		def["description"] = "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories."
		def["promptSnippet"] = "Write content to a file."
		def["promptGuidelines"] = []string{"Use write only for new files or complete rewrites."}
		parameters = writeSchema
	case "edit":
		def["description"] = editDescription
		def["promptSnippet"] = "Edit a single file using exact text replacement."
		def["promptGuidelines"] = []string{
			"Use edit for precise changes (edits[].oldText must match exactly)",
			"When changing multiple separate locations in one file, use one edit call with multiple entries in edits[] instead of multiple edit calls",
			"Each edits[].oldText is matched against the original file, not after earlier edits are applied. Do not emit overlapping or nested edits. Merge nearby changes into one edit.",
			"Keep edits[].oldText as small as possible while still being unique in the file. Do not pad with large unchanged regions.",
		}
		def["renderShell"] = "self"
		parameters = editSchema
	case "bash":
		def["description"] = bashDescription
		def["promptSnippet"] = bashSentence
		def["promptGuidelines"] = []string{}
		def["outputSchema"] = json.RawMessage(bashOutputSchema)
		parameters = bashSchema
	default:
		return nil, fmt.Errorf("unhandled built-in tool %q", name)
	}
	def["parameters"] = json.RawMessage(parameters)
	return def, nil
}

func (h *Host) builtinExecute(args map[string]any) (any, error) {
	name, _ := args["name"].(string)
	cwd, _ := args["cwd"].(string)
	params, _ := args["params"].(map[string]any)
	options, _ := args["options"].(map[string]any)
	switch name {
	case "write":
		return localWrite(cwd, params)
	case "edit":
		return localEdit(cwd, params)
	case "bash":
		return localBash(cwd, params, options)
	}
	return nil, fmt.Errorf("unhandled built-in tool %q", name)
}

func textResult(isError bool, text string) map[string]any {
	res := map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
	if isError {
		res["isError"] = true
	}
	return res
}

func resolve(cwd, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(cwd, path)
}

func localWrite(cwd string, params map[string]any) (any, error) {
	path, _ := params["path"].(string)
	content, _ := params["content"].(string)
	full := resolve(cwd, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return nil, err
	}
	return textResult(false, "Successfully wrote to "+path), nil
}

func localEdit(cwd string, params map[string]any) (any, error) {
	path, _ := params["path"].(string)
	full := resolve(cwd, path)
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	original := string(data)
	edits, _ := params["edits"].([]any)
	type replacement struct{ oldText, newText string }
	var replacements []replacement
	for _, e := range edits {
		entry, _ := e.(map[string]any)
		oldText, _ := entry["oldText"].(string)
		newText, _ := entry["newText"].(string)
		if oldText == "" || strings.Count(original, oldText) != 1 {
			return textResult(true, "Could not edit file: "+path+". Error: oldText not found or not unique."), nil
		}
		replacements = append(replacements, replacement{oldText, newText})
	}
	out := original
	for _, r := range replacements {
		out = strings.Replace(out, r.oldText, r.newText, 1)
	}
	if err := os.WriteFile(full, []byte(out), 0o644); err != nil {
		return nil, err
	}
	res := textResult(false, fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(replacements), path))
	res["details"] = map[string]any{"diff": "", "patch": "", "firstChangedLine": 1}
	return res, nil
}

func localBash(cwd string, params, options map[string]any) (any, error) {
	command, _ := params["command"].(string)
	shell, _ := options["shellPath"].(string)
	if shell == "" {
		shell = "/bin/bash"
	}
	if prefix, _ := options["commandPrefix"].(string); prefix != "" {
		command = prefix + "\n" + command
	}
	ctx := context.Background()
	timeout, hasTimeout := params["timeout"].(float64)
	if hasTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout*float64(time.Second)))
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, shell, "-c", command)
	cmd.Dir = cwd
	cmd.WaitDelay = 100 * time.Millisecond // a grandchild holding the pipe open must not outlast the timeout
	out, err := cmd.CombinedOutput()
	if hasTimeout && ctx.Err() != nil {
		return nil, fmt.Errorf("Command timed out after %v seconds", timeout)
	}
	text, code := string(out), 0
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case err != nil:
		return nil, err
	}
	if text == "" {
		text = "(no output)"
	}
	if code != 0 {
		text += fmt.Sprintf("\n\nCommand exited with code %d", code)
	}
	res := textResult(code != 0, text)
	res["structuredContent"] = map[string]any{"exit_code": code}
	return res, nil
}
