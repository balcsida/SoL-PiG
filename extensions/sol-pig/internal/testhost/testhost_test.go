// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

package testhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func TestHostDrivesAToolAndAnEvent(t *testing.T) {
	ext := sdk.New("probe")
	ext.Tool("echo", "echo", sdk.Schema{"type": "object"}, func(ctx sdk.Context, p map[string]any) (any, error) {
		ctx.Notify("hi", "info")
		return sdk.ToolResult{Content: "echo:" + p["text"].(string), Details: map[string]any{"n": 1}}, nil
	})
	seen := 0
	ext.OnEvent(sdk.EventContext, func(ctx sdk.Context, d map[string]any) (any, error) { seen++; return nil, nil })
	h := Start(t, ext, Options{Cwd: t.TempDir(), SessionDir: t.TempDir(), SessionID: "s1"})
	if got := h.Handlers("context"); len(got) != 1 {
		t.Fatalf("handlers = %v", got)
	}
	res := h.CallTool("echo", "call_1", map[string]any{"text": "x"})
	if res.Err != nil || res.Content[0].(map[string]any)["text"] != "echo:x" || res.Details.(map[string]any)["n"] != float64(1) {
		t.Fatalf("result = %+v", res)
	}
	if c := h.Calls("ui.notify"); len(c) != 1 || c[0].Args["message"] != "hi" {
		t.Fatalf("notify calls = %+v", c)
	}
	if _, err := h.Emit("context", map[string]any{"messages": []any{}}); err != nil {
		t.Fatal(err)
	}
	if seen != 1 {
		t.Fatalf("context handler ran %d times", seen)
	}
}

func TestHostRecordsLateRegistration(t *testing.T) {
	ext := sdk.New("probe")
	ext.OnSessionStart(func(ctx sdk.Context, d map[string]any) (any, error) {
		ext.OnEvent(sdk.EventToolResult, func(sdk.Context, map[string]any) (any, error) { return nil, nil })
		ext.RegisterTool(sdk.ToolDefinition{Name: "late", Description: "d", Parameters: sdk.Schema{"type": "object"}, Execute: func(sdk.Context, map[string]any) (any, error) { return "ok", nil }})
		return nil, nil
	})
	h := Start(t, ext, Options{Cwd: t.TempDir()})
	if len(h.RegisteredHandlers()["tool_result"]) != 0 {
		t.Fatal("tool_result subscribed before session_start")
	}
	if _, err := h.Emit("session_start", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if len(h.Handlers("tool_result")) != 1 || len(h.Tools()) != 1 || h.Tools()[0].Name != "late" {
		t.Fatalf("late registration not recorded: %v %v", h.Handlers("tool_result"), h.Tools())
	}
}

func probeHost(t *testing.T, opts Options, body func(ctx sdk.Context)) *Host {
	t.Helper()
	ext := sdk.New("probe")
	ext.Tool("probe", "probe", sdk.Schema{"type": "object"}, func(ctx sdk.Context, p map[string]any) (any, error) {
		body(ctx)
		return "ok", nil
	})
	opts.Cwd, opts.SessionDir, opts.SessionID = t.TempDir(), t.TempDir(), "s1"
	h := Start(t, ext, opts)
	if res := h.CallTool("probe", "c1", map[string]any{}); res.Err != nil {
		t.Fatalf("probe: %v", res.Err)
	}
	return h
}

func firstText(t *testing.T, res sdk.AgentToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatalf("result has no content: %+v", res)
	}
	return res.Content[0].Text
}

func TestBuiltinWriteAndBashThroughTheHost(t *testing.T) {
	cwd := ""
	h := probeHost(t, Options{}, func(ctx sdk.Context) {
		cwd = ctx.Cwd()
		write, err := sdk.CreateWriteToolDefinition(cwd, nil)
		if err != nil {
			t.Error(err)
			return
		}
		res, err := write.Execute(ctx, map[string]any{"path": "out.txt", "content": "hi\n"})
		if err != nil {
			t.Error(err)
			return
		}
		if got := firstText(t, res.(sdk.AgentToolResult)); got != "Successfully wrote to out.txt" {
			t.Errorf("write text = %q", got)
		}
		bash, err := sdk.CreateBashToolDefinition(cwd, &sdk.BashToolOptions{CommandPrefix: "export CI=1"})
		if err != nil {
			t.Error(err)
			return
		}
		res, err = bash.Execute(ctx, map[string]any{"command": "printf native-$CI"})
		if err != nil {
			t.Error(err)
			return
		}
		if got := firstText(t, res.(sdk.AgentToolResult)); got != "native-1" {
			t.Errorf("bash text = %q", got)
		}
		res, err = bash.Execute(ctx, map[string]any{"command": "exit 7"})
		if err != nil {
			t.Error(err)
			return
		}
		failed := res.(sdk.AgentToolResult)
		if got := firstText(t, failed); !strings.HasSuffix(got, "Command exited with code 7") || !failed.IsError {
			t.Errorf("failing bash = %q isError=%v", got, failed.IsError)
		}
	})
	if data, err := os.ReadFile(filepath.Join(cwd, "out.txt")); err != nil || string(data) != "hi\n" {
		t.Errorf("out.txt = %q, %v", data, err)
	}
	calls := h.BuiltinCalls()
	if len(calls) != 3 || calls[1].Name != "bash" || calls[1].Options["commandPrefix"] != "export CI=1" {
		t.Errorf("BuiltinCalls = %+v", calls)
	}
}

func TestBuiltinEditThroughTheHost(t *testing.T) {
	probeHost(t, Options{}, func(ctx sdk.Context) {
		path := filepath.Join(ctx.Cwd(), "f.txt")
		if err := os.WriteFile(path, []byte("alpha beta"), 0o644); err != nil {
			t.Error(err)
			return
		}
		edit, err := sdk.CreateEditToolDefinition(ctx.Cwd(), nil)
		if err != nil {
			t.Error(err)
			return
		}
		edits := func(old, new string) map[string]any {
			return map[string]any{"path": "f.txt", "edits": []any{map[string]any{"oldText": old, "newText": new}}}
		}
		res, err := edit.Execute(ctx, edits("beta", "gamma"))
		if err != nil {
			t.Error(err)
			return
		}
		if got := firstText(t, res.(sdk.AgentToolResult)); got != "Successfully replaced 1 block(s) in f.txt." {
			t.Errorf("edit text = %q", got)
		}
		if data, _ := os.ReadFile(path); string(data) != "alpha gamma" {
			t.Errorf("f.txt = %q", data)
		}
		// PiG throws on a missing oldText, so the SDK surfaces a Go error.
		if _, err = edit.Execute(ctx, edits("zeta", "x")); err == nil || !strings.Contains(err.Error(), "Could not edit file") {
			t.Errorf("missing oldText error = %v", err)
		}
	})
}

func TestBuiltinDefinitionsCarryPiGTexts(t *testing.T) {
	probeHost(t, Options{}, func(ctx sdk.Context) {
		edit, err := sdk.CreateEditToolDefinition(ctx.Cwd(), nil)
		if err != nil {
			t.Error(err)
			return
		}
		if edit.Description != editDescription || len(edit.PromptGuidelines) != 4 {
			t.Errorf("edit description/guidelines = %q / %d", edit.Description, len(edit.PromptGuidelines))
		}
		if req, _ := edit.Parameters["required"].([]any); len(req) != 2 || req[0] != "path" || req[1] != "edits" {
			t.Errorf("edit required = %v", edit.Parameters["required"])
		}
		write, _ := sdk.CreateWriteToolDefinition(ctx.Cwd(), nil)
		if !strings.HasPrefix(write.Description, "Write content to a file. Creates the file if it doesn't exist") {
			t.Errorf("write description = %q", write.Description)
		}
		bash, _ := sdk.CreateBashToolDefinition(ctx.Cwd(), nil)
		props, _ := bash.Parameters["properties"].(map[string]any)
		if props["command"] == nil || props["timeout"] == nil {
			t.Errorf("bash properties = %v", props)
		}
	})
}

func TestSettingsManagerShellOptions(t *testing.T) {
	ptr := func(s string) *string { return &s }
	probeHost(t, Options{ShellPath: ptr("/bin/zsh"), ShellCommandPrefix: ptr("export CI=1")}, func(ctx sdk.Context) {
		agentDir := t.TempDir()
		sm, err := sdk.SettingsManagerCreate(ctx, ctx.Cwd(), &agentDir, &sdk.SettingsManagerCreateOptions{ProjectTrusted: sdk.Bool(true)})
		if err != nil {
			t.Error(err)
			return
		}
		if p, err := sm.GetShellPath(); err != nil || p == nil || *p != "/bin/zsh" {
			t.Errorf("GetShellPath = %v, %v", p, err)
		}
		if p, err := sm.GetShellCommandPrefix(); err != nil || p == nil || *p != "export CI=1" {
			t.Errorf("GetShellCommandPrefix = %v, %v", p, err)
		}
		if errs, err := sm.DrainErrors(); err != nil || len(errs) != 0 {
			t.Errorf("DrainErrors = %v, %v", errs, err)
		}
	})
	probeHost(t, Options{SettingsErrors: []string{"settings.json: bad"}}, func(ctx sdk.Context) {
		sm, err := sdk.SettingsManagerCreate(ctx, ctx.Cwd(), nil, nil)
		if err != nil {
			t.Error(err)
			return
		}
		errs, err := sm.DrainErrors()
		if err != nil || len(errs) != 1 || errs[0].Error.Error() != "settings.json: bad" {
			t.Errorf("DrainErrors = %v, %v", errs, err)
		}
		if p, _ := sm.GetShellPath(); p != nil {
			t.Errorf("GetShellPath = %v, want nil", *p)
		}
	})
}

func TestBuiltinBashTimeoutDoesNotWaitForGrandchildren(t *testing.T) {
	probeHost(t, Options{}, func(ctx sdk.Context) {
		bash, err := sdk.CreateBashToolDefinition(ctx.Cwd(), nil)
		if err != nil {
			t.Error(err)
			return
		}
		start := time.Now()
		_, err = bash.Execute(ctx, map[string]any{"command": "sleep 5; echo x", "timeout": 0.2})
		if err == nil || !strings.Contains(err.Error(), "Command timed out after 0.2 seconds") {
			t.Errorf("err = %v", err)
		}
		if elapsed := time.Since(start); elapsed > 3*time.Second {
			t.Errorf("timeout took %v", elapsed)
		}
	})
}

var fauxModel = map[string]any{"id": "faux-1", "provider": "test-faux", "name": "Faux", "api": "openai-completions", "maxTokens": 4096.0, "contextWindow": 128000.0}

func fauxReply(text string) map[string]any {
	return map[string]any{
		"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}, "stopReason": "stop",
		"provider": "test-faux", "model": "faux-1",
	}
}

func TestModelRegistryFindAndComplete(t *testing.T) {
	var found, missing, reply map[string]any
	var findErr, missingErr error
	h := probeHost(t, Options{
		Models:     map[string]map[string]any{"test-faux/faux-1": fauxModel},
		ModelReply: func(map[string]any) map[string]any { return fauxReply("scripted") },
	}, func(ctx sdk.Context) {
		found, findErr = ctx.ModelRegistry().Find("test-faux", "faux-1")
		missing, missingErr = ctx.ModelRegistry().Find("x", "y")
		reply = ctx.ModelRegistry().Complete(found,
			map[string]any{"systemPrompt": "s", "messages": []any{map[string]any{"role": "user", "content": "hi"}}},
			map[string]any{"maxTokens": 2048, "cacheRetention": "none", "sessionId": "r1", "timeoutMs": 90000})
	})
	if findErr != nil || found["id"] != "faux-1" || missingErr != nil || missing != nil {
		t.Fatalf("Find = %v, %v; missing = %v, %v", found, findErr, missing, missingErr)
	}
	if text := reply["content"].([]any)[0].(map[string]any)["text"]; reply["stopReason"] != "stop" || text != "scripted" {
		t.Fatalf("Complete = %v", reply)
	}
	reqs := h.ModelRequests()
	if len(reqs) != 1 || reqs[0]["maxTokens"] != float64(2048) || reqs[0]["systemPrompt"] != "s" {
		t.Fatalf("ModelRequests = %v", reqs)
	}
}

func TestModelCompleteWithoutScriptedReplyIsAnError(t *testing.T) {
	var reply map[string]any
	probeHost(t, Options{}, func(ctx sdk.Context) { reply = ctx.ModelRegistry().Complete(fauxModel, map[string]any{}, nil) })
	if reply["stopReason"] != "error" || reply["errorMessage"] != "no model scripted" {
		t.Fatalf("Complete = %v", reply)
	}
}

func TestModelReplyDelay(t *testing.T) {
	var took time.Duration
	probeHost(t, Options{
		ModelDelay: 300 * time.Millisecond,
		ModelReply: func(map[string]any) map[string]any { return fauxReply("late") },
	}, func(ctx sdk.Context) {
		start := time.Now()
		ctx.ModelRegistry().Complete(fauxModel, map[string]any{}, nil)
		took = time.Since(start)
	})
	if took < 300*time.Millisecond {
		t.Fatalf("Complete returned after %v, want >= 300ms", took)
	}
}
