// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

package occ

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func userMessage(text string, timestamp float64) map[string]any {
	return map[string]any{"role": "user", "content": text, "timestamp": timestamp}
}

func assistantMessage(text string) map[string]any {
	return map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}}
}

func toolResultMessage(text string) map[string]any {
	return map[string]any{"role": "toolResult", "toolCallId": "large", "toolName": "bash", "timestamp": 2.0,
		"content": []any{map[string]any{"type": "text", "text": text}}}
}

func messageEntry(id string, message map[string]any) Projected {
	return Projected{SourceEntry: map[string]any{"type": "message", "id": id, "message": message}, Messages: []map[string]any{message}}
}

// history is user, tool result of ~52k tokens, user, assistant of 20k tokens: the cut keeps only the last entry.
func history() []Projected {
	return []Projected{
		messageEntry("e0", userMessage("Inspect the diagnostic output.", 1)),
		messageEntry("e1", toolResultMessage(strings.Repeat("diagnostic line\n", 13_000))),
		messageEntry("e2", userMessage("Continue with the next step.", 3)),
		messageEntry("e3", assistantMessage(strings.Repeat("r", 80_000))),
	}
}

func messages(projected []Projected) []map[string]any {
	var out []map[string]any
	for _, entry := range projected {
		out = append(out, entry.Messages...)
	}
	return out
}

func TestMessageKey(t *testing.T) {
	tool := toolResultMessage("a")
	replaced := toolResultMessage("a placeholder")
	if got, want := MessageKey(tool), `tool:["large","bash",2]`; got != want {
		t.Fatalf("tool key = %s, want %s", got, want)
	}
	if MessageKey(tool) != MessageKey(replaced) {
		t.Fatal("tool key must ignore content")
	}
	if got, want := MessageKey(map[string]any{"role": "user", "content": "x", "timestamp": 1.0}), `{"content":"x","role":"user","timestamp":1}`; got != want {
		t.Fatalf("plain key = %s, want %s", got, want)
	}
}

func TestCutEntriesShapes(t *testing.T) {
	summary := map[string]any{"role": "compactionSummary", "summary": "memo"}
	compaction := Projected{
		SourceEntry: map[string]any{"type": "compaction", "id": "c", "summary": "memo", "systemMessage": map[string]any{"role": "system"}},
		Messages:    []map[string]any{summary},
	}
	custom := Projected{SourceEntry: map[string]any{"type": "label", "id": "l", "data": 1.0}}
	message := userMessage("hi", 1)
	got := CutEntries([]Projected{compaction, messageEntry("m", message), custom})
	if got[0]["summary"] != "" {
		t.Fatalf("compaction summary = %v", got[0]["summary"])
	}
	if _, ok := got[0]["systemMessage"]; ok {
		t.Fatal("compaction keeps systemMessage")
	}
	if got[0]["type"] != "compaction" || got[0]["id"] != "c" {
		t.Fatalf("compaction = %v", got[0])
	}
	if got[1]["type"] != "message" || !reflect.DeepEqual(got[1]["message"], message) {
		t.Fatalf("message entry = %v", got[1])
	}
	if got[2]["type"] != "custom" || got[2]["customType"] != "sol-pi-projection-empty" || got[2]["id"] != "l" {
		t.Fatalf("empty entry = %v", got[2])
	}
	if _, ok := got[2]["data"]; ok {
		t.Fatal("stub keeps data")
	}
	if compaction.SourceEntry["summary"] != "memo" {
		t.Fatal("source entry was mutated")
	}
}

func TestEstimateRawAndNeverFallsBackWhenObservedLacksEntry(t *testing.T) {
	projected := history()
	raw := EstimateProjectedCompactionTokens(projected, 20_000, nil)
	if raw <= 50_000 {
		t.Fatalf("raw = %d", raw)
	}
	if got := EstimateProjectedCompactionTokens(projected, 20_000, messages(projected)); got != raw {
		t.Fatalf("identity observed = %d, want %d", got, raw)
	}
	dropped := []map[string]any{projected[0].Messages[0], projected[2].Messages[0], projected[3].Messages[0]}
	got := EstimateProjectedCompactionTokens(projected, 20_000, dropped)
	if got <= 0 || got >= DefaultNativeSummaryTokenEstimate {
		t.Fatalf("dropped = %d, want smaller than the summary", got)
	}
	if got := EstimateProjectedCompactionTokens(projected, 20_000, []map[string]any{}); got != 0 {
		t.Fatalf("empty observed = %d, want 0", got)
	}
}

func TestEstimateZeroWhenNothingRemovable(t *testing.T) {
	projected := history()[3:]
	if got := EstimateProjectedCompactionTokens(projected, 20_000, nil); got != 0 {
		t.Fatalf("got %d, want 0", got)
	}
}

func TestProjectedEntryTokensMatchesByIdentityAfterReorderAndInjection(t *testing.T) {
	projected := history()
	placeholder := toolResultMessage("short placeholder")
	observed := []map[string]any{assistantMessage("extension-injected content"), projected[3].Messages[0], placeholder, projected[2].Messages[0]}
	sizes := ProjectedEntryTokens(projected, observed)
	if sizes["e1"] != sdk.EstimateTokens(placeholder) {
		t.Fatalf("e1 = %d", sizes["e1"])
	}
	if _, ok := sizes["e0"]; ok {
		t.Fatal("e0 was not observed")
	}
	ambiguous := ProjectedEntryTokens(projected, append(observed, placeholder))
	if _, ok := ambiguous["e1"]; ok {
		t.Fatal("duplicate observed tool key must be ignored")
	}
}

func TestProjectedEntryTokensIgnoresDuplicateSourceToolKeys(t *testing.T) {
	first, second := toolResultMessage("one"), toolResultMessage("two")
	projected := []Projected{messageEntry("a", first), messageEntry("b", second)}
	if sizes := ProjectedEntryTokens(projected, []map[string]any{first, second}); len(sizes) != 0 {
		t.Fatalf("sizes = %v", sizes)
	}
}

func TestProjectedEntryTokensAfterRepeatedCompactions(t *testing.T) {
	projected := history()
	compaction := func(id, summary string) Projected {
		return Projected{SourceEntry: map[string]any{"type": "compaction", "id": id, "summary": summary},
			Messages: []map[string]any{{"role": "compactionSummary", "summary": summary}}}
	}
	// The older checkpoint yields no messages; the latest one leads the projection.
	older := Projected{SourceEntry: map[string]any{"type": "compaction", "id": "c1", "summary": "old"}}
	chain := append([]Projected{compaction("c2", "newer memo")}, projected[1:]...)
	chain = append(chain[:1], append([]Projected{older}, chain[1:]...)...)
	placeholder := toolResultMessage("short")
	observed := []map[string]any{{"role": "compactionSummary", "summary": "newer memo"}, placeholder, projected[2].Messages[0], projected[3].Messages[0]}
	sizes := ProjectedEntryTokens(chain, observed)
	if sizes["c2"] != 3 || sizes["e1"] != sdk.EstimateTokens(placeholder) {
		t.Fatalf("sizes = %v", sizes)
	}
	if _, ok := sizes["c1"]; ok {
		t.Fatal("older checkpoint has no messages")
	}
	// start is after c2; prefix is e1 (tool result), c1, e2; plus c2's summary tokens.
	want := sizes["e1"] + sizes["e2"] + sizes["c2"]
	if got := EstimateProjectedCompactionTokens(chain, 20_000, observed); got != want {
		t.Fatalf("estimate = %d, want %d", got, want)
	}
}

func TestSystemCheckpointIsNeverCounted(t *testing.T) {
	system := map[string]any{"role": "system", "content": strings.Repeat("s", 80_000), "timestamp": 0.0}
	summary := map[string]any{"role": "compactionSummary", "summary": "memo"}
	checkpoint := Projected{
		SourceEntry: map[string]any{"type": "compaction", "id": "checkpoint", "summary": "memo", "systemMessage": system},
		Messages:    []map[string]any{system, summary},
	}
	projected := []Projected{checkpoint, messageEntry("kept", assistantMessage("kept answer")),
		messageEntry("q", userMessage("new question", 3)), messageEntry("big", assistantMessage(strings.Repeat("x", 80_000)))}
	observed := messages(projected)
	if sizes := ProjectedEntryTokens(projected, observed); sizes["checkpoint"] != sdk.EstimateTokens(summary) {
		t.Fatalf("checkpoint = %d", sizes["checkpoint"])
	}
	want := sdk.EstimateTokens(summary) + sdk.EstimateTokens(projected[1].Messages[0]) + sdk.EstimateTokens(projected[2].Messages[0])
	if got := EstimateProjectedCompactionTokens(projected, 20_000, nil); got != want {
		t.Fatalf("raw = %d, want %d", got, want)
	}
	if got := EstimateProjectedCompactionTokens(projected, 20_000, observed); got != want {
		t.Fatalf("observed = %d, want %d", got, want)
	}
}
