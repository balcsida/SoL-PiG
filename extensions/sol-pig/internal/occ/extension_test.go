// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi tests/online-context-compact.test.ts and tests/online-context-compact-recovery.test.ts at 2195680 (NVIDIA, MIT).

package occ

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/balcsida/SoL-PiG/extensions/sol-pig/internal/testhost"
)

var (
	stepsOpen = []any{map[string]any{"id": "build", "goal": "build it", "status": "in_progress"}}
	stepsDone = []any{map[string]any{"id": "build", "goal": "build it", "status": "completed"}}
	progress  = map[string]any{
		"files_changed": []any{"src/a.go"}, "verification": []any{"tests passed"}, "decisions": []any{"kept it small"},
	}

	oldUser      = map[string]any{"role": "user", "content": "old " + strings.Repeat("x", 8000), "timestamp": 1.0}
	workAssist   = assistantTurn("work "+strings.Repeat("y", 2000), "stop")
	boundaryTurn = assistantTurn("boundary", "stop")
)

func assistantTurn(text, stopReason string) map[string]any {
	return map[string]any{
		"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}},
		"api": "test", "provider": "test", "model": "test", "stopReason": stopReason, "timestamp": 2.0,
		"usage": map[string]any{
			"input": 1.0, "output": 1.0, "cacheRead": 0.0, "cacheWrite": 0.0, "totalTokens": 2.0,
			"cost": map[string]any{"input": 0.0, "output": 0.0, "cacheRead": 0.0, "cacheWrite": 0.0, "total": 0.0},
		},
	}
}

func projectionOf(messages ...map[string]any) func([]map[string]any) []map[string]any {
	return func([]map[string]any) []map[string]any {
		out := make([]map[string]any, len(messages))
		for i, m := range messages {
			out[i] = map[string]any{
				"sourceEntry": map[string]any{"type": "message", "id": fmt.Sprintf("e%d", i), "message": m},
				"messages":    []any{m},
			}
		}
		return out
	}
}

type rig struct {
	*testhost.Host
	ext *sdk.Extension
}

func startRig(t *testing.T, opts Options, host testhost.Options) *rig {
	t.Helper()
	ext := sdk.New("probe")
	if err := Register(ext, opts); err != nil {
		t.Fatal(err)
	}
	ext.Tool("seed", "seed", sdk.Schema{"type": "object"}, func(ctx sdk.Context, p map[string]any) (any, error) {
		return "ok", ctx.AppendEntry("sol-pi-online-context-state-v1", p["state"])
	})
	if host.Cwd == "" {
		host.Cwd = t.TempDir()
	}
	if host.SessionDir == "" {
		host.SessionDir, host.SessionID = t.TempDir(), "s1"
	}
	if host.Settings == nil {
		host.Settings = map[string]any{}
	}
	if host.ContextUsage == nil {
		host.ContextUsage = map[string]any{"tokens": 195000, "contextWindow": 200000}
	}
	if host.ModelInfo == nil {
		host.ModelInfo = map[string]any{"id": "m1", "provider": "p", "contextWindow": 200000}
	}
	if host.Projection == nil {
		host.Projection = projectionOf(oldUser, workAssist)
	}
	return &rig{Host: testhost.Start(t, ext, host), ext: ext}
}

func ratio(v float64) *float64 { return &v }
func keep(v int) *int          { return &v }

// boundary runs the two plan updates and the turn_end that completes the step; it returns the turn_end result.
func (r *rig) boundary(t *testing.T, turnMessage map[string]any, resultError bool) {
	t.Helper()
	r.EmitContext([]any{oldUser, workAssist})
	for _, call := range []struct {
		id     string
		params map[string]any
	}{{"plan-open", map[string]any{"steps": stepsOpen}}, {"plan-done", map[string]any{"steps": stepsDone, "progress": progress}}} {
		if res := r.CallTool("update_plan", call.id, call.params); res.Err != nil {
			t.Fatalf("%s: %v", call.id, res.Err)
		}
	}
	r.turnEnd(t, turnMessage, planResult("plan-done", resultError))
}

func planResult(id string, isError bool) map[string]any {
	return map[string]any{"role": "toolResult", "toolCallId": id, "toolName": "update_plan", "isError": isError, "timestamp": 3.0}
}

func (r *rig) turnEnd(t *testing.T, message map[string]any, results ...map[string]any) {
	t.Helper()
	list := make([]any, len(results))
	for i, res := range results {
		list[i] = res
	}
	if _, err := r.Emit("turn_end", map[string]any{"type": "turn_end", "message": message, "toolResults": list}); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) settle(t *testing.T) error {
	t.Helper()
	_, err := r.Emit("agent_settled", map[string]any{"type": "agent_settled"})
	return err
}

func stateEntries(r *rig) []State {
	var out []State
	for _, e := range r.Entries() {
		if e["customType"] == "sol-pi-online-context-state-v1" {
			out = append(out, RestoreState([]map[string]any{e}))
		}
	}
	return out
}

func lastState(t *testing.T, r *rig) State {
	t.Helper()
	states := stateEntries(r)
	if len(states) == 0 {
		t.Fatal("no state entry was written")
	}
	return states[len(states)-1]
}

func TestRegistersOneToolAndTwelveEventsInOrder(t *testing.T) {
	ext := sdk.New("probe")
	if err := Register(ext, Options{}); err != nil {
		t.Fatal(err)
	}
	h := testhost.Start(t, ext, testhost.Options{Cwd: t.TempDir()})
	tools := h.Tools()
	if len(tools) != 1 || tools[0].Name != "update_plan" || tools[0].Exposure != "model-only" || tools[0].ExecutionMode != "sequential" || tools[0].RenderShell != "self" {
		t.Fatalf("tools = %+v", tools)
	}
	if tools[0].PromptSnippet != "Keep the working plan current" || tools[0].Description != "Replace the complete working plan. Completing a previously registered unfinished step becomes a safe point where SoL-Pi may compact context if doing so is economical. Newly introduced completed steps are recorded as history." {
		t.Fatalf("tool texts = %+v", tools[0])
	}
	wantGuidelines := []string{
		"Send the complete plan on every update_plan call.",
		"Preserve step IDs and register unfinished steps before reporting their completion.",
		"Keep at most one step in_progress and mark finished steps completed.",
		"When completing a step, include concise progress evidence when available.",
	}
	if !reflect.DeepEqual(tools[0].PromptGuidelines, wantGuidelines) {
		t.Fatalf("guidelines = %q", tools[0].PromptGuidelines)
	}
	if got := normalizeJSON(t, tools[0].Parameters); !reflect.DeepEqual(got, normalizeJSON(t, wantSchema)) {
		t.Fatalf("parameters = %v", got)
	}
	registered := h.RegisteredHandlers()
	want := []string{"session_start", "session_before_tree", "session_tree", "context", "before_provider_request", "message_end", "input", "turn_end", "agent_settled", "session_before_compact", "session_compact", "session_shutdown"}
	if len(registered) != len(want) {
		t.Fatalf("events = %v", registered)
	}
	for _, event := range want {
		if len(registered[event]) != 1 {
			t.Fatalf("event %s handlers = %v", event, registered[event])
		}
	}
	// Handler ids are allocated in registration order.
	var ids []int
	for _, event := range want {
		ids = append(ids, registered[event][0])
	}
	if !slices.IsSorted(ids) {
		t.Fatalf("registration order = %v", ids)
	}
}

func TestRegisterValidatesOptions(t *testing.T) {
	for _, tc := range []struct {
		opts Options
		want string
	}{
		{Options{KeepRecentTokens: keep(0)}, "Online Context Compact keepRecentTokens must be a positive safe integer"},
		{Options{KeepRecentTokens: keep(-3)}, "Online Context Compact keepRecentTokens must be a positive safe integer"},
		{Options{CacheWriteReadRatio: ratio(-1)}, "Online Context Compact cacheWriteReadRatio must be finite and non-negative"},
		{Options{CacheWriteReadRatio: ratio(math.Inf(1))}, "Online Context Compact cacheWriteReadRatio must be finite and non-negative"},
	} {
		if err := Register(sdk.New("probe"), tc.opts); err == nil || err.Error() != tc.want {
			t.Errorf("Register(%+v) = %v, want %q", tc.opts, err, tc.want)
		}
	}
	if err := Register(sdk.New("probe"), Options{KeepRecentTokens: keep(1), CacheWriteReadRatio: ratio(0)}); err != nil {
		t.Fatal(err)
	}
}

func TestResolveCompactionTokenSettings(t *testing.T) {
	model := &sdk.ModelInfo{Provider: "p", ID: "m1"}
	settings := map[string]any{"compaction": map[string]any{
		"keepRecentTokens": 3000.0, "reserveTokens": 4000.0,
		"modelOverrides": map[string]any{"p/m1": map[string]any{"keepRecentTokens": 500.0, "reserveTokens": -1.0}},
	}}
	for _, tc := range []struct {
		name     string
		settings map[string]any
		model    *sdk.ModelInfo
		keep     int
		reserve  int
	}{
		{"override over base, invalid override ignored", settings, model, 500, 4000},
		{"base without a model", settings, nil, 3000, 4000},
		{"base for another model", settings, &sdk.ModelInfo{Provider: "p", ID: "other"}, 3000, 4000},
		{"defaults", map[string]any{}, model, 20000, 16384},
		{"strings and fractions ignored", map[string]any{"compaction": map[string]any{"keepRecentTokens": "9", "reserveTokens": 1.5}}, nil, 20000, 16384},
		{"zero is valid", map[string]any{"compaction": map[string]any{"keepRecentTokens": 0.0}}, nil, 0, 16384},
	} {
		keepTokens, reserve := resolveCompactionTokenSettings(tc.settings, tc.model)
		if keepTokens != tc.keep || reserve != tc.reserve {
			t.Errorf("%s: got %d/%d, want %d/%d", tc.name, keepTokens, reserve, tc.keep, tc.reserve)
		}
	}
}

func TestContextHandlerLeavesMessagesUnchanged(t *testing.T) {
	r := startRig(t, Options{}, testhost.Options{})
	messages := []any{oldUser, workAssist}
	held, replaced := r.EmitContext(messages)
	if replaced || len(held) != 2 {
		t.Fatalf("held = %v replaced = %v", held, replaced)
	}
}

func TestUpdatePlanResultAndErrors(t *testing.T) {
	r := startRig(t, Options{}, testhost.Options{})
	res := r.CallTool("update_plan", "c1", map[string]any{"steps": []any{
		map[string]any{"id": "a", "goal": "do <a> & b", "status": "pending"},
	}})
	if res.Err != nil || res.IsError {
		t.Fatalf("result = %+v", res)
	}
	text := res.Content[0].(map[string]any)["text"].(string)
	want := `<sol-pi-plan task_status="active">{"steps":[{"id":"a","goal":"do <a> & b","status":"pending"}]}</sol-pi-plan>` + "\n" +
		"Mark one pending plan step in_progress before starting it."
	if text != want {
		t.Fatalf("text = %q, want %q", text, want)
	}
	details := res.Details.(map[string]any)
	if details["boundary"] != false || details["progress_recorded"] != false || details["task_status"] != "active" || len(details["completed_step_ids"].([]any)) != 0 {
		t.Fatalf("details = %v", details)
	}

	for _, steps := range []any{[]any{}, []any{map[string]any{"id": "a", "goal": "g", "status": "bogus"}}} {
		res := r.CallTool("update_plan", "c2", map[string]any{"steps": steps})
		if res.Err == nil && !res.IsError || res.Err != nil && !strings.Contains(res.Err.Error(), "Plan must contain at least one valid step") {
			t.Fatalf("invalid steps result = %+v", res)
		}
	}

	r.StartRun()
	r.AbortRun()
	waitFor(t, func() bool {
		res := r.CallTool("update_plan", "c3", map[string]any{"steps": stepsOpen})
		return res.Err != nil && strings.Contains(res.Err.Error(), "Plan update was aborted")
	})
}

func TestUpdatePlanBoundaryDetails(t *testing.T) {
	r := startRig(t, Options{}, testhost.Options{})
	r.CallTool("update_plan", "c1", map[string]any{"steps": stepsOpen})
	res := r.CallTool("update_plan", "c2", map[string]any{"steps": stepsDone, "progress": progress})
	details := res.Details.(map[string]any)
	if details["boundary"] != true || details["progress_recorded"] != true || fmt.Sprint(details["completed_step_ids"]) != "[build]" {
		t.Fatalf("details = %v", details)
	}
	s := lastState(t, r)
	if len(s.PendingProgress) != 1 || s.PendingProgress[0].StepID != "build" || s.PendingProgress[0].Verification[0] != "tests passed" || len(s.PendingProgress[0].NextWork) != 0 {
		t.Fatalf("state = %+v", s)
	}
	if len(stateEntries(r)) != 2 {
		t.Fatalf("state entries = %d, want one per update_plan", len(stateEntries(r)))
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (r *rig) providerRequest(t *testing.T) {
	t.Helper()
	if _, err := r.Emit("before_provider_request", map[string]any{"type": "before_provider_request", "payload": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) assistantEnds(t *testing.T) {
	t.Helper()
	if _, err := r.Emit("message_end", map[string]any{"type": "message_end", "message": workAssist}); err != nil {
		t.Fatal(err)
	}
}

func TestRequestCounting(t *testing.T) {
	r := startRig(t, Options{}, testhost.Options{})
	r.providerRequest(t) // a cache-warm replay: no context event before it
	if n := len(stateEntries(r)); n != 0 {
		t.Fatalf("replay without context wrote %d state entries", n)
	}
	r.EmitContext([]any{oldUser})
	r.providerRequest(t)
	r.providerRequest(t) // a second event for the same request
	if s := lastState(t, r); s.RequestCount != 1 || len(stateEntries(r)) != 1 {
		t.Fatalf("after one request: count %d, entries %d", s.RequestCount, len(stateEntries(r)))
	}
	r.assistantEnds(t)
	r.providerRequest(t) // a replay after the assistant message ended the request
	if s := lastState(t, r); s.RequestCount != 1 {
		t.Fatalf("replay after message_end counted: %d", s.RequestCount)
	}
	r.EmitContext([]any{oldUser})
	r.providerRequest(t)
	if s := lastState(t, r); s.RequestCount != 2 {
		t.Fatalf("second request: %d", s.RequestCount)
	}
}

func TestAbortedRunContextIsNotARequest(t *testing.T) {
	r := startRig(t, Options{}, testhost.Options{})
	r.StartRun()
	r.AbortRun()
	waitFor(t, func() bool {
		r.EmitContext([]any{oldUser})
		before := len(stateEntries(r))
		r.providerRequest(t)
		return len(stateEntries(r)) == before // the aborted run's build was not counted
	})
}

func TestRequestRecordsTheLargerContextEstimate(t *testing.T) {
	r := startRig(t, Options{}, testhost.Options{ContextUsage: map[string]any{"tokens": 10, "contextWindow": 200000}, SystemPrompt: strings.Repeat("s", 400)})
	r.EmitContext([]any{oldUser}) // 2001 tokens + 100 for the system prompt beat the reported 10
	r.providerRequest(t)
	if s := lastState(t, r); s.LastContextTokens == nil || *s.LastContextTokens != 2101 {
		t.Fatalf("lastContextTokens = %v", s.LastContextTokens)
	}
}

func TestBoundaryFlowCompactsAndResumes(t *testing.T) {
	var instructions []string
	r := startRig(t, Options{CacheWriteReadRatio: ratio(12.5), KeepRecentTokens: keep(1)}, testhost.Options{
		CompactResult: func(o map[string]any) (map[string]any, error) {
			instructions = append(instructions, o["customInstructions"].(string))
			return map[string]any{"summary": "SUMMARY", "firstKeptEntryId": "e1", "tokensBefore": 195000}, nil
		},
	})
	r.boundary(t, boundaryTurn, false)
	if r.Aborts() != 1 {
		t.Fatalf("aborts = %d, want 1 at turn_end", r.Aborts())
	}
	if len(r.Calls("compact")) != 0 {
		t.Fatal("compaction started before agent_settled")
	}
	if got, err := r.Emit("session_before_compact", map[string]any{"reason": "threshold"}); err != nil || got.(map[string]any)["cancel"] != true {
		t.Fatalf("threshold while selected = %v, %v", got, err)
	}
	if err := r.settle(t); err != nil {
		t.Fatal(err)
	}
	if len(instructions) != 1 || instructions[0] != "Preserve completed work, verification results, important decisions, and remaining work." {
		t.Fatalf("compact instructions = %v", instructions)
	}
	msgs := r.Messages()
	if len(msgs) != 1 {
		t.Fatalf("messages = %+v", msgs)
	}
	m := msgs[0].Args
	if m["customType"] != "sol-pi-online-context-compact" || m["content"] != "Online context compaction finished. The parent task is still active. Continue the remaining work from the current plan. Preserve existing step IDs when updating progress." || m["display"] != false || m["options"].(map[string]any)["triggerTurn"] != true {
		t.Fatalf("message = %+v", m)
	}
	// Only the 2,001-token user message is removable: saving 2001-1000, debt (195000-1001)*11.5.
	s := lastState(t, r)
	if s.NativeCompactionCount != 1 || len(s.PendingProgress) != 0 || s.CacheDebtTokens != 2230988.5 || s.CacheDebtRepaymentTokens != 1001 || s.LastCompactionRequestCount == nil {
		t.Fatalf("state = %+v", s)
	}
	notes := r.Calls("ui.notify")
	if len(notes) != 1 || !strings.Contains(notes[0].Args["message"].(string), "1,999 context tokens removed") {
		t.Fatalf("savings notices = %+v", notes)
	}
	// The selection is consumed: a later threshold compaction is not cancelled.
	if got, _ := r.Emit("session_before_compact", map[string]any{"reason": "threshold"}); got != nil {
		t.Fatalf("threshold after consumption = %v", got)
	}
	// A second settle finds nothing pending.
	if err := r.settle(t); err != nil || len(r.Calls("compact")) != 1 || len(r.Messages()) != 1 {
		t.Fatalf("second settle: %v, compacts %d, messages %d", err, len(r.Calls("compact")), len(r.Messages()))
	}
}

func TestBoundaryInFlightCancelsTreeNavigation(t *testing.T) {
	release := make(chan struct{})
	r := startRig(t, Options{CacheWriteReadRatio: ratio(12.5), KeepRecentTokens: keep(1)}, testhost.Options{
		CompactResult: func(map[string]any) (map[string]any, error) {
			<-release
			return map[string]any{"summary": "S", "firstKeptEntryId": "e1", "tokensBefore": 1}, nil
		},
	})
	r.boundary(t, boundaryTurn, false)
	done := make(chan error, 1)
	go func() { done <- r.settle(t) }()
	waitFor(t, func() bool { return len(r.Calls("compact")) == 1 })
	var got any
	waitFor(t, func() bool { got, _ = r.Emit("session_before_tree", map[string]any{}); return got != nil })
	if got.(map[string]any)["cancel"] != true {
		t.Fatalf("tree during compaction = %v", got)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got, _ := r.Emit("session_before_tree", map[string]any{}); got != nil {
		t.Fatalf("tree after compaction = %v", got)
	}
}

func TestSettleWaitsWhileNotIdle(t *testing.T) {
	r := startRig(t, Options{CacheWriteReadRatio: ratio(12.5), KeepRecentTokens: keep(1)}, testhost.Options{})
	r.boundary(t, boundaryTurn, false)
	r.SetIdle(false)
	if err := r.settle(t); err != nil || len(r.Calls("compact")) != 0 {
		t.Fatalf("busy settle: %v, compacts %d", err, len(r.Calls("compact")))
	}
	r.SetIdle(true)
	if err := r.settle(t); err != nil || len(r.Calls("compact")) != 1 {
		t.Fatalf("idle settle: %v, compacts %d", err, len(r.Calls("compact")))
	}
}

func TestNoAbortWhenTheBoundaryFailed(t *testing.T) {
	for _, tc := range []struct {
		name        string
		message     map[string]any
		resultError bool
	}{
		{"error result", boundaryTurn, true},
		{"errored assistant", assistantTurn("", "error"), false},
		{"aborted assistant", assistantTurn("", "aborted"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := startRig(t, Options{CacheWriteReadRatio: ratio(12.5), KeepRecentTokens: keep(1)}, testhost.Options{})
			r.boundary(t, tc.message, tc.resultError)
			if r.Aborts() != 0 {
				t.Fatalf("aborts = %d", r.Aborts())
			}
			if got, _ := r.Emit("session_before_compact", map[string]any{"reason": "threshold"}); got != nil {
				t.Fatalf("threshold cancelled without a selection: %v", got)
			}
			if err := r.settle(t); err != nil || len(r.Calls("compact")) != 0 || len(r.Messages()) != 0 {
				t.Fatalf("settle: %v, compacts %d, messages %d", err, len(r.Calls("compact")), len(r.Messages()))
			}
		})
	}
}

func TestThresholdCancelOnlyWhileSelected(t *testing.T) {
	r := startRig(t, Options{CacheWriteReadRatio: ratio(12.5), KeepRecentTokens: keep(1)}, testhost.Options{})
	r.boundary(t, boundaryTurn, false)
	for reason, cancelled := range map[string]bool{"threshold": true, "manual": false, "overflow": false} {
		got, err := r.Emit("session_before_compact", map[string]any{"reason": reason})
		if err != nil || (got != nil) != cancelled {
			t.Errorf("%s: got %v, %v; want cancelled=%v", reason, got, err, cancelled)
		}
	}
}

func TestNoAbortWithoutPressureOrRatio(t *testing.T) {
	// 100k of 200k is below window protection, and no ratio means the economic gate never opens.
	r := startRig(t, Options{KeepRecentTokens: keep(1)}, testhost.Options{ContextUsage: map[string]any{"tokens": 100000, "contextWindow": 200000}})
	r.boundary(t, boundaryTurn, false)
	if r.Aborts() != 0 {
		t.Fatalf("aborts = %d", r.Aborts())
	}
}

func TestTooLittleHistoryIsNotCompactable(t *testing.T) {
	r := startRig(t, Options{CacheWriteReadRatio: ratio(12.5)}, testhost.Options{Projection: projectionOf(workAssist)})
	r.boundary(t, boundaryTurn, false)
	if r.Aborts() != 0 || len(r.Calls("compact")) != 0 {
		t.Fatalf("aborts = %d, compacts = %d", r.Aborts(), len(r.Calls("compact")))
	}
}

func TestProjectionErrorMeansNoCompaction(t *testing.T) {
	r := startRig(t, Options{CacheWriteReadRatio: ratio(12.5), KeepRecentTokens: keep(1)}, testhost.Options{})
	r.Handle("sessionRead", func(map[string]any) (any, error) { return nil, errors.New("projection unavailable") })
	r.boundary(t, boundaryTurn, false)
	if r.Aborts() != 0 {
		t.Fatalf("aborts = %d after a projection error", r.Aborts())
	}
}

func TestRecoverableErrorsSkipAndDoNotRetryUntilRealWork(t *testing.T) {
	for _, reason := range []string{
		"Nothing to compact (session too small)",
		"Already compacted",
		"Summarization failed: generation hit the token cap and the summary is incomplete",
	} {
		t.Run(reason, func(t *testing.T) {
			r := startRig(t, Options{CacheWriteReadRatio: ratio(12.5), KeepRecentTokens: keep(1)}, testhost.Options{
				CompactResult: func(map[string]any) (map[string]any, error) { return nil, errors.New(reason) },
			})
			threeSteps := func(a, b, c string) []any {
				return []any{
					map[string]any{"id": "a", "goal": "ga", "status": a},
					map[string]any{"id": "b", "goal": "gb", "status": b},
					map[string]any{"id": "c", "goal": "gc", "status": c},
				}
			}
			update := func(id string, steps []any) {
				if res := r.CallTool("update_plan", id, map[string]any{"steps": steps}); res.Err != nil {
					t.Fatal(res.Err)
				}
			}
			r.EmitContext([]any{oldUser, workAssist})
			update("p0", threeSteps("in_progress", "pending", "pending"))
			update("p1", threeSteps("completed", "in_progress", "pending"))
			r.turnEnd(t, boundaryTurn, planResult("p1", false))
			if r.Aborts() != 1 {
				t.Fatalf("aborts = %d", r.Aborts())
			}
			if err := r.settle(t); err != nil {
				t.Fatalf("recoverable error must not fail the handler: %v", err)
			}
			var skips []map[string]any
			for _, e := range r.Entries() {
				if e["customType"] == "sol-pi-online-context-compact-skipped" {
					skips = append(skips, e["data"].(map[string]any))
				}
			}
			if len(skips) != 1 || skips[0]["reason"] != reason {
				t.Fatalf("skip entries = %v", skips)
			}
			notes := r.Calls("ui.notify")
			if len(notes) != 1 || notes[0].Args["message"] != "Online compaction skipped: "+reason || fmt.Sprint(notes[0].Args) == "" {
				t.Fatalf("notices = %+v", notes)
			}
			if level := fmt.Sprint(notes[0].Args); !strings.Contains(level, "warning") {
				t.Fatalf("notice args = %v", level)
			}
			if msgs := r.Messages(); len(msgs) != 1 || msgs[0].Args["content"] != "Online context compaction was skipped. The existing context is still available. Continue the remaining work from the current plan." || msgs[0].Args["options"].(map[string]any)["triggerTurn"] != true {
				t.Fatalf("messages = %+v", msgs)
			}
			if s := lastState(t, r); s.NativeCompactionCount != 0 || s.CacheDebtTokens != 0 {
				t.Fatalf("a refused compaction changed state: %+v", s)
			}

			// Plan-only chatter does not retry.
			update("p2", threeSteps("completed", "completed", "in_progress"))
			r.turnEnd(t, boundaryTurn, planResult("p2", false))
			if r.Aborts() != 1 {
				t.Fatalf("plan-only boundary retried: aborts = %d", r.Aborts())
			}
			// A real tool success re-enables it.
			update("p3", threeSteps("completed", "completed", "completed"))
			r.turnEnd(t, boundaryTurn,
				map[string]any{"role": "toolResult", "toolCallId": "b1", "toolName": "bash", "isError": false},
				planResult("p3", false))
			if r.Aborts() != 2 {
				t.Fatalf("aborts after real work = %d, want 2", r.Aborts())
			}
		})
	}
}

func TestUserCancellationSendsNothing(t *testing.T) {
	r := startRig(t, Options{CacheWriteReadRatio: ratio(12.5), KeepRecentTokens: keep(1)}, testhost.Options{
		CompactResult: func(map[string]any) (map[string]any, error) { return nil, errors.New("Compaction cancelled") },
	})
	r.boundary(t, boundaryTurn, false)
	if err := r.settle(t); err != nil {
		t.Fatalf("cancellation returned %v", err)
	}
	if len(r.Messages()) != 0 {
		t.Fatalf("messages = %+v", r.Messages())
	}
	for _, e := range r.Entries() {
		if e["customType"] == "sol-pi-online-context-compact-skipped" {
			t.Fatal("cancellation wrote a skip entry")
		}
	}
}

func TestOtherFailureResumesAndFails(t *testing.T) {
	r := startRig(t, Options{CacheWriteReadRatio: ratio(12.5), KeepRecentTokens: keep(1)}, testhost.Options{
		CompactResult: func(map[string]any) (map[string]any, error) { return nil, errors.New("provider exploded") },
	})
	r.boundary(t, boundaryTurn, false)
	err := r.settle(t)
	if err == nil || !strings.Contains(err.Error(), "provider exploded") {
		t.Fatalf("settle error = %v", err)
	}
	if msgs := r.Messages(); len(msgs) != 1 || msgs[0].Args["content"] != "Online context compaction did not complete. The parent task is still active. Continue the remaining work from the current plan." {
		t.Fatalf("messages = %+v", msgs)
	}
	if s := lastState(t, r); s.NativeCompactionCount != 0 || s.CacheDebtTokens != 0 {
		t.Fatalf("failed attempt charged state: %+v", s)
	}
}

func TestNoSavingsNoticeWhenTheSummaryIsLarger(t *testing.T) {
	r := startRig(t, Options{CacheWriteReadRatio: ratio(12.5), KeepRecentTokens: keep(1)}, testhost.Options{
		CompactResult: func(map[string]any) (map[string]any, error) {
			return map[string]any{"summary": strings.Repeat("s", 40000), "firstKeptEntryId": "e1", "tokensBefore": 1}, nil
		},
	})
	r.boundary(t, boundaryTurn, false)
	if err := r.settle(t); err != nil {
		t.Fatal(err)
	}
	if notes := r.Calls("ui.notify"); len(notes) != 0 {
		t.Fatalf("notices = %+v", notes)
	}
	if s := lastState(t, r); s.NativeCompactionCount != 1 {
		t.Fatalf("state = %+v", s)
	}
	if len(r.Messages()) != 1 {
		t.Fatalf("messages = %+v", r.Messages())
	}
}

func (r *rig) seed(t *testing.T, s State) {
	t.Helper()
	if res := r.CallTool("seed", "seed", map[string]any{"state": s}); res.Err != nil {
		t.Fatal(res.Err)
	}
	if _, err := r.Emit("session_start", map[string]any{"type": "session_start"}); err != nil {
		t.Fatal(err)
	}
}

func TestUnrelatedNativeCompactionKeepsDebt(t *testing.T) {
	for _, fromExtension := range []bool{false, true} {
		r := startRig(t, Options{}, testhost.Options{})
		seeded := InitialState()
		seeded.CacheDebtTokens, seeded.CacheDebtRepaymentTokens = 900, 300
		r.seed(t, seeded)
		if _, err := r.Emit("session_compact", map[string]any{"type": "session_compact", "fromExtension": fromExtension, "reason": "manual", "compactionEntry": map[string]any{}}); err != nil {
			t.Fatal(err)
		}
		s := lastState(t, r)
		if s.NativeCompactionCount != 1 || s.CacheDebtTokens != 900 || s.CacheDebtRepaymentTokens != 300 {
			t.Fatalf("fromExtension=%v: state = %+v", fromExtension, s)
		}
	}
}

func TestRestoreFromTheBranch(t *testing.T) {
	r := startRig(t, Options{}, testhost.Options{})
	seeded := InitialState()
	seeded.RequestCount, seeded.NativeCompactionCount = 7, 2
	r.seed(t, seeded)
	r.EmitContext([]any{oldUser})
	r.providerRequest(t)
	if s := lastState(t, r); s.RequestCount != 8 || s.NativeCompactionCount != 2 {
		t.Fatalf("state = %+v", s)
	}
}

func TestInputHandoffAndCorrection(t *testing.T) {
	completed := InitialState()
	completed.Plan = []Step{{"a", "ga", "completed"}}
	completed.RequestCount, completed.LastBoundaryRequestCount = 5, 3
	completed.CompletedBoundaryRequestCounts = []int{3}
	completed.CacheDebtTokens, completed.CacheDebtRepaymentTokens = 900, 300

	input := func(r *rig, data map[string]any) {
		t.Helper()
		got, err := r.Emit("input", data)
		if err != nil || got.(map[string]any)["action"] != "continue" {
			t.Fatalf("input result = %v, %v", got, err)
		}
	}

	r := startRig(t, Options{}, testhost.Options{})
	r.seed(t, completed)
	input(r, map[string]any{"text": "next task", "source": "interactive"})
	s := lastState(t, r)
	if len(s.Plan) != 0 || s.CacheDebtTokens != 900 || s.Epoch != 1 || s.LastBoundaryRequestCount != 5 {
		t.Fatalf("handoff state = %+v", s)
	}
	before := len(stateEntries(r))
	input(r, map[string]any{"text": "again"}) // nothing to hand off any more
	if len(stateEntries(r)) != before {
		t.Fatal("a second handoff wrote state")
	}

	for name, data := range map[string]map[string]any{
		"steer":  {"text": "stop that", "streamingBehavior": "steer"},
		"prefix": {"text": "CORRECTION: use go"},
	} {
		r := startRig(t, Options{}, testhost.Options{})
		r.seed(t, completed)
		input(r, data)
		if s := lastState(t, r); len(s.Plan) != 0 || s.CacheDebtTokens != 0 || s.CacheDebtRepaymentTokens != 0 || s.Epoch != 1 {
			t.Fatalf("%s: correction state = %+v", name, s)
		}
	}

	r = startRig(t, Options{}, testhost.Options{})
	open := completed
	open.Plan = []Step{{"a", "ga", "in_progress"}}
	r.seed(t, open)
	input(r, map[string]any{"text": "follow up"})
	if len(stateEntries(r)) != 1 {
		t.Fatalf("handoff over an unfinished plan wrote state: %d entries", len(stateEntries(r)))
	}
}

func TestCorrectionDropsTheSelection(t *testing.T) {
	r := startRig(t, Options{CacheWriteReadRatio: ratio(12.5), KeepRecentTokens: keep(1)}, testhost.Options{})
	r.boundary(t, boundaryTurn, false)
	if _, err := r.Emit("input", map[string]any{"text": "CORRECTION: stop"}); err != nil {
		t.Fatal(err)
	}
	if err := r.settle(t); err != nil || len(r.Calls("compact")) != 0 {
		t.Fatalf("settle: %v, compacts %d", err, len(r.Calls("compact")))
	}
}

func TestShutdownDropsTheSelection(t *testing.T) {
	r := startRig(t, Options{CacheWriteReadRatio: ratio(12.5), KeepRecentTokens: keep(1)}, testhost.Options{})
	r.boundary(t, boundaryTurn, false)
	if _, err := r.Emit("session_shutdown", map[string]any{"type": "session_shutdown"}); err != nil {
		t.Fatal(err)
	}
	if err := r.settle(t); err != nil || len(r.Calls("compact")) != 0 {
		t.Fatalf("settle: %v, compacts %d", err, len(r.Calls("compact")))
	}
}

func TestSessionStartAndTreeMakeNoSessionRead(t *testing.T) {
	r := startRig(t, Options{}, testhost.Options{})
	for _, event := range []string{"session_start", "session_tree"} {
		if _, err := r.Emit(event, map[string]any{}); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(r.Calls("sessionRead")) + len(r.Calls("sessionGraph.call")); n != 0 {
		t.Fatalf("%d session reads before the first state-needing event", n)
	}
	r.EmitContext([]any{oldUser})
	if len(r.Calls("sessionRead")) == 0 {
		t.Fatal("the first state-needing event did not restore")
	}
}

func normalizeJSON(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func schemaString(max int) map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": max}
}

func schemaStrings(max int) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string", "maxLength": 1000}, "maxItems": max}
}

var wantSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"steps": map[string]any{
			"type": "array", "minItems": 1, "maxItems": 128,
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": schemaString(16384), "goal": schemaString(16384),
					"status": map[string]any{"anyOf": []any{
						map[string]any{"const": "pending", "type": "string"},
						map[string]any{"const": "in_progress", "type": "string"},
						map[string]any{"const": "completed", "type": "string"},
					}},
				},
				"required":             []any{"id", "goal", "status"},
				"additionalProperties": false,
			},
		},
		"progress": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"files_changed": schemaStrings(128), "verification": schemaStrings(64), "decisions": schemaStrings(64),
			},
			"required":             []any{"files_changed", "verification", "decisions"},
			"additionalProperties": false,
		},
	},
	"required":             []any{"steps"},
	"additionalProperties": false,
}

func TestPlanRenderers(t *testing.T) {
	banner := []string{"⚡ SoL-PiG · Online Context Compact", "Money saved · compacts only when projected savings are positive"}
	steps := []any{
		map[string]any{"id": "a", "goal": "g", "status": "completed"},
		map[string]any{"id": "b", "goal": "g", "status": "in_progress"},
		map[string]any{"id": "c", "goal": "g", "status": "pending"},
	}
	for name, tc := range map[string]struct {
		got  []string
		want string
	}{
		"call":     {planCallLines(map[string]any{"steps": steps}), "Plan: 3 steps, 1 completed"},
		"pending":  {planResultLines(true, nil), "Updating plan..."},
		"boundary": {planResultLines(false, map[string]any{"boundary": true}), "Progress boundary recorded"},
		"plain":    {planResultLines(false, map[string]any{"boundary": false}), "Plan recorded"},
	} {
		if want := append(slices.Clone(banner), tc.want); !slices.Equal(tc.got, want) {
			t.Errorf("%s = %q, want %q", name, tc.got, want)
		}
	}
}

func TestSettingsErrorMeansNoCompaction(t *testing.T) {
	ext := sdk.New("probe")
	if err := Register(ext, Options{CacheWriteReadRatio: ratio(12.5), KeepRecentTokens: keep(1)}); err != nil {
		t.Fatal(err)
	}
	h := testhost.Start(t, ext, testhost.Options{
		Cwd: t.TempDir(), Mode: "tui", SessionDir: t.TempDir(), SessionID: "s1", // no Settings: GetSettings fails
		ContextUsage: map[string]any{"tokens": 195000, "contextWindow": 200000},
		ModelInfo:    map[string]any{"id": "m1", "provider": "p", "contextWindow": 200000},
		Projection:   projectionOf(oldUser, workAssist),
	})
	r := &rig{Host: h, ext: ext}
	r.boundary(t, boundaryTurn, false)
	if r.Aborts() != 0 {
		t.Fatalf("aborts = %d after a settings error", r.Aborts())
	}
}

func TestThresholdDuringOwnCompactionIsNotCancelled(t *testing.T) {
	var during any
	var r *rig
	r = startRig(t, Options{CacheWriteReadRatio: ratio(12.5), KeepRecentTokens: keep(1)}, testhost.Options{
		CompactResult: func(map[string]any) (map[string]any, error) {
			during, _ = r.Emit("session_before_compact", map[string]any{"reason": "threshold"})
			return map[string]any{"summary": "S", "firstKeptEntryId": "e1", "tokensBefore": 1}, nil
		},
	})
	r.boundary(t, boundaryTurn, false)
	if err := r.settle(t); err != nil {
		t.Fatal(err)
	}
	if len(r.Calls("compact")) != 1 || during != nil {
		t.Fatalf("compacts %d, threshold during compaction = %v", len(r.Calls("compact")), during)
	}
}

func TestReminderSendFailureIsReturned(t *testing.T) {
	r := startRig(t, Options{CacheWriteReadRatio: ratio(12.5), KeepRecentTokens: keep(1)}, testhost.Options{})
	r.Handle("sendMessage", func(map[string]any) (any, error) { return nil, errors.New("send refused") })
	r.boundary(t, boundaryTurn, false)
	if err := r.settle(t); err == nil || !strings.Contains(err.Error(), "send refused") {
		t.Fatalf("settle error = %v", err)
	}
}
