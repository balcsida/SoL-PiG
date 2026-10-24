// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi tests/online-context-compact-state.test.ts at 2195680 (NVIDIA, MIT).

package occ

import (
	"encoding/json"
	"reflect"
	"testing"
)

var (
	testPlan = []Step{
		{"inspect", "inspect the implementation", "completed"},
		{"verify", "verify the change", "in_progress"},
	}
	testProgress = Progress{
		StepID:       "inspect",
		Goal:         "inspect the implementation",
		FilesChanged: []string{"src/a.ts"},
		Verification: []string{"targeted test passed"},
		Decisions:    []string{"keep the change small"},
		NextWork:     []string{"verify the change"},
	}
)

func asData(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return decode(t, string(raw)).(map[string]any)
}

func stateEntry(data any) map[string]any {
	return map[string]any{"type": "custom", "customType": StateEntryType, "data": data}
}

func TestInitialStateJSON(t *testing.T) {
	raw, err := json.Marshal(InitialState())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"version":1,"epoch":0,"plan":[],"pendingProgress":[],"requestCount":0,"lastCompactionRequestCount":null,` +
		`"lastBoundaryRequestCount":0,"completedBoundaryRequestCounts":[],"lastContextTokens":null,` +
		`"positiveContextDeltaTotal":0,"positiveContextDeltaCount":0,"nativeCompactionCount":0,` +
		`"cacheDebtTokens":0,"cacheDebtRepaymentTokens":0}`
	if string(raw) != want {
		t.Fatalf("got %s", raw)
	}
}

func TestRestoreStateSkipsMalformedTail(t *testing.T) {
	state := InitialState().RecordProviderRequest(100).RecordBoundary(testPlan, &testProgress)
	branch := []map[string]any{
		stateEntry(asData(t, state)),
		stateEntry(map[string]any{"version": 1.0, "plan": "broken"}),
	}
	if got := RestoreState(branch); !reflect.DeepEqual(got, state) {
		t.Fatalf("got %+v want %+v", got, state)
	}
}

func TestRestoreStatePicksNewestValidAndIgnoresOtherEntries(t *testing.T) {
	older := InitialState().RecordProviderRequest(1)
	newer := older.RecordProviderRequest(2)
	branch := []map[string]any{
		stateEntry(asData(t, older)),
		stateEntry(asData(t, newer)),
		{"type": "custom", "customType": "other", "data": asData(t, InitialState())},
		{"type": "message"},
		stateEntry(nil),
	}
	if got := RestoreState(branch); !reflect.DeepEqual(got, newer) {
		t.Fatalf("got %+v", got)
	}
}

func TestRestoreStateInitialWhenNoValidEntry(t *testing.T) {
	for _, branch := range [][]map[string]any{nil, {{"type": "message"}}, {stateEntry(map[string]any{"version": 2.0})}} {
		if got := RestoreState(branch); !reflect.DeepEqual(got, InitialState()) {
			t.Fatalf("got %+v", got)
		}
	}
}

func TestParseStateRejections(t *testing.T) {
	valid := func() map[string]any { return asData(t, InitialState()) }
	if _, ok := ParseState(valid()); !ok {
		t.Fatal("initial state should parse")
	}
	for name, mutate := range map[string]func(map[string]any){
		"version":        func(m map[string]any) { m["version"] = 2.0 },
		"fractional":     func(m map[string]any) { m["requestCount"] = 1.5 },
		"negative":       func(m map[string]any) { m["epoch"] = -1.0 },
		"unsafe":         func(m map[string]any) { m["epoch"] = 9007199254740992.0 },
		"boundary>count": func(m map[string]any) { m["lastBoundaryRequestCount"] = 1.0 },
		"bad counts":     func(m map[string]any) { m["completedBoundaryRequestCounts"] = []any{1.0, "x"} },
		"counts null":    func(m map[string]any) { m["completedBoundaryRequestCounts"] = nil },
		"bad ctx":        func(m map[string]any) { m["lastContextTokens"] = -1.0 },
		"neg delta":      func(m map[string]any) { m["positiveContextDeltaTotal"] = -0.5 },
		"bad last comp":  func(m map[string]any) { m["lastCompactionRequestCount"] = "x" },
		"bad plan":       func(m map[string]any) { m["plan"] = []any{map[string]any{"id": "a"}} },
		"bad progress":   func(m map[string]any) { m["pendingProgress"] = []any{map[string]any{"stepId": "a"}} },
		"progress nil":   func(m map[string]any) { m["pendingProgress"] = []any{nil} },
		"missing debt":   func(m map[string]any) { delete(m, "cacheDebtTokens") },
	} {
		m := valid()
		mutate(m)
		if _, ok := ParseState(m); ok {
			t.Errorf("%s should be rejected", name)
		}
	}
	if _, ok := ParseState("x"); ok {
		t.Error("non-object should be rejected")
	}
}

func TestParseStateLegacyAndExtraFields(t *testing.T) {
	m := asData(t, InitialState())
	delete(m, "lastCompactionRequestCount")
	m["awaitingPlanRestatement"] = true
	got, ok := ParseState(m)
	if !ok || !reflect.DeepEqual(got, InitialState()) {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

func TestRecordProviderRequestCountsGrowthAndRepaysDebt(t *testing.T) {
	s := InitialState()
	s.CacheDebtTokens, s.CacheDebtRepaymentTokens = 300, 100
	s = s.RecordProviderRequest(1000).RecordProviderRequest(1250).RecordProviderRequest(900)
	if s.RequestCount != 3 || *s.LastContextTokens != 900 || s.PositiveContextDeltaTotal != 250 ||
		s.PositiveContextDeltaCount != 1 || s.CacheDebtTokens != 0 || s.CacheDebtRepaymentTokens != 0 {
		t.Fatalf("got %+v", s)
	}
}

func TestRecordBoundaryIntervalAndProgress(t *testing.T) {
	s := InitialState().RecordProviderRequest(100).RecordProviderRequest(200).RecordBoundary(testPlan, &testProgress)
	s = s.RecordProviderRequest(300).RecordBoundary(testPlan, nil)
	if !reflect.DeepEqual(s.CompletedBoundaryRequestCounts, []int{2, 1}) || s.LastBoundaryRequestCount != 3 ||
		!reflect.DeepEqual(s.PendingProgress, []Progress{testProgress}) {
		t.Fatalf("got %+v", s)
	}
}

func TestRecordCompactionPreservesHistory(t *testing.T) {
	before := InitialState().RecordProviderRequest(5000)
	before = before.RecordProviderRequest(5200).RecordBoundary(testPlan, &testProgress).RecordProviderRequest(5300)
	after := before.RecordCompaction(1200, 300)
	if after.Epoch != 1 || !reflect.DeepEqual(after.Plan, testPlan) || *after.LastCompactionRequestCount != before.RequestCount ||
		after.LastBoundaryRequestCount != 2 || !reflect.DeepEqual(after.CompletedBoundaryRequestCounts, []int{2}) ||
		after.PositiveContextDeltaTotal != 300 || after.PositiveContextDeltaCount != 2 || after.LastContextTokens != nil ||
		len(after.PendingProgress) != 0 || after.NativeCompactionCount != 1 ||
		after.CacheDebtTokens != 1200 || after.CacheDebtRepaymentTokens != 300 {
		t.Fatalf("got %+v", after)
	}
	next := after.RecordProviderRequest(1000).RecordBoundary(testPlan, nil)
	if !reflect.DeepEqual(next.CompletedBoundaryRequestCounts, []int{2, 2}) || next.PositiveContextDeltaTotal != 300 {
		t.Fatalf("got %+v", next)
	}
	if got := AnalyzePlanTransition(after.Plan, testPlan).CompletedIDs; len(got) != 0 {
		t.Fatalf("re-issued plan counted as progress: %v", got)
	}
}

func TestRecordCompactionAccumulatesDebt(t *testing.T) {
	o := InitialState()
	o.CacheDebtTokens, o.CacheDebtRepaymentTokens = 900, 300
	if a := o.RecordCompaction(800, 200); a.CacheDebtTokens != 1700 || a.CacheDebtRepaymentTokens != 500 {
		t.Fatalf("got %+v", a)
	}
	if a := o.RecordCompaction(0, 0); a.CacheDebtTokens != 900 || a.CacheDebtRepaymentTokens != 300 {
		t.Fatalf("got %+v", a)
	}
	if a := o.RecordCompaction(-5, -5); a.CacheDebtTokens != 900 || a.CacheDebtRepaymentTokens != 300 {
		t.Fatalf("negatives should clamp: %+v", a)
	}
}

func TestRecordCompletedPlanHandoff(t *testing.T) {
	before := InitialState().RecordProviderRequest(5000).RecordBoundary(testPlan, &testProgress)
	before.Plan = []Step{{"inspect", "g", "completed"}, {"verify", "g", "completed"}}
	before.CacheDebtTokens, before.CacheDebtRepaymentTokens = 900, 300
	got, changed := before.RecordCompletedPlanHandoff()
	if !changed || len(got.Plan) != 0 || len(got.CompletedBoundaryRequestCounts) != 0 || got.LastBoundaryRequestCount != before.RequestCount ||
		got.LastContextTokens != nil || got.CacheDebtTokens != 900 || got.CacheDebtRepaymentTokens != 300 || got.Epoch != before.Epoch+1 {
		t.Fatalf("got %+v changed=%v", got, changed)
	}
	active := before
	active.Plan = testPlan
	if got, changed := active.RecordCompletedPlanHandoff(); changed || !reflect.DeepEqual(got, active) {
		t.Fatalf("active plan should be untouched: %+v", got)
	}
	if _, changed := InitialState().RecordCompletedPlanHandoff(); changed {
		t.Fatal("empty plan should be untouched")
	}
}

func TestRecordCorrectionDropsStaleHistory(t *testing.T) {
	before := InitialState().RecordProviderRequest(5000).RecordBoundary(testPlan, &testProgress)
	before.CacheDebtTokens, before.CacheDebtRepaymentTokens = 900, 300
	got := before.RecordCorrection()
	if got.Epoch != 1 || len(got.Plan) != 0 || len(got.PendingProgress) != 0 || len(got.CompletedBoundaryRequestCounts) != 0 ||
		got.LastContextTokens != nil || got.CacheDebtTokens != 0 || got.CacheDebtRepaymentTokens != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestTransitionsDoNotAliasTheReceiver(t *testing.T) {
	a := InitialState().RecordProviderRequest(1).RecordBoundary(testPlan, &testProgress)
	b := a.RecordBoundary(testPlan, &testProgress)
	if len(a.PendingProgress) != 1 || len(a.CompletedBoundaryRequestCounts) != 1 || len(b.PendingProgress) != 2 {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
}
