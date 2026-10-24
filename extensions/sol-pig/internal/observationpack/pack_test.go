// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

package observationpack

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/savings"
	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/testhost"
)

func textBlock(text string) map[string]any { return map[string]any{"type": "text", "text": text} }

func toolResult(tool, callID string, text string) map[string]any {
	return map[string]any{"role": "toolResult", "toolCallId": callID, "toolName": tool, "content": []any{textBlock(text)}, "isError": false, "timestamp": 1}
}

func bigResult(tool, callID string, n int) (map[string]any, string) {
	text, _ := numberedText(n)
	return toolResult(tool, callID, text), text
}

func assistant() map[string]any {
	return map[string]any{"role": "assistant", "content": []any{textBlock("ok")}}
}

func user() map[string]any { return map[string]any{"role": "user", "content": "hi"} }

func textOf(msg any) string {
	blocks, _ := msg.(map[string]any)["content"].([]any)
	if len(blocks) != 1 {
		return fmt.Sprintf("<%d blocks>", len(blocks))
	}
	text, _ := blocks[0].(map[string]any)["text"].(string)
	return text
}

func newHost(t *testing.T, sessionDir, sessionID string) *testhost.Host {
	t.Helper()
	ext := sdk.New("sol-pig")
	Register(ext)
	return testhost.Start(t, ext, testhost.Options{Cwd: t.TempDir(), Mode: "tui", SessionDir: sessionDir, SessionID: sessionID})
}

func rootOf(sessionDir, sessionID string) string {
	return filepath.Join(sessionDir, "sol-pi", sessionID)
}

func ledgerEntries(t *testing.T, root string) []map[string]any {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, "observation-pack", "ledger.jsonl"))
	if err != nil {
		return nil
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("ledger line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

func TestRegistersToolAndContextHandler(t *testing.T) {
	h := newHost(t, t.TempDir(), "sess1")
	tools := h.Tools()
	if len(tools) != 1 || tools[0].Name != "obs_recall" || tools[0].Description != "Read a stored large tool result by observation id and byte offset." ||
		tools[0].PromptSnippet != "Recall a paged excerpt from a previously replaced large tool result" {
		t.Fatalf("tools = %+v", tools)
	}
	if len(h.Handlers("context")) != 1 || len(h.RegisteredHandlers()) != 1 {
		t.Fatalf("handlers = %v", h.RegisteredHandlers())
	}
}

func TestFullTwiceThenPlaceholder(t *testing.T) {
	sessionDir := t.TempDir()
	h := newHost(t, sessionDir, "sess1")
	msg, original := bigResult("bash", "call-1", 400)
	lines := strings.SplitAfter(original, "\n")

	held, _ := h.EmitContext([]any{user(), msg})
	if textOf(held[1]) != original {
		t.Fatal("first send changed the content")
	}
	held, _ = h.EmitContext([]any{user(), msg, assistant()})
	if textOf(held[1]) != original {
		t.Fatal("second send changed the content")
	}
	held, replaced := h.EmitContext([]any{user(), msg, assistant(), assistant()})
	placeholder := textOf(held[1])
	if replaced || !strings.HasPrefix(placeholder, "[large tool result replaced after its first 2 provider requests]\nid: obs_") ||
		!strings.Contains(placeholder, lines[0]) || !strings.Contains(placeholder, lines[len(lines)-2]) || !strings.HasSuffix(placeholder, "[20000 original bytes omitted]") {
		t.Fatalf("third send: replaced=%v content=%q", replaced, placeholder)
	}
	held, _ = h.EmitContext([]any{user(), msg, assistant(), assistant(), assistant()})
	if textOf(held[1]) != placeholder {
		t.Fatal("fourth send differs from the third")
	}

	id := regexp.MustCompile(`id: (obs_[a-f0-9]{24})`).FindStringSubmatch(placeholder)[1]
	root := rootOf(sessionDir, "sess1")
	if body, err := os.ReadFile(ObjectPath(root, id)); err != nil || string(body) != original {
		t.Fatalf("object: %v", err)
	}
	o, _ := NewObservation("bash", "call-1", []string{original})
	removed := o.Tokens - EstimateTokens(Placeholder(o))
	notify := h.Calls("ui.notify")
	if len(notify) != 1 || notify[0].Args["message"] != "⚡ SoL-PiG · Observation Pack\nMoney saved · "+savings.FormatCount(float64(removed), "context tokens avoided") {
		t.Fatalf("notify = %+v", notify)
	}
	entries := ledgerEntries(t, root)
	var events []string
	var requests, sendNumbers []float64
	for _, e := range entries {
		events = append(events, e["event"].(string))
		requests = append(requests, e["request"].(float64))
		if n, ok := e["sendNumber"].(float64); ok {
			sendNumbers = append(sendNumbers, n)
		}
	}
	if !reflect.DeepEqual(events, []string{"full", "full", "placeholder", "placeholder"}) || !reflect.DeepEqual(requests, []float64{1, 2, 3, 4}) || !reflect.DeepEqual(sendNumbers, []float64{3, 4}) {
		t.Fatalf("ledger = %v %v %v", events, requests, sendNumbers)
	}
}

func TestInPlaceEditKeepsSystemMessagePositions(t *testing.T) {
	h := newHost(t, t.TempDir(), "sess1")
	msg, original := bigResult("bash", "call-1", 400)
	system := func() map[string]any { return map[string]any{"role": "system", "content": "sys"} }
	held, replaced := h.EmitContext([]any{system(), user(), msg, assistant(), assistant(), system()})
	if replaced || len(held) != 6 || held[0].(map[string]any)["role"] != "system" || held[5].(map[string]any)["role"] != "system" {
		t.Fatalf("replaced=%v len=%d", replaced, len(held))
	}
	if textOf(held[2]) == original || !strings.HasPrefix(textOf(held[2]), "[large tool result replaced") {
		t.Fatal("result with two later assistants was not placeholdered")
	}
}

func TestRestartReconstructsSendCount(t *testing.T) {
	sessionDir := t.TempDir()
	msg, original := bigResult("bash", "call-1", 400)
	first := newHost(t, sessionDir, "sess1")
	if held, _ := first.EmitContext([]any{user(), msg, assistant()}); textOf(held[1]) != original {
		t.Fatal("one later assistant must still send in full")
	}
	second := newHost(t, sessionDir, "sess1")
	if held, _ := second.EmitContext([]any{user(), msg, assistant(), assistant()}); !strings.HasPrefix(textOf(held[1]), "[large tool result replaced") {
		t.Fatal("a restarted extension must reconstruct the send count from later assistants")
	}
}

func TestPerSessionIsolation(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	a, b := newHost(t, dirA, "sess-a"), newHost(t, dirB, "sess-b")
	msg, original := bigResult("bash", "call-1", 400)
	for range 3 {
		a.EmitContext([]any{user(), msg})
	}
	if held, _ := a.EmitContext([]any{user(), msg}); !strings.HasPrefix(textOf(held[1]), "[large tool result replaced") {
		t.Fatal("session a: fourth send not placeholdered")
	}
	if held, _ := b.EmitContext([]any{user(), msg}); textOf(held[1]) != original {
		t.Fatal("session b counted session a's sends")
	}
	o, _ := NewObservation("bash", "call-1", []string{original})
	for _, root := range []string{rootOf(dirA, "sess-a"), rootOf(dirB, "sess-b")} {
		if _, err := os.Stat(ObjectPath(root, o.ID)); err != nil {
			t.Fatalf("object missing under %s: %v", root, err)
		}
	}
}

func captureStderr(t *testing.T, run func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	run()
	os.Stderr = old
	w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestFailOpenOnHashMismatch(t *testing.T) {
	sessionDir := t.TempDir()
	msg, original := bigResult("bash", "call-1", 400)
	o, _ := NewObservation("bash", "call-1", []string{original})
	path := ObjectPath(rootOf(sessionDir, "sess1"), o.ID)
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, []byte(strings.Repeat("q", len(original))), 0o600)
	h := newHost(t, sessionDir, "sess1")
	stderr := captureStderr(t, func() {
		for range 3 {
			if held, _ := h.EmitContext([]any{user(), msg, assistant(), assistant()}); textOf(held[1]) != original {
				t.Fatal("content changed despite the archive mismatch")
			}
		}
	})
	if !strings.Contains(stderr, "[observationpack] fail-open for tool result:") || !strings.Contains(stderr, "hash mismatch") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestPassThrough(t *testing.T) {
	sessionDir := t.TempDir()
	h := newHost(t, sessionDir, "sess1")
	big, _ := numberedText(400)
	errResult := toolResult("bash", "c-err", big)
	errResult["isError"] = true
	mixed := toolResult("bash", "c-mixed", big)
	mixed["content"] = []any{textBlock(big), map[string]any{"type": "image", "data": "AAAA", "mimeType": "image/png"}}
	receipt := toolResult("bash", "c-receipt", "x\n"+ReceiptPrefixLine+"\n"+big)
	stringContent := toolResult("bash", "c-string", big)
	stringContent["content"] = big
	noText := toolResult("bash", "c-notext", big)
	noText["content"] = []any{map[string]any{"type": "text"}}
	input := []any{user(), errResult, mixed, receipt, stringContent, "junk", noText, assistant(), assistant()}
	held, replaced := h.EmitContext(input)
	if replaced || len(held) != len(input) {
		t.Fatalf("replaced=%v len=%d", replaced, len(held))
	}
	for i := range input {
		if !reflect.DeepEqual(jsonRoundTrip(t, held[i]), jsonRoundTrip(t, input[i])) {
			t.Fatalf("message %d changed: %v", i, held[i])
		}
	}
	if _, err := os.Stat(filepath.Join(rootOf(sessionDir, "sess1"), "observation-pack")); !os.IsNotExist(err) {
		t.Fatalf("observation-pack dir created for pass-through messages: %v", err)
	}
}

func jsonRoundTrip(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	json.Unmarshal(raw, &out)
	return out
}

func TestRecallTool(t *testing.T) {
	sessionDir := t.TempDir()
	h := newHost(t, sessionDir, "sess1")
	msg, original := bigResult("bash", "call-1", 400)
	held, _ := h.EmitContext([]any{user(), msg, assistant(), assistant()})
	id := regexp.MustCompile(`id: (obs_[a-f0-9]{24})`).FindStringSubmatch(textOf(held[1]))[1]

	header := regexp.MustCompile(`^\[obs_recall id=(obs_[a-f0-9]{24}) offset=(\d+) next_offset=(\d+) eof=(true|false)\]\n\[chunk_bytes=(\d+) chunk_lines=(\d+); use next_offset to continue\]\n`)
	var got strings.Builder
	offset := 0
	for {
		params := map[string]any{"id": id}
		if offset > 0 {
			params["offset"] = offset
		}
		res := h.CallTool("obs_recall", "c1", params)
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		text := res.Content[0].(map[string]any)["text"].(string)
		m := header.FindStringSubmatch(text)
		if m == nil || m[1] != id || m[2] != strconv.Itoa(offset) {
			t.Fatalf("header mismatch: %q", text[:min(len(text), 160)])
		}
		got.WriteString(text[len(m[0]):])
		details := res.Details.(map[string]any)
		for _, key := range []string{"id", "offset", "bytes", "lines", "nextOffset", "eof"} {
			if _, ok := details[key]; !ok {
				t.Fatalf("details missing %s: %v", key, details)
			}
		}
		next, _ := strconv.Atoi(m[3])
		if m[4] == "true" {
			break
		}
		offset = next
	}
	if got.String() != original {
		t.Fatal("paged recall does not reproduce the original")
	}
	var recalls int
	for _, e := range ledgerEntries(t, rootOf(sessionDir, "sess1")) {
		if e["event"] == "recall" {
			recalls++
		}
	}
	if recalls < 2 {
		t.Fatalf("recall ledger entries = %d", recalls)
	}
	if res := h.CallTool("obs_recall", "c2", map[string]any{"id": "nope"}); res.Err == nil || res.Err.Error() != "Unknown observation id: nope" {
		t.Fatalf("bad id: %v", res.Err)
	}
	missing := "obs_" + strings.Repeat("0", 24)
	if res := h.CallTool("obs_recall", "c3", map[string]any{"id": missing}); res.Err == nil || res.Err.Error() != "Unknown observation id: "+missing {
		t.Fatalf("missing id: %v", res.Err)
	}
	if res := h.CallTool("obs_recall", "c4", map[string]any{"id": id, "offset": len(original) + 1}); res.Err == nil || !strings.Contains(res.Err.Error(), "exceeds observation size") {
		t.Fatalf("offset past end: %v", res.Err)
	}
}

func TestRecallRenderers(t *testing.T) {
	got := recallCallLines(map[string]any{"id": "obs_000000000000000000000000", "offset": float64(0)})
	want := []string{"⚡ SoL-PiG · Observation Pack", "Money saved · full observation replay avoided", "Recall obs_000000000000000000000000 from byte 0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
	partial := recallResultLines(true, nil)
	if !reflect.DeepEqual(partial, []string{"⚡ SoL-PiG · Observation Pack", "Money saved · full observation replay avoided", "Recalling the requested slice..."}) {
		t.Fatalf("partial = %q", partial)
	}
	done := recallResultLines(false, map[string]any{"bytes": float64(12), "lines": float64(3)})
	if done[2] != "Recalled 12 bytes across 3 lines" {
		t.Fatalf("done = %q", done)
	}
}
