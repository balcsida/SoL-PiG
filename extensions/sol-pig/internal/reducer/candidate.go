// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/extensions/evidence-preserving-reducer/candidate.ts at 2195680 (NVIDIA, MIT).

package reducer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/actionfusion"
)

var fullOutputPattern = regexp.MustCompile(`Full output:\s*([^\]\r\n]+)`)

// Reducible is the log inside a tool result: a bash result, or the command
// output appended by a fused write/edit.
type Reducible struct {
	Command    string
	Body       string                     // exact body (inline or the full-output log), valid UTF-8
	ExitFailed *bool                      // nil = the result carries no signal
	Project    func(receipt string) []any // the replacement content blocks
}

// TempDir follows Node's os.tmpdir(): TMPDIR, TMP, TEMP, else /tmp, trailing separator trimmed.
func TempDir() string {
	dir := "/tmp"
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		if value := os.Getenv(name); value != "" {
			dir = value
			break
		}
	}
	if len(dir) > 1 && strings.HasSuffix(dir, string(filepath.Separator)) {
		dir = dir[:len(dir)-1]
	}
	return dir
}

// safeTempLogPath ports safePiBashTempPath.
func safeTempLogPath(path string) bool {
	if path == "" || !tempLogName.MatchString(filepath.Base(path)) {
		return false
	}
	status, err := os.Lstat(path)
	if err != nil || !status.Mode().IsRegular() {
		return false
	}
	candidate, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	root, err := filepath.EvalSymlinks(TempDir())
	if err != nil {
		return false
	}
	return filepath.Dir(candidate) == root
}

func toValidUTF8(s string) string { return strings.ToValidUTF8(s, "\uFFFD") }

// textContent ports textContent: text blocks joined with "\n".
func textContent(content []any) string {
	var texts []string
	for _, item := range content {
		if block, ok := item.(map[string]any); ok && block["type"] == "text" {
			if s, ok := block["text"].(string); ok {
				texts = append(texts, s)
			}
		}
	}
	return strings.Join(texts, "\n")
}

// inlineExitFailed ports inlineExitFailed; it must read the inline text, not the log file.
func inlineExitFailed(inline string) *bool {
	match := exitStatusLine.FindStringSubmatch(inline)
	if match == nil {
		return nil
	}
	code, _ := strconv.ParseFloat(match[1], 64) // Number(code) !== 0
	failed := code != 0
	return &failed
}

// structuredExitFailed ports structuredExitCode: structuredContent.exit_code != 0 when present.
func structuredExitFailed(event map[string]any) *bool {
	structured, _ := event["structuredContent"].(map[string]any)
	var code float64
	switch value := structured["exit_code"].(type) {
	case float64:
		code = value
	case int:
		code = float64(value)
	case json.Number:
		parsed, err := strconv.ParseFloat(value.String(), 64)
		if err != nil {
			return nil
		}
		code = parsed
	default:
		return nil
	}
	failed := code != 0
	return &failed
}

// exactBodyFromInline prefers the untruncated log pi wrote for a large bash result.
func exactBodyFromInline(inline string, details any) string {
	var candidate string
	if record, ok := details.(map[string]any); ok {
		candidate, _ = record["fullOutputPath"].(string)
	}
	if candidate == "" {
		if match := fullOutputPattern.FindStringSubmatch(inline); match != nil {
			candidate = strings.TrimSpace(match[1])
		}
	}
	if candidate == "" || !safeTempLogPath(candidate) {
		return inline
	}
	data, err := os.ReadFile(candidate)
	if err != nil {
		return inline
	}
	return toValidUTF8(string(data))
}

func firstIndex(left, right int) int {
	switch {
	case left < 0:
		return right
	case right < 0:
		return left
	}
	return min(left, right)
}

// ReducibleToolResult ports reducibleToolResult over the tool_result event data.
func ReducibleToolResult(event map[string]any) (Reducible, bool) {
	toolName, _ := event["toolName"].(string)
	input, _ := event["input"].(map[string]any)
	content, _ := event["content"].([]any)
	if toolName == "bash" {
		command, _ := input["command"].(string)
		if command == "" {
			return Reducible{}, false
		}
		inline := toValidUTF8(textContent(content))
		exitFailed := structuredExitFailed(event)
		if exitFailed == nil {
			exitFailed = inlineExitFailed(inline)
		}
		return Reducible{
			Command:    command,
			Body:       exactBodyFromInline(inline, event["details"]),
			ExitFailed: exitFailed,
			Project: func(receipt string) []any {
				return []any{map[string]any{"type": "text", "text": receipt}}
			},
		}, true
	}
	if toolName != "write" && toolName != "edit" {
		return Reducible{}, false
	}
	thenRun, _ := input["then_run"].(map[string]any)
	command, _ := thenRun["command"].(string)
	if command == "" {
		return Reducible{}, false
	}
	for index, item := range content {
		block, ok := item.(map[string]any)
		if !ok || block["type"] != "text" {
			continue
		}
		blockText, _ := block["text"].(string)
		// The first marker is the one action-fusion wrote; the command's own output may quote either later.
		markerIndex := firstIndex(strings.Index(blockText, actionfusion.ThenRunSucceeded), strings.Index(blockText, actionfusion.ThenRunFailed))
		if markerIndex < 0 {
			continue
		}
		marker := actionfusion.ThenRunSucceeded
		if strings.HasPrefix(blockText[markerIndex:], actionfusion.ThenRunFailed) {
			marker = actionfusion.ThenRunFailed
		}
		suffixStart := markerIndex + len(marker)
		suffix := blockText[suffixStart:]
		separator := leadingNewlines(suffix)
		if separator == "" {
			separator = "\n"
		}
		inline := suffix
		if separator != "\n" || strings.HasPrefix(suffix, "\n") {
			inline = suffix[len(separator):]
		}
		inline = toValidUTF8(inline)
		// A succeeded marker still yields to pi's status line: conservative, a misreported failure must not get a success receipt.
		failed := marker == actionfusion.ThenRunFailed
		if trailer := inlineExitFailed(inline); trailer != nil && *trailer {
			failed = true
		}
		prefix := blockText[:suffixStart] + separator
		return Reducible{
			Command:    command,
			Body:       exactBodyFromInline(inline, event["details"]),
			ExitFailed: &failed,
			Project: func(receipt string) []any {
				projected := make([]any, len(content))
				copy(projected, content)
				replaced := map[string]any{}
				for k, v := range block {
					replaced[k] = v
				}
				replaced["text"] = prefix + receipt
				projected[index] = replaced
				return projected
			},
		}, true
	}
	return Reducible{}, false
}

// leadingNewlines is the leading (\r?\n)+ run of s, or "".
func leadingNewlines(s string) string {
	end := 0
	for end < len(s) {
		switch {
		case s[end] == '\n':
			end++
		case strings.HasPrefix(s[end:], "\r\n"):
			end += 2
		default:
			return s[:end]
		}
	}
	return s[:end]
}
