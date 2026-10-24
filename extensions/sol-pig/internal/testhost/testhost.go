// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

// Package testhost is a fake PiG host for tests. It speaks the subprocess
// wire of PiG v0.5.3 over net.Pipe (4-byte big-endian length prefix, then
// JSON), as examples/extensions/plan-mode/main_test.go in PiG does, and
// covers only what SoL-PiG's tests need.
package testhost

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Options describes the session the fake host presents.
type Options struct {
	Cwd, Mode      string // Mode defaults to "tui"
	ProjectTrusted bool
	SessionDir     string // "" means no persistent session (SoL-Pi temp fallback)
	SessionID      string
	Settings       map[string]any // answer to GetSettings; nil means the host sent none
	ActiveTools    []string

	ShellPath, ShellCommandPrefix *string  // answers for the SettingsManager; nil → JSON null
	SettingsErrors                []string // drainErrors returns one global-scope error per entry
}

// Call is one recorded extension→host call.
type Call struct {
	Method string
	Args   map[string]any
}

// ToolDecl is a tool as the extension declared it.
type ToolDecl struct {
	Name, Description, PromptSnippet string
	PromptGuidelines                 []string
	Parameters                       map[string]any
	PreparesArguments                bool
}

// ToolCallResult is a tool call's outcome as the host sees it.
type ToolCallResult struct {
	Content []any
	Details any
	IsError bool
	Err     error
}

type envelope struct {
	Type       string          `json:"type"`
	ID         string          `json:"id,omitempty"`
	Register   json.RawMessage `json:"register,omitempty"`
	Response   *responseMsg    `json:"response,omitempty"`
	Call       *callMsg        `json:"call,omitempty"`
	Notify     *callMsg        `json:"notify,omitempty"`
	WidgetPush json.RawMessage `json:"widget_push,omitempty"`
}

type responseMsg struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *errorInfo      `json:"error,omitempty"`
}

type callMsg struct {
	Method string          `json:"method"`
	Args   json.RawMessage `json:"args,omitempty"`
}

type errorInfo struct {
	Message string `json:"message"`
}

type wireTool struct {
	Name              string         `json:"name"`
	Description       string         `json:"description"`
	PromptSnippet     string         `json:"prompt_snippet"`
	PromptGuidelines  []string       `json:"prompt_guidelines"`
	Parameters        map[string]any `json:"parameters"`
	PreparesArguments bool           `json:"prepares_arguments"`
}

type wireRegister struct {
	Name     string     `json:"name"`
	Tools    []wireTool `json:"tools"`
	Handlers []struct {
		Event     string `json:"event"`
		HandlerID int    `json:"handler_id"`
	} `json:"handlers"`
}

// Host is one fake PiG host bound to one extension.
type Host struct {
	t    *testing.T
	opts Options
	conn net.Conn
	done chan error

	writeMu sync.Mutex

	mu         sync.Mutex
	registered map[string][]int
	handlers   map[string][]int
	tools      map[string]ToolDecl
	toolOrder  []string
	calls      []Call
	entries    []map[string]any
	scripted   map[string]func(args map[string]any) (any, error)
	pending    map[string]chan responseMsg
	nextID     uint64
	handles    uint64
	cancels    map[string]context.CancelFunc // in-flight builtinTool.execute by executeId
}

// Start runs ext against a fake host and returns once the handshake is done.
func Start(t *testing.T, ext *sdk.Extension, opts Options) *Host {
	t.Helper()
	if opts.Mode == "" {
		opts.Mode = "tui"
	}
	if opts.ActiveTools == nil {
		opts.ActiveTools = []string{}
	}
	hostConn, extConn := net.Pipe()
	h := &Host{
		t: t, opts: opts, conn: hostConn, done: make(chan error, 1),
		registered: map[string][]int{}, handlers: map[string][]int{}, tools: map[string]ToolDecl{},
		scripted: map[string]func(map[string]any) (any, error){}, pending: map[string]chan responseMsg{},
	}
	go func() { h.done <- ext.RunWithConn(extConn) }()

	env := h.readEnvelope()
	if env.Type != "register" || env.Register == nil {
		t.Fatalf("first extension frame = %+v, want register", env)
	}
	var reg wireRegister
	if err := json.Unmarshal(env.Register, &reg); err != nil {
		t.Fatalf("decode register frame: %v", err)
	}
	for _, hd := range reg.Handlers {
		h.registered[hd.Event] = append(h.registered[hd.Event], hd.HandlerID)
		h.handlers[hd.Event] = append(h.handlers[hd.Event], hd.HandlerID)
	}
	for _, tool := range reg.Tools {
		h.recordTool(tool)
	}
	state := map[string]any{
		"activeTools": opts.ActiveTools, "isIdle": false, "projectTrusted": opts.ProjectTrusted, "hasUI": true,
	}
	if opts.Settings != nil {
		state["settings"] = opts.Settings
	}
	h.writeEnvelope(map[string]any{
		"type": "ready",
		"ready": map[string]any{
			"cwd": opts.Cwd, "mode": opts.Mode, "width": 100, "height": 30, "state": state,
		},
	})
	go h.readLoop()
	t.Cleanup(func() {
		_ = hostConn.Close()
		select {
		case <-h.done:
		case <-time.After(2 * time.Second):
			t.Error("extension did not stop after transport close")
		}
	})
	return h
}

// RegisteredHandlers returns the handlers declared in the register frame.
func (h *Host) RegisteredHandlers() map[string][]int {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string][]int{}
	for event, ids := range h.registered {
		out[event] = slices.Clone(ids)
	}
	return out
}

// Handlers returns the live handlers for event, late subscriptions included.
func (h *Host) Handlers(event string) []int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.handlers[event])
}

// Tools returns the declared tools, latest declaration per name, in first-seen order.
func (h *Host) Tools() []ToolDecl {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]ToolDecl, 0, len(h.toolOrder))
	for _, name := range h.toolOrder {
		out = append(out, h.tools[name])
	}
	return out
}

// Calls returns the recorded extension→host calls for method, in order.
func (h *Host) Calls(method string) []Call {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Call
	for _, c := range h.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// Entries returns the custom entries appended so far, as getBranch serves them.
func (h *Host) Entries() []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.entries)
}

// Handle scripts the reply to a host method, replacing the default.
func (h *Host) Handle(method string, fn func(args map[string]any) (any, error)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.scripted[method] = fn
}

// Emit delivers event to every live handler in order and returns the last non-nil result.
func (h *Host) Emit(event string, data map[string]any) (any, error) {
	h.t.Helper()
	var last any
	for _, id := range h.Handlers(event) {
		raw, err := h.invoke("event", "", event, id, "", data)
		if err != nil {
			return nil, err
		}
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var result any
		if err := json.Unmarshal(raw, &result); err != nil {
			h.t.Fatalf("decode %s result %s: %v", event, raw, err)
		}
		last = result
	}
	return last, nil
}

// StartRun tells the extension a run is active (the SDK's ctx.Signal becomes non-nil).
func (h *Host) StartRun() { h.runSignal(false) }

// AbortRun aborts the active run: the SDK cancels ctx.Signal.
func (h *Host) AbortRun() { h.runSignal(true) }

func (h *Host) runSignal(aborted bool) {
	h.writeEnvelope(map[string]any{"type": "notify", "notify": map[string]any{
		"method": "run_signal", "args": map[string]any{"run": 1, "active": true, "aborted": aborted},
	}})
}

// EmitContext emits a context event and returns the message list the host now
// holds, and whether a handler replaced the list rather than editing it in place.
func (h *Host) EmitContext(messages []any) (held []any, replaced bool) {
	h.t.Helper()
	result, err := h.Emit("context", map[string]any{"messages": messages})
	if err != nil {
		h.t.Fatalf("context handler failed: %v", err)
	}
	reply, _ := result.(map[string]any)
	if list, ok := reply["messages"].([]any); ok {
		unchanged, _ := reply["_pigContextUnchanged"].(bool)
		return list, !unchanged
	}
	return messages, false
}

// CallTool invokes a tool as the agent would.
func (h *Host) CallTool(name, toolCallID string, params map[string]any) ToolCallResult {
	h.t.Helper()
	raw, err := h.invoke("tool_call", name, "", 0, toolCallID, params)
	if err != nil {
		return ToolCallResult{Err: err}
	}
	var decoded any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			h.t.Fatalf("decode tool result %s: %v", raw, err)
		}
	}
	return normalizeToolResult(decoded)
}

func normalizeToolResult(decoded any) ToolCallResult {
	switch v := decoded.(type) {
	case string:
		return ToolCallResult{Content: []any{map[string]any{"type": "text", "text": v}}}
	case map[string]any:
		res := ToolCallResult{Details: v["details"]}
		switch content := v["content"].(type) {
		case []any:
			res.Content = content
		case string:
			res.Content = []any{map[string]any{"type": "text", "text": content}}
		}
		if b, ok := v["is_error"].(bool); ok {
			res.IsError = b
		} else if b, ok := v["isError"].(bool); ok {
			res.IsError = b
		}
		return res
	}
	return ToolCallResult{}
}

func (h *Host) invoke(method, tool, event string, handlerID int, toolCallID string, args any) (json.RawMessage, error) {
	h.mu.Lock()
	h.nextID++
	id := fmt.Sprintf("req-%d", h.nextID)
	ch := make(chan responseMsg, 1)
	h.pending[id] = ch
	h.mu.Unlock()

	request := map[string]any{"method": method, "args": args}
	if tool != "" {
		request["tool"] = tool
	}
	if event != "" {
		request["event"] = event
		request["handler_id"] = handlerID
	}
	if toolCallID != "" {
		request["tool_call_id"] = toolCallID
	}
	h.writeEnvelope(map[string]any{"type": "request", "id": id, "request": request})
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("%s", resp.Error.Message)
		}
		return resp.Result, nil
	case <-time.After(10 * time.Second):
		h.t.Fatalf("no response to %s %s%s within 10s", method, tool, event)
		return nil, nil
	}
}

func (h *Host) readLoop() {
	for {
		env, err := h.tryReadEnvelope()
		if err != nil {
			return
		}
		switch env.Type {
		case "response":
			h.mu.Lock()
			ch := h.pending[env.ID]
			delete(h.pending, env.ID)
			h.mu.Unlock()
			if ch != nil && env.Response != nil {
				ch <- *env.Response
			}
		case "call":
			h.handleCall(env.ID, env.Call)
		case "notify":
			if env.Notify != nil {
				h.record(env.Notify.Method, env.Notify.Args)
			}
		}
	}
}

func (h *Host) record(method string, raw json.RawMessage) map[string]any {
	var args map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &args)
	}
	h.mu.Lock()
	h.calls = append(h.calls, Call{Method: method, Args: args})
	h.mu.Unlock()
	return args
}

func (h *Host) recordTool(tool wireTool) {
	if _, seen := h.tools[tool.Name]; !seen {
		h.toolOrder = append(h.toolOrder, tool.Name)
	}
	h.tools[tool.Name] = ToolDecl{
		Name: tool.Name, Description: tool.Description, PromptSnippet: tool.PromptSnippet,
		PromptGuidelines: tool.PromptGuidelines, Parameters: tool.Parameters, PreparesArguments: tool.PreparesArguments,
	}
}

func (h *Host) handleCall(id string, call *callMsg) {
	if call == nil {
		return
	}
	args := h.record(call.Method, call.Args)
	if call.Method == "builtinTool.execute" {
		// Runs aside so an executeTool.cancel can arrive while the tool is still running.
		go h.finishCall(id, call, args)
		return
	}
	h.finishCall(id, call, args)
}

func (h *Host) finishCall(id string, call *callMsg, args map[string]any) {
	result, err := h.reply(call.Method, call.Args, args)
	msg := map[string]any{}
	if err != nil {
		msg["error"] = map[string]any{"message": err.Error()}
	} else {
		msg["result"] = result
	}
	h.writeEnvelope(map[string]any{"type": "call_result", "id": id, "call_result": msg})
}

func (h *Host) reply(method string, raw json.RawMessage, args map[string]any) (any, error) {
	h.mu.Lock()
	scripted := h.scripted[method]
	h.mu.Unlock()
	if scripted != nil {
		return scripted(args)
	}
	empty := map[string]any{}
	switch method {
	case "ui.notify", "ui.setStatus", "sessionGraph.release":
		return empty, nil
	case "event.subscribe", "event.unsubscribe":
		event, _ := args["event"].(string)
		idf, _ := args["handlerId"].(float64)
		id := int(idf)
		h.mu.Lock()
		if method == "event.subscribe" {
			if !slices.Contains(h.handlers[event], id) {
				h.handlers[event] = append(h.handlers[event], id)
			}
		} else {
			h.handlers[event] = slices.DeleteFunc(h.handlers[event], func(x int) bool { return x == id })
		}
		h.mu.Unlock()
		return empty, nil
	case "registerTool":
		var tool wireTool
		if err := json.Unmarshal(raw, &tool); err != nil {
			return nil, fmt.Errorf("decode registerTool: %w", err)
		}
		h.mu.Lock()
		h.recordTool(tool)
		h.mu.Unlock()
		return empty, nil
	case "getActiveTools":
		return map[string]any{"tools": h.opts.ActiveTools, "activeTools": h.opts.ActiveTools}, nil
	case "isProjectTrusted":
		return map[string]any{"trusted": h.opts.ProjectTrusted}, nil
	case "isIdle":
		return map[string]any{"idle": false}, nil
	case "appendEntry":
		h.mu.Lock()
		h.entries = append(h.entries, map[string]any{"type": "custom", "customType": args["customType"], "data": args["data"]})
		h.mu.Unlock()
		return empty, nil
	case "sessionGraph.new":
		h.mu.Lock()
		h.handles++
		handle := fmt.Sprintf("h%d-%v", h.handles, args["kind"])
		h.mu.Unlock()
		return map[string]any{"handle": handle}, nil
	case "builtinTool.definition":
		return h.builtinDefinition(args)
	case "builtinTool.execute":
		return h.builtinExecute(args)
	case "executeTool.cancel":
		id, _ := args["executeId"].(string)
		h.mu.Lock()
		cancel := h.cancels[id]
		h.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return empty, nil
	case "sessionRead", "sessionGraph.call":
		switch m, _ := args["method"].(string); m {
		case "getShellPath":
			return h.opts.ShellPath, nil
		case "getShellCommandPrefix":
			return h.opts.ShellCommandPrefix, nil
		case "drainErrors":
			errs := []any{}
			for _, message := range h.opts.SettingsErrors {
				errs = append(errs, map[string]any{"scope": "global", "path": nil, "error": message})
			}
			return errs, nil
		case "getSessionDir":
			return h.opts.SessionDir, nil
		case "getSessionId", "getSessionID":
			return h.opts.SessionID, nil
		case "getBranch":
			return h.Entries(), nil
		default:
			h.t.Errorf("unhandled session manager call %s %v", m, args)
			return nil, fmt.Errorf("unhandled session manager call %s", m)
		}
	}
	if strings.HasPrefix(method, "request_state") {
		return empty, nil
	}
	h.t.Errorf("unhandled host call %s %v", method, args)
	return nil, fmt.Errorf("unhandled host call %s", method)
}

func (h *Host) readEnvelope() envelope {
	h.t.Helper()
	env, err := h.tryReadEnvelope()
	if err != nil {
		h.t.Fatalf("read frame: %v", err)
	}
	return env
}

func (h *Host) tryReadEnvelope() (envelope, error) {
	var header [4]byte
	if _, err := io.ReadFull(h.conn, header[:]); err != nil {
		return envelope{}, err
	}
	payload := make([]byte, binary.BigEndian.Uint32(header[:]))
	if _, err := io.ReadFull(h.conn, payload); err != nil {
		return envelope{}, err
	}
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return envelope{}, fmt.Errorf("decode frame %s: %w", payload, err)
	}
	return env, nil
}

func (h *Host) writeEnvelope(value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		h.t.Fatalf("encode frame: %v", err)
	}
	frame := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(frame, uint32(len(payload)))
	copy(frame[4:], payload)
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if _, err := h.conn.Write(frame); err != nil {
		h.t.Errorf("write frame: %v", err)
	}
}
