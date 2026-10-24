// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

// Command mockprovider is a scripted OpenAI-compatible Chat Completions
// endpoint for zero-spend live checks of SoL-PiG inside a real PiG process.
// Every request body it receives is logged, so the log shows exactly what
// PiG sent after the extension's hooks ran. It binds loopback only.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type request struct {
	Model               string           `json:"model"`
	Stream              bool             `json:"stream"`
	Messages            []map[string]any `json:"messages"`
	Tools               []any            `json:"tools"`
	MaxTokens           int              `json:"max_tokens"`
	MaxCompletionTokens int              `json:"max_completion_tokens"`
}

// summary is one line of requests.jsonl; its field order is what verify.sh matches on.
type summary struct {
	Request         int    `json:"request"`
	Kind            string `json:"kind"`
	Bytes           int    `json:"bytes"`
	Messages        int    `json:"messages"`
	ToolResults     int    `json:"toolResults"`
	ToolResultBytes []int  `json:"toolResultBytes"`
	Placeholders    int    `json:"placeholders"`
	MaxTokens       int    `json:"maxTokens"`
}

func messageText(m map[string]any) string {
	switch content := m["content"].(type) {
	case string:
		return content
	case []any:
		var texts []string
		for _, part := range content {
			if p, ok := part.(map[string]any); ok {
				if text, ok := p["text"].(string); ok {
					texts = append(texts, text)
				}
			}
		}
		return strings.Join(texts, "\n")
	}
	return ""
}

func classify(r request) string {
	if len(r.Tools) > 0 {
		return "agent"
	}
	return "auxiliary"
}

func summarize(n int, kind string, body []byte, r request) summary {
	s := summary{Request: n, Kind: kind, Bytes: len(body), Messages: len(r.Messages), ToolResultBytes: []int{},
		Placeholders: bytes.Count(body, []byte("[large tool result replaced")), MaxTokens: r.MaxTokens}
	if s.MaxTokens == 0 {
		s.MaxTokens = r.MaxCompletionTokens
	}
	for _, m := range r.Messages {
		if m["role"] == "tool" {
			s.ToolResults++
			s.ToolResultBytes = append(s.ToolResultBytes, len(messageText(m)))
		}
	}
	return s
}

type server struct {
	scenario []step
	logDir   string

	mu        sync.Mutex
	requests  int
	agentStep int
}

// nextStep is the scenario step for the next agent request; past the end the last step repeats.
func (s *server) nextStep(serialized string) step {
	s.mu.Lock()
	next := s.scenario[min(s.agentStep, len(s.scenario)-1)]
	s.agentStep++
	s.mu.Unlock()
	if next.kind == "dynamic" {
		return next.build(serialized)
	}
	return next
}

func (s *server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodGet {
		writeJSON(w, 200, map[string]any{"object": "list", "data": []any{map[string]any{"id": "mock-1", "object": "model"}}})
		return
	}
	body, _ := io.ReadAll(req.Body)
	var r request
	if err := json.Unmarshal(body, &r); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid JSON"})
		return
	}
	if !r.Stream {
		writeJSON(w, 400, map[string]any{"error": "stream only"})
		return
	}
	s.mu.Lock()
	s.requests++
	n := s.requests
	s.mu.Unlock()
	kind := classify(r)
	s.log(n, kind, body, r)

	var action step
	if kind == "agent" {
		action = s.nextStep(string(body))
	} else {
		action = textReply("Scripted session")
	}
	s.stream(w, n, r, action)
}

func (s *server) log(n int, kind string, body []byte, r request) {
	if s.logDir == "" {
		return
	}
	_ = os.MkdirAll(s.logDir, 0o755)
	var pretty bytes.Buffer
	if json.Indent(&pretty, body, "", "  ") != nil {
		pretty.Write(body)
	}
	_ = os.WriteFile(filepath.Join(s.logDir, fmt.Sprintf("request-%03d-%s.json", n, kind)), pretty.Bytes(), 0o644)
	line, _ := json.Marshal(summarize(n, kind, body, r))
	f, err := os.OpenFile(filepath.Join(s.logDir, "requests.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		f.Write(append(line, '\n'))
		f.Close()
	}
}

func (s *server) stream(w http.ResponseWriter, n int, r request, action step) {
	base := func(choice map[string]any, usage map[string]any) map[string]any {
		c := map[string]any{"id": fmt.Sprintf("chatcmpl-%d", n), "object": "chat.completion.chunk", "created": 0, "model": r.Model, "choices": []any{choice}}
		if usage != nil {
			c["usage"] = usage
		}
		return c
	}
	messagesJSON, _ := json.Marshal(r.Messages)
	prompt := (len(messagesJSON) + 3) / 4
	usage := map[string]any{"prompt_tokens": prompt, "completion_tokens": 20, "total_tokens": prompt + 20}
	var chunks []map[string]any
	if action.kind == "tool" {
		args, _ := json.Marshal(action.args)
		call := map[string]any{"index": 0, "id": fmt.Sprintf("call_%d", n), "type": "function", "function": map[string]any{"name": action.name, "arguments": string(args)}}
		chunks = []map[string]any{
			base(map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{call}}, "finish_reason": nil}, nil),
			base(map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}, usage),
		}
	} else {
		chunks = []map[string]any{
			base(map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": action.text}, "finish_reason": nil}, nil),
			base(map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}, usage),
		}
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(200)
	for _, c := range chunks {
		data, _ := json.Marshal(c)
		fmt.Fprintf(w, "data: %s\n\n", data)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "listen address (loopback)")
	logDir := flag.String("log-dir", "./mock-log", "directory for request logs and the addr file")
	name := flag.String("scenario", "observation-pack", "scenario to play")
	flag.Parse()
	scenario, ok := scenarios[*name]
	if !ok {
		names := make([]string, 0, len(scenarios))
		for k := range scenarios {
			names = append(names, k)
		}
		sort.Strings(names)
		fmt.Fprintf(os.Stderr, "unknown scenario %q; known: %s\n", *name, strings.Join(names, ", "))
		os.Exit(2)
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.MkdirAll(*logDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(*logDir, "addr"), []byte(ln.Addr().String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("mock provider on %s (%s)\n", ln.Addr(), *name)
	if err := http.Serve(ln, &server{scenario: scenario, logDir: *logDir}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
