// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-License-Identifier: MIT

package testhost

import (
	"testing"

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
