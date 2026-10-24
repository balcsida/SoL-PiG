// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func parts(text string) []any { return []any{map[string]any{"type": "text", "text": text}} }

func TestClassifyAndSummarize(t *testing.T) {
	r := request{Messages: []map[string]any{{"role": "user", "content": parts("hi")}}}
	if got := messageText(r.Messages[0]); got != "hi" {
		t.Fatalf("messageText(parts) = %q", got)
	}
	if classify(r) != "auxiliary" {
		t.Fatal("no tools must classify as auxiliary")
	}
	r.Tools = []any{map[string]any{}}
	if classify(r) != "agent" {
		t.Fatal("tools must classify as agent")
	}
	r.Messages = append(r.Messages,
		map[string]any{"role": "tool", "content": parts("abc")},
		map[string]any{"role": "tool", "content": strings.Repeat("x", 12000)},
	)
	body := []byte(`{"x":"[large tool result replaced ... [large tool result replaced"}`)
	s := summarize(1, "agent", body, r)
	if s.ToolResults != 2 || !reflect.DeepEqual(s.ToolResultBytes, []int{3, 12000}) || s.Placeholders != 2 || s.Messages != 3 || s.Bytes != len(body) {
		t.Fatalf("summary = %+v", s)
	}
	raw, _ := json.Marshal(s)
	if !strings.HasPrefix(string(raw), `{"request":1,"kind":"agent","bytes":`) {
		t.Fatalf("summary field order: %s", raw)
	}
}

func TestScenarioProgressionAndDynamicRecall(t *testing.T) {
	s := &server{scenario: scenarios["observation-pack"]}
	for i, want := range []string{"yes 'abcdefghijklmnopqrstuvwxyz 0123456789 padding padding' | head -n 500", "echo second-step", "echo third-step"} {
		got := s.nextStep("{}")
		if got.kind != "tool" || got.name != "bash" || got.args["command"] != want {
			t.Fatalf("step %d = %+v", i+1, got)
		}
	}
	got := s.nextStep(`{"messages":[{"role":"tool","content":"id: obs_0123456789abcdef01234567\ntool: bash"}]}`)
	if got.kind != "tool" || got.name != "obs_recall" || got.args["id"] != "obs_0123456789abcdef01234567" || got.args["offset"] != 0 {
		t.Fatalf("recall step = %+v", got)
	}
	for i := range 3 {
		if got := s.nextStep("{}"); got.kind != "text" || got.text != "All done." {
			t.Fatalf("call %d past the end = %+v", i, got)
		}
	}
	fresh := &server{scenario: scenarios["observation-pack"], agentStep: 3}
	if got := fresh.nextStep("{}"); got.args["id"] != "missing" {
		t.Fatalf("recall without a placeholder = %+v", got)
	}
}

func TestActionFusionScenarioSteps(t *testing.T) {
	s := &server{scenario: scenarios["action-fusion"]}
	got := s.nextStep("{}")
	if got.kind != "tool" || got.name != "write" || got.args["path"] != "report.txt" || got.args["content"] != "hello from the fused write\n" ||
		!reflect.DeepEqual(got.args["then_run"], map[string]any{"command": "cat report.txt"}) {
		t.Fatalf("step 1 = %+v", got)
	}
	got = s.nextStep("{}")
	wantEdits := []map[string]any{{"oldText": "hello", "newText": "bye"}}
	if got.kind != "tool" || got.name != "edit" || got.args["path"] != "report.txt" || !reflect.DeepEqual(got.args["edits"], wantEdits) ||
		!reflect.DeepEqual(got.args["then_run"], map[string]any{"command": "exit 7"}) {
		t.Fatalf("step 2 = %+v", got)
	}
	if got := s.nextStep("{}"); got.kind != "text" || got.text != "All done." {
		t.Fatalf("step 3 = %+v", got)
	}
}

func post(t *testing.T, url, body string) (*http.Response, []string) {
	t.Helper()
	resp, err := http.Post(url+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			lines = append(lines, line)
		}
	}
	resp.Body.Close()
	return resp, lines
}

func chunk(t *testing.T, line string) map[string]any {
	t.Helper()
	var c map[string]any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &c); err != nil {
		t.Fatalf("chunk %q: %v", line, err)
	}
	return c
}

func choice(c map[string]any) map[string]any {
	return c["choices"].([]any)[0].(map[string]any)
}

func TestServeStreamsToolCallAndText(t *testing.T) {
	dir := t.TempDir()
	s := &server{scenario: scenarios["observation-pack"], logDir: dir}
	ts := httptest.NewServer(s)
	defer ts.Close()

	resp, lines := post(t, ts.URL, `{"model":"mock-1","stream":true,"messages":[{"role":"user","content":"go"}],"tools":[{}]}`)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status %d content-type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if len(lines) != 3 || lines[2] != "data: [DONE]" {
		t.Fatalf("lines = %q", lines)
	}
	first := chunk(t, lines[0])
	call := choice(first)["delta"].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	fn := call["function"].(map[string]any)
	var args map[string]any
	json.Unmarshal([]byte(fn["arguments"].(string)), &args)
	if first["object"] != "chat.completion.chunk" || first["model"] != "mock-1" || call["type"] != "function" || fn["name"] != "bash" || args["command"] != scenarios["observation-pack"][0].args["command"] {
		t.Fatalf("first chunk = %v", first)
	}
	second := chunk(t, lines[1])
	if choice(second)["finish_reason"] != "tool_calls" || second["usage"] == nil {
		t.Fatalf("second chunk = %v", second)
	}
	if _, err := os.Stat(filepath.Join(dir, "request-001-agent.json")); err != nil {
		t.Fatal(err)
	}
	logged, _ := os.ReadFile(filepath.Join(dir, "request-001-agent.json"))
	var decoded map[string]any
	if err := json.Unmarshal(logged, &decoded); err != nil || decoded["model"] != "mock-1" {
		t.Fatalf("logged request: %v %s", err, logged)
	}
	jsonl, _ := os.ReadFile(filepath.Join(dir, "requests.jsonl"))
	if !bytes.HasPrefix(jsonl, []byte(`{"request":1,"kind":"agent",`)) {
		t.Fatalf("requests.jsonl = %s", jsonl)
	}

	textServer := httptest.NewServer(&server{scenario: []step{textReply("All done.")}, logDir: t.TempDir()})
	defer textServer.Close()
	_, lines = post(t, textServer.URL, `{"model":"mock-1","stream":true,"messages":[],"tools":[{}]}`)
	if len(lines) != 3 || choice(chunk(t, lines[0]))["delta"].(map[string]any)["content"] != "All done." || choice(chunk(t, lines[1]))["finish_reason"] != "stop" {
		t.Fatalf("text lines = %q", lines)
	}
}

func TestServeRejectsNonStream(t *testing.T) {
	ts := httptest.NewServer(&server{scenario: scenarios["observation-pack"], logDir: t.TempDir()})
	defer ts.Close()
	resp, lines := post(t, ts.URL, `{"stream":false,"messages":[],"tools":[]}`)
	if resp.StatusCode != 400 || strings.Join(lines, "") != `{"error":"stream only"}` {
		t.Fatalf("non-stream: %d %q", resp.StatusCode, lines)
	}
	resp, lines = post(t, ts.URL, `nope`)
	if resp.StatusCode != 400 || strings.Join(lines, "") != `{"error":"invalid JSON"}` {
		t.Fatalf("invalid JSON: %d %q", resp.StatusCode, lines)
	}
	get, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	var list map[string]any
	json.NewDecoder(get.Body).Decode(&list)
	if get.StatusCode != 200 || list["data"].([]any)[0].(map[string]any)["id"] != "mock-1" {
		t.Fatalf("GET models: %d %v", get.StatusCode, list)
	}
}
