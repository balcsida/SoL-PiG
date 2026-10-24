// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/extensions/online-context-compact/projection.ts at 2195680 (NVIDIA, MIT).

package occ

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	// DefaultKeepRecentTokens is the retained tail when neither settings nor options set one.
	DefaultKeepRecentTokens = 20000
	// DefaultNativeSummaryTokenEstimate is the assumed size of the summary native compaction writes.
	DefaultNativeSummaryTokenEstimate = 1000

	projectionEmptyType = "sol-pi-projection-empty"
	abortMarkerID       = "sol-pi-online-context-compact-abort-marker"
)

// Projected is one projected branch entry: the source entry and the messages it contributes (sdk.ProjectedSessionEntry's shape).
type Projected struct {
	SourceEntry map[string]any
	Messages    []map[string]any
}

// MessageKey identifies a message across ObservationPack rewrites: a tool result by its call identity, anything else by its JSON.
// json.Marshal sorts map keys and writes numbers the way Go does; both sides of a comparison come from the same decoded shapes, so equal messages give equal keys.
func MessageKey(m map[string]any) string {
	if m["role"] == "toolResult" {
		key, _ := json.Marshal([]any{m["toolCallId"], m["toolName"], m["timestamp"]})
		return "tool:" + string(key)
	}
	key, _ := json.Marshal(m)
	return string(key)
}

// ProjectedEntryTokens maps the last observed messages to source entry ids. A key counts only when the observed and source multiplicities match
// and, for tool results, it is unique; dropped, rewritten, injected or ambiguous messages give no entry, never a raw size. System messages are skipped.
func ProjectedEntryTokens(projected []Projected, observed []map[string]any) map[string]int {
	sources := map[string][]string{}
	var order []string
	for _, entry := range projected {
		id, _ := entry.SourceEntry["id"].(string)
		for _, message := range entry.Messages {
			if message["role"] == "system" {
				continue
			}
			key := MessageKey(message)
			if _, seen := sources[key]; !seen {
				order = append(order, key)
			}
			sources[key] = append(sources[key], id)
		}
	}
	visible := map[string][]int{}
	for _, message := range observed {
		key := MessageKey(message)
		visible[key] = append(visible[key], sdk.EstimateTokens(message))
	}
	tokens := map[string]int{}
	for _, key := range order {
		ids, sizes := sources[key], visible[key]
		if len(sizes) != len(ids) || len(sizes) == 0 || (strings.HasPrefix(key, "tool:") && len(ids) > 1) {
			continue
		}
		for i, id := range ids {
			tokens[id] += sizes[i]
		}
	}
	return tokens
}

// CutEntries materializes the entries sdk.FindCutPoint reads. A compaction keeps its type (which stops metadata-boundary movement) but loses its summary and system message.
func CutEntries(projected []Projected) []map[string]any {
	entries := make([]map[string]any, 0, len(projected))
	for _, entry := range projected {
		cut := maps.Clone(entry.SourceEntry)
		switch {
		case cut["type"] == "compaction":
			cut["summary"] = ""
			delete(cut, "systemMessage")
		case len(entry.Messages) > 0:
			cut["type"] = "message"
			cut["message"] = entry.Messages[0]
		default:
			cut["type"] = "custom"
			cut["customType"] = projectionEmptyType
			delete(cut, "data")
		}
		entries = append(entries, cut)
	}
	return entries
}

// EstimateProjectedCompactionTokens prices the prefix native compaction would remove from the projected branch, plus the previous summary.
// observed (nil = raw sizes) is the last extension-visible projection; an entry it lacks counts 0.
func EstimateProjectedCompactionTokens(projected []Projected, keepRecentTokens int, observed []map[string]any) int {
	path := CutEntries(projected)
	previous := slices.IndexFunc(projected, func(entry Projected) bool {
		return entry.SourceEntry["type"] == "compaction" && len(entry.Messages) > 0
	})
	start := previous + 1 // previous is -1 when there is none
	// The agent can append an empty aborted assistant after OCC stops the turn; it weighs nothing but makes the trailing cut available.
	var parentID any
	if len(path) > 0 {
		parentID = path[len(path)-1]["id"]
	}
	path = append(path, map[string]any{
		"type": "message", "id": abortMarkerID, "parentId": parentID, "timestamp": "1970-01-01T00:00:00.000Z",
		"message": map[string]any{"role": "assistant", "content": []any{}, "api": "sol-pi", "provider": "sol-pi", "model": "aborted",
			"usage": map[string]any{"input": 0.0, "output": 0.0, "cacheRead": 0.0, "cacheWrite": 0.0, "totalTokens": 0.0,
				"cost": map[string]any{"input": 0.0, "output": 0.0, "cacheRead": 0.0, "cacheWrite": 0.0, "total": 0.0}},
			"stopReason": "aborted", "timestamp": 0.0},
	})
	cut := sdk.FindCutPoint(path, start, len(path), keepRecentTokens).FirstKeptEntryIndex
	prefix := projected[start:cut]
	// System messages survive as the next checkpoint and are never summarized.
	removable := func(entry Projected) []map[string]any {
		if entry.SourceEntry["type"] == "compaction" {
			return nil
		}
		return slices.DeleteFunc(slices.Clone(entry.Messages), func(m map[string]any) bool { return m["role"] == "system" })
	}
	if !slices.ContainsFunc(prefix, func(entry Projected) bool { return len(removable(entry)) > 0 }) {
		return 0
	}
	var sizes map[string]int
	if observed != nil {
		sizes = ProjectedEntryTokens(projected, observed)
	}
	sum := func(messages []map[string]any) int {
		total := 0
		for _, m := range messages {
			total += sdk.EstimateTokens(m)
		}
		return total
	}
	total := 0
	for _, entry := range prefix {
		if sizes != nil {
			id, _ := entry.SourceEntry["id"].(string)
			total += sizes[id]
		} else {
			total += sum(removable(entry))
		}
	}
	if previous >= 0 {
		entry := projected[previous]
		if sizes != nil {
			id, _ := entry.SourceEntry["id"].(string)
			total += sizes[id]
		} else {
			total += sum(slices.DeleteFunc(slices.Clone(entry.Messages), func(m map[string]any) bool { return m["role"] != "compactionSummary" }))
		}
	}
	return total
}
