// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi tests/online-context-compact-plan.test.ts at 2195680 (NVIDIA, MIT).

package occ

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

var (
	openPlan = []Step{{"build", "build it", "in_progress"}}
	donePlan = []Step{{"build", "build it", "completed"}}
)

// decode returns JSON text as the any-tree encoding/json produces.
func decode(t *testing.T, text string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestParsePlanSteps(t *testing.T) {
	big := strings.Repeat("x", 16385)
	for _, c := range []struct {
		name, text string
		ok         bool
	}{
		{"empty", `[]`, true},
		{"valid", `[{"id":"build","goal":"build it","status":"in_progress"}]`, true},
		{"missing id", `[{"goal":"missing id","status":"pending"}]`, false},
		{"unknown status", `[{"id":"x","goal":"x","status":"unknown"}]`, false},
		{"duplicate ids", `[{"id":"x","goal":"a","status":"pending"},{"id":"x","goal":"b","status":"pending"}]`, false},
		{"extra key", `[{"id":"x","goal":"a","status":"pending","note":"n"}]`, false},
		{"empty id", `[{"id":"","goal":"a","status":"pending"}]`, false},
		{"non-string goal", `[{"id":"x","goal":1,"status":"pending"}]`, false},
		{"oversize goal", `[{"id":"x","goal":"` + big + `","status":"pending"}]`, false},
		{"limit goal", `[{"id":"x","goal":"` + big[1:] + `","status":"pending"}]`, true},
		{"not an array", `{}`, false},
		{"non-object item", `["x"]`, false},
	} {
		steps, ok := ParsePlanSteps(decode(t, c.text))
		if ok != c.ok {
			t.Errorf("%s: ok=%v", c.name, ok)
		}
		if !ok && steps != nil {
			t.Errorf("%s: steps should be nil", c.name)
		}
	}
	steps, ok := ParsePlanSteps(decode(t, `[{"id":"build","goal":"build it","status":"in_progress"}]`))
	if !ok || !reflect.DeepEqual(steps, openPlan) {
		t.Fatalf("got %+v", steps)
	}
	if steps, _ := ParsePlanSteps(decode(t, `[]`)); steps == nil || len(steps) != 0 {
		t.Fatalf("empty plan should be a non-nil empty slice, got %#v", steps)
	}
}

func TestParsePlanStepsLimit(t *testing.T) {
	mk := func(n int) []any {
		items := make([]any, n)
		for i := range items {
			items[i] = map[string]any{"id": string(rune('a'+i%26)) + strings.Repeat("i", i/26), "goal": "g", "status": "pending"}
		}
		return items
	}
	if _, ok := ParsePlanSteps(mk(128)); !ok {
		t.Fatal("128 steps should pass")
	}
	if _, ok := ParsePlanSteps(mk(129)); ok {
		t.Fatal("129 steps should fail")
	}
}

func completedIDs(a, b []Step) []string { return AnalyzePlanTransition(a, b).CompletedIDs }

func TestAnalyzePlanTransitionDetectsOnlyNewCompletions(t *testing.T) {
	if got := completedIDs(openPlan, donePlan); !reflect.DeepEqual(got, []string{"build"}) {
		t.Fatalf("got %v", got)
	}
	if got := completedIDs(donePlan, donePlan); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestAnalyzePlanTransitionHistoryAndGenuineTransitions(t *testing.T) {
	rekeyed := []Step{{"renamed", "build it (restated)", "completed"}}
	if got := completedIDs(nil, donePlan); len(got) != 0 {
		t.Fatalf("new completed step is history: %v", got)
	}
	if got := completedIDs(donePlan, rekeyed); len(got) != 0 {
		t.Fatalf("rekeyed: %v", got)
	}
	if got := completedIDs(openPlan, append(append([]Step{}, donePlan...), rekeyed...)); !reflect.DeepEqual(got, []string{"build"}) {
		t.Fatalf("got %v", got)
	}
	pending := []Step{{"build", "build it", "pending"}}
	if got := completedIDs(pending, donePlan); !reflect.DeepEqual(got, []string{"build"}) {
		t.Fatalf("got %v", got)
	}
}

func TestAnalyzePlanTransitionAdvice(t *testing.T) {
	tr := AnalyzePlanTransition(
		[]Step{{"a", "old", "in_progress"}},
		[]Step{{"a", "new", "in_progress"}, {"b", "second", "in_progress"}},
	)
	want := []string{
		`Plan step "a" changed goal; reuse an id only for the same goal.`,
		"Keep at most one plan step in_progress.",
	}
	if !reflect.DeepEqual(tr.Advice, want) {
		t.Fatalf("got %q", tr.Advice)
	}
	tr = AnalyzePlanTransition(nil, []Step{{"a", "g", "pending"}})
	if !reflect.DeepEqual(tr.Advice, []string{"Mark one pending plan step in_progress before starting it."}) {
		t.Fatalf("got %q", tr.Advice)
	}
	if tr := AnalyzePlanTransition(nil, nil); tr.Advice == nil || tr.CompletedIDs == nil {
		t.Fatal("results should be non-nil empty slices")
	}
}

func TestFormatPlanSnapshotKeepsKeyOrder(t *testing.T) {
	got := FormatPlanSnapshot(openPlan)
	want := `<sol-pi-plan task_status="active">{"steps":[{"id":"build","goal":"build it","status":"in_progress"}]}</sol-pi-plan>`
	if got != want {
		t.Fatalf("got %s", got)
	}
	if got := FormatPlanSnapshot(nil); got != `<sol-pi-plan task_status="active">{"steps":[]}</sol-pi-plan>` {
		t.Fatalf("nil plan: %s", got)
	}
}
