// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

package reducer_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/actionfusion"
	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/reducer"
	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/testhost"
)

const sessionID = "sess-1"

var fauxModel = map[string]any{"id": "faux-1", "provider": "test-faux", "name": "Faux", "api": "openai-completions", "maxTokens": 4096.0, "contextWindow": 128000.0}

func diagnosticBody() string {
	var b strings.Builder
	b.WriteString("ERROR real target failed\n")
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&b, "line %d ... ok\n", i)
	}
	return b.String()
}

func bashEvent(command, text string, isError bool, extra map[string]any) map[string]any {
	event := map[string]any{
		"toolName":   "bash",
		"toolCallId": "call_1",
		"input":      map[string]any{"command": command},
		"content":    []any{map[string]any{"type": "text", "text": text}},
		"isError":    isError,
	}
	for k, v := range extra {
		event[k] = v
	}
	return event
}

func fusedEvent(tool string, isError bool, blocks ...string) map[string]any {
	content := make([]any, len(blocks))
	for i, b := range blocks {
		content[i] = map[string]any{"type": "text", "text": b}
	}
	return map[string]any{
		"toolName":   tool,
		"toolCallId": "call_1",
		"input":      map[string]any{"path": "a.txt", "then_run": map[string]any{"command": "make check"}},
		"content":    content,
		"isError":    isError,
		"details":    map[string]any{"patch": "@@ -1 +1 @@"},
	}
}

// receiptReply answers like a reducer model: a valid receipt quoting the first line with ERROR, else the first line.
func receiptReply(request map[string]any) map[string]any {
	messages, _ := request["messages"].([]any)
	content := messages[0].(map[string]any)["content"].([]any)
	prompt := content[0].(map[string]any)["text"].(string)
	var hash string
	isError := false
	for _, line := range strings.Split(prompt, "\n") {
		switch {
		case strings.HasPrefix(line, "source_sha256="):
			hash = strings.TrimPrefix(line, "source_sha256=")
		case line == "is_error=true":
			isError = true
		}
	}
	log := prompt[strings.Index(prompt, "<untrusted_log>\n")+len("<untrusted_log>\n") : strings.LastIndex(prompt, "\n</untrusted_log>")]
	quote := strings.SplitN(log, "\n", 2)[0]
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, "ERROR") {
			quote = line
			break
		}
	}
	status, kind := "success", "summary"
	if isError {
		status, kind = "failure", "failure"
	}
	return modelText(mustJSON(map[string]any{
		"schema": reducer.ReceiptSchema, "source_sha256": hash, "status": status, "uncertain": false,
		"evidence": []any{map[string]any{"kind": kind, "quote": quote}},
	}))
}

func modelText(text string) map[string]any {
	return map[string]any{
		"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}, "stopReason": "stop",
		"provider": "test-faux", "model": "faux-1",
		"usage": map[string]any{"input": 10.0, "output": 5.0, "cacheRead": 0.0, "cacheWrite": 0.0, "totalTokens": 15.0},
	}
}

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func start(t *testing.T, opts testhost.Options) *testhost.Host {
	t.Helper()
	ext := sdk.New("reducer-test")
	reducer.Register(ext, reducer.Options{Provider: "test-faux", Model: "faux-1"})
	if opts.Cwd == "" {
		opts.Cwd = t.TempDir()
	}
	if opts.SessionDir == "" && opts.SessionID == "" {
		opts.SessionDir, opts.SessionID = t.TempDir(), sessionID
	}
	if opts.Mode == "" {
		opts.Mode = "tui"
	}
	if opts.Models == nil {
		opts.Models = map[string]map[string]any{"test-faux/faux-1": fauxModel}
	}
	if opts.ModelReply == nil {
		opts.ModelReply = receiptReply
	}
	return testhost.Start(t, ext, opts)
}

func kinds(h *testhost.Host) []string {
	var out []string
	for _, e := range h.Entries() {
		out = append(out, e["data"].(map[string]any)["kind"].(string))
	}
	return out
}

func lastEntry(t *testing.T, h *testhost.Host) map[string]any {
	t.Helper()
	entries := h.Entries()
	if len(entries) == 0 {
		t.Fatal("no journal entries")
	}
	return entries[len(entries)-1]["data"].(map[string]any)
}

func expectUntouched(t *testing.T, h *testhost.Host, event map[string]any, wantKinds []string, reason string) {
	t.Helper()
	result, err := h.Emit("tool_result", event)
	if err != nil || result != nil {
		t.Fatalf("result = %v, err = %v; want nil, nil", result, err)
	}
	if got := strings.Join(kinds(h), ","); got != strings.Join(wantKinds, ",") {
		t.Fatalf("journal kinds = %q, want %q", got, strings.Join(wantKinds, ","))
	}
	if reason != "" {
		if got := lastEntry(t, h)["reason"]; got != reason {
			t.Fatalf("reason = %v, want %q", got, reason)
		}
	}
}

func receiptText(t *testing.T, result any) string {
	t.Helper()
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result = %v, want a replacement", result)
	}
	return m["content"].([]any)[0].(map[string]any)["text"].(string)
}

func TestRegistersOneToolResultHandler(t *testing.T) {
	h := start(t, testhost.Options{})
	registered := h.RegisteredHandlers()
	if len(registered) != 1 || len(registered["tool_result"]) != 1 || len(h.Tools()) != 0 {
		t.Fatalf("handlers = %v tools = %v", registered, h.Tools())
	}
}

func TestHappyPathReplacesWithReceipt(t *testing.T) {
	sessionDir := t.TempDir()
	h := start(t, testhost.Options{SessionDir: sessionDir, SessionID: sessionID})
	body := diagnosticBody()
	event := bashEvent("make check", body, true, map[string]any{"structuredContent": map[string]any{"exit_code": 2.0}})
	result, err := h.Emit("tool_result", event)
	if err != nil {
		t.Fatal(err)
	}
	text := receiptText(t, result)
	for _, want := range []string{"sol_pi_evidence_receipt_v1\nstatus=failure\n", "line=1", `quote="ERROR real target failed"`, "reducer_provider=test-faux", "authority="} {
		if !strings.Contains(text, want) {
			t.Errorf("receipt lacks %q:\n%s", want, text)
		}
	}
	if len(text) >= len(body) {
		t.Errorf("receipt %d bytes, body %d", len(text), len(body))
	}
	m := result.(map[string]any)
	if m["isError"] != true {
		t.Errorf("isError = %v", m["isError"])
	}
	applied := m["details"].(map[string]any)["evidencePreservingReducer"].(map[string]any)
	if applied["schema"] != "sol-pi-evidence-receipt/1" {
		t.Errorf("details = %v", applied)
	}
	archivePath := strings.TrimPrefix(strings.Split(strings.Split(text, "source_artifact=")[1], "\n")[0], "")
	data, err := os.ReadFile(archivePath)
	if err != nil || string(data) != body {
		t.Fatalf("archive %s: %v, equal=%v", archivePath, err, string(data) == body)
	}
	if info, _ := os.Stat(archivePath); info.Mode().Perm() != 0o600 {
		t.Errorf("archive mode = %v", info.Mode().Perm())
	}
	if !strings.HasPrefix(archivePath, filepath.Join(sessionDir, "sol-pi", sessionID)) {
		t.Errorf("archive outside the runtime root: %s", archivePath)
	}
	requests := h.ModelRequests()
	if len(requests) != 1 {
		t.Fatalf("model requests = %d", len(requests))
	}
	r := requests[0]
	if r["maxTokens"] != 2048.0 || r["cacheRetention"] != "none" || r["timeoutMs"] != 90000.0 || r["sessionId"] == "" || r["sessionId"] == nil {
		t.Errorf("options = %v", r)
	}
	if got := lastEntry(t, h)["runId"]; got != r["sessionId"] {
		t.Errorf("sessionId %v != runId %v", r["sessionId"], got)
	}
	if !strings.Contains(r["systemPrompt"].(string), "lossless test/build output reducer") {
		t.Errorf("systemPrompt = %v", r["systemPrompt"])
	}
	prompt := r["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(prompt, "<untrusted_log>") {
		t.Errorf("prompt = %.80s", prompt)
	}
	if got := strings.Join(kinds(h), ","); got != "candidate,provider_response,applied" {
		t.Errorf("kinds = %s", got)
	}
	for _, e := range h.Entries() {
		data := e["data"].(map[string]any)
		if e["customType"] != reducer.EventType || data["schema"] != reducer.EventSchema {
			t.Errorf("entry = %v", e)
		}
	}
	notify := h.Calls("ui.notify")
	if len(notify) != 1 {
		t.Fatalf("notify = %+v", notify)
	}
	message, _ := notify[0].Args["message"].(string)
	if !strings.HasPrefix(message, "⚡ SoL-PiG · Luna Delegating\nMoney saved · ") || !strings.HasSuffix(message, "removed from future prompts") {
		t.Errorf("notify = %+v", notify)
	}
}

func TestFusedResultKeepsConfirmationAndMarker(t *testing.T) {
	for _, tc := range []struct {
		marker  string
		isError bool
		status  string
	}{
		{actionfusion.ThenRunFailed, true, "failure"},
		{actionfusion.ThenRunSucceeded, false, "success"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			h := start(t, testhost.Options{})
			body := diagnosticBody()
			event := fusedEvent("write", tc.isError, "Successfully wrote 12 bytes", tc.marker+"\n"+body)
			result, err := h.Emit("tool_result", event)
			if err != nil {
				t.Fatal(err)
			}
			m := result.(map[string]any)
			content := m["content"].([]any)
			if len(content) != 2 || content[0].(map[string]any)["text"] != "Successfully wrote 12 bytes" {
				t.Fatalf("content = %v", content)
			}
			second := content[1].(map[string]any)["text"].(string)
			if !strings.HasPrefix(second, tc.marker+"\nsol_pi_evidence_receipt_v1\nstatus="+tc.status+"\n") || strings.Contains(second, "line 200 ... ok") {
				t.Fatalf("block 1 = %.200s", second)
			}
			details := m["details"].(map[string]any)
			if m["isError"] != tc.isError || details["patch"] != "@@ -1 +1 @@" || details["evidencePreservingReducer"] == nil {
				t.Fatalf("result = %v", m)
			}
		})
	}
}

func TestNestedResultUntouched(t *testing.T) {
	h := start(t, testhost.Options{})
	event := bashEvent("make check", diagnosticBody(), true, map[string]any{"parentToolCallId": "call_0"})
	expectUntouched(t, h, event, nil, "")
	if len(h.ModelRequests()) != 0 {
		t.Fatal("a nested result must not reach the model")
	}
}

func TestCodemodeNeverCandidate(t *testing.T) {
	h := start(t, testhost.Options{})
	event := bashEvent("make check", diagnosticBody(), true, nil)
	event["toolName"] = "codemode"
	expectUntouched(t, h, event, nil, "")
	if len(h.ModelRequests()) != 0 {
		t.Fatal("codemode must not reach the model")
	}
}

func TestExitStateMismatchCases(t *testing.T) {
	body := diagnosticBody()
	cases := map[string]map[string]any{
		"fused succeeded with exit trailer": fusedEvent("write", false, "ok", actionfusion.ThenRunSucceeded+"\n"+body+"\n\nCommand exited with code 7"),
		"fused failed marker, not error":    fusedEvent("write", false, "ok", actionfusion.ThenRunFailed+"\n"+body),
		"fused succeeded marker, error":     fusedEvent("write", true, "ok", actionfusion.ThenRunSucceeded+"\n"+body),
		"bash structured exit, not error":   bashEvent("make check", body, false, map[string]any{"structuredContent": map[string]any{"exit_code": 2.0}}),
		"bash inline exit, not error":       bashEvent("make check", body+"\n\nCommand exited with code 2", false, nil),
	}
	for name, event := range cases {
		t.Run(name, func(t *testing.T) {
			h := start(t, testhost.Options{})
			expectUntouched(t, h, event, []string{"fallback"}, "exit-state-mismatch")
			if len(h.ModelRequests()) != 0 {
				t.Fatal("a mismatch must not reach the model")
			}
		})
	}
}

func TestInlineTrailerOutranksFile(t *testing.T) {
	// The full-output log has no trailer; the mismatch is read from the inline text.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	logPath := filepath.Join(tmp, "pi-bash-abc123.log")
	if err := os.WriteFile(logPath, []byte(diagnosticBody()), 0o600); err != nil {
		t.Fatal(err)
	}
	h := start(t, testhost.Options{})
	event := bashEvent("make check", "tail\n\n[Showing lines 1-2 of 300. Full output: "+logPath+"]\n\nCommand exited with code 2", false, map[string]any{"details": map[string]any{"fullOutputPath": logPath}})
	expectUntouched(t, h, event, []string{"fallback"}, "exit-state-mismatch")
	if len(h.ModelRequests()) != 0 {
		t.Fatal("a mismatch must not reach the model")
	}
}

func TestStructuredExitOutranksTrailer(t *testing.T) {
	h := start(t, testhost.Options{})
	event := bashEvent("make check", diagnosticBody()+"\n\nCommand exited with code 3", false, map[string]any{"structuredContent": map[string]any{"exit_code": 0.0}})
	result, err := h.Emit("tool_result", event)
	if err != nil {
		t.Fatal(err)
	}
	if text := receiptText(t, result); !strings.Contains(text, "status=success\n") {
		t.Fatalf("receipt = %.120s", text)
	}
}

func TestFailsOpen(t *testing.T) {
	body := diagnosticBody()
	failing := func(isError bool) map[string]any { return bashEvent("make check", body, isError, nil) }
	t.Run("invented quote", func(t *testing.T) {
		h := start(t, testhost.Options{ModelReply: func(request map[string]any) map[string]any {
			good := receiptReply(request)
			var receipt map[string]any
			if err := json.Unmarshal([]byte(good["content"].([]any)[0].(map[string]any)["text"].(string)), &receipt); err != nil {
				t.Fatal(err)
			}
			receipt["evidence"] = []any{map[string]any{"kind": "failure", "quote": "ERROR invented"}}
			return modelText(mustJSON(receipt))
		}})
		expectUntouched(t, h, failing(true), []string{"candidate", "provider_response", "fallback"}, "unverifiable-quote")
	})
	t.Run("model error", func(t *testing.T) {
		h := start(t, testhost.Options{ModelReply: func(map[string]any) map[string]any {
			return map[string]any{"role": "assistant", "content": []any{}, "stopReason": "error", "errorMessage": "boom"}
		}})
		expectUntouched(t, h, failing(true), []string{"candidate", "provider_response", "fallback"}, "model-response-error")
		last := lastEntry(t, h)
		if last["stopReason"] != "error" || last["errorMessage"] != "boom" {
			t.Errorf("entry = %v", last)
		}
	})
	for name, mutate := range map[string]func(map[string]any){
		"tool use stop reason": func(m map[string]any) { m["stopReason"] = "toolUse" },
		"empty stop reason":    func(m map[string]any) { delete(m, "stopReason") },
	} {
		t.Run(name, func(t *testing.T) {
			h := start(t, testhost.Options{ModelReply: func(request map[string]any) map[string]any {
				reply := receiptReply(request)
				mutate(reply)
				return reply
			}})
			expectUntouched(t, h, failing(true), []string{"candidate", "provider_response", "fallback"}, "model-response-error")
			if got := lastEntry(t, h)["stopReason"]; (got == "toolUse") != (name == "tool use stop reason") {
				t.Errorf("stopReason = %v", got)
			}
		})
	}
	t.Run("model lookup error", func(t *testing.T) {
		h := start(t, testhost.Options{})
		h.Handle("getModel", func(map[string]any) (any, error) { return nil, fmt.Errorf("registry down") })
		expectUntouched(t, h, failing(true), []string{"candidate", "fallback"}, "model-call-exception")
	})
	t.Run("model unavailable", func(t *testing.T) {
		h := start(t, testhost.Options{Models: map[string]map[string]any{"other/model": fauxModel}})
		expectUntouched(t, h, failing(true), []string{"candidate", "fallback"}, "reducer-model-unavailable")
		if len(h.ModelRequests()) != 0 {
			t.Fatal("no model, no request")
		}
	})
	t.Run("small body", func(t *testing.T) {
		h := start(t, testhost.Options{})
		expectUntouched(t, h, bashEvent("make check", "ERROR short", true, nil), nil, "")
	})
	t.Run("non-diagnostic command", func(t *testing.T) {
		h := start(t, testhost.Options{})
		expectUntouched(t, h, bashEvent("rg symbol src", body, true, nil), nil, "")
	})
	t.Run("likely secret", func(t *testing.T) {
		h := start(t, testhost.Options{})
		expectUntouched(t, h, bashEvent("make check", body+"api_key=abcdef\n", true, nil), []string{"fallback"}, "likely-secret")
	})
	t.Run("over max chars", func(t *testing.T) {
		h := start(t, testhost.Options{})
		huge := "ERROR x\n" + strings.Repeat("a", reducer.MaxChars)
		expectUntouched(t, h, bashEvent("make check", huge, true, nil), []string{"fallback"}, "source-over-max-chars")
	})
	t.Run("receipt not smaller", func(t *testing.T) {
		// Twelve verified 330-byte lines outweigh a 4.3 KB log once the hashes are added.
		var lines []string
		for i := 0; i < 13; i++ {
			lines = append(lines, fmt.Sprintf("ERROR %02d %s", i, strings.Repeat("x", 320)))
		}
		long := strings.Join(lines, "\n")
		h := start(t, testhost.Options{ModelReply: func(request map[string]any) map[string]any {
			good := receiptReply(request)
			var receipt map[string]any
			if err := json.Unmarshal([]byte(good["content"].([]any)[0].(map[string]any)["text"].(string)), &receipt); err != nil {
				t.Fatal(err)
			}
			var evidence []any
			for _, line := range lines[:12] {
				evidence = append(evidence, map[string]any{"kind": "failure", "quote": line})
			}
			receipt["evidence"] = evidence
			return modelText(mustJSON(receipt))
		}})
		expectUntouched(t, h, bashEvent("make check", long, true, nil), []string{"candidate", "provider_response", "fallback"}, "receipt-not-smaller")
	})
}

func TestTimeoutFailsOpen(t *testing.T) {
	old := reducer.Timeout
	reducer.Timeout = 200 * time.Millisecond
	t.Cleanup(func() { reducer.Timeout = old })
	h := start(t, testhost.Options{ModelDelay: 2 * time.Second})
	began := time.Now()
	expectUntouched(t, h, bashEvent("make check", diagnosticBody(), true, nil), []string{"candidate", "fallback"}, "model-call-timeout")
	if took := time.Since(began); took > time.Second {
		t.Fatalf("handler took %v", took)
	}
}

func TestAbortedRunFailsOpen(t *testing.T) {
	h := start(t, testhost.Options{ModelDelay: 5 * time.Second})
	h.StartRun()
	time.Sleep(100 * time.Millisecond)
	time.AfterFunc(200*time.Millisecond, h.AbortRun)
	began := time.Now()
	expectUntouched(t, h, bashEvent("make check", diagnosticBody(), true, nil), []string{"candidate", "fallback"}, "model-call-timeout")
	if took := time.Since(began); took > 2*time.Second {
		t.Fatalf("handler took %v after the abort", took)
	}
}

func TestEmptySessionIDSilent(t *testing.T) {
	h := start(t, testhost.Options{SessionDir: t.TempDir(), SessionID: ""})
	expectUntouched(t, h, bashEvent("make check", diagnosticBody(), true, nil), nil, "")
	if len(h.ModelRequests()) != 0 {
		t.Fatal("an unsafe session id must not reach the model")
	}
}

func TestOnlyTempDirLogsAreRead(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	inside := filepath.Join(tmp, "pi-bash-abc.log")
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "pi-bash-evil.log")
	fileBody := "ERROR from the log file\n" + strings.Repeat("file line ok\n", 400)
	for _, p := range []string{inside, outside} {
		if err := os.WriteFile(p, []byte(fileBody), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	inline := "ERROR inline tail\n" + strings.Repeat("inline line ok\n", 400)
	promptOf := func(h *testhost.Host) string {
		return h.ModelRequests()[0]["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	}
	h := start(t, testhost.Options{})
	if _, err := h.Emit("tool_result", bashEvent("make check", inline, true, map[string]any{"details": map[string]any{"fullOutputPath": inside}})); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(promptOf(h), "ERROR from the log file") {
		t.Error("a pi-bash log in the temp dir must be read")
	}
	h = start(t, testhost.Options{})
	if _, err := h.Emit("tool_result", bashEvent("make check", inline, true, map[string]any{"details": map[string]any{"fullOutputPath": outside}})); err != nil {
		t.Fatal(err)
	}
	if p := promptOf(h); !strings.Contains(p, "ERROR inline tail") || strings.Contains(p, "from the log file") {
		t.Error("a log outside the temp dir must be ignored")
	}
}
