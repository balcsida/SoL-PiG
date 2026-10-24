// SPDX-FileCopyrightText: 2026 SoL-PiG contributors
// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: MIT
//
// Ported from SoL-Pi src/sol-pi/extensions/online-context-compact/state.ts at 2195680 (NVIDIA, MIT).

package occ

import (
	"math"
	"slices"
)

// StateEntryType is the custom entry type that persists State.
const StateEntryType = "sol-pi-online-context-state-v1"

// Progress ports ProgressSummary.
type Progress struct {
	StepID       string   `json:"stepId"`
	Goal         string   `json:"goal"`
	FilesChanged []string `json:"filesChanged"`
	Verification []string `json:"verification"`
	Decisions    []string `json:"decisions"`
	NextWork     []string `json:"nextWork"`
}

// State ports OnlineState; the field order fixes the JSON key order.
type State struct {
	Version                        int        `json:"version"`
	Epoch                          int        `json:"epoch"`
	Plan                           []Step     `json:"plan"`
	PendingProgress                []Progress `json:"pendingProgress"`
	RequestCount                   int        `json:"requestCount"`
	LastCompactionRequestCount     *int       `json:"lastCompactionRequestCount"`
	LastBoundaryRequestCount       int        `json:"lastBoundaryRequestCount"`
	CompletedBoundaryRequestCounts []int      `json:"completedBoundaryRequestCounts"`
	LastContextTokens              *int       `json:"lastContextTokens"`
	PositiveContextDeltaTotal      float64    `json:"positiveContextDeltaTotal"`
	PositiveContextDeltaCount      int        `json:"positiveContextDeltaCount"`
	NativeCompactionCount          int        `json:"nativeCompactionCount"`
	CacheDebtTokens                float64    `json:"cacheDebtTokens"`
	CacheDebtRepaymentTokens       float64    `json:"cacheDebtRepaymentTokens"`
}

// InitialState ports initialOnlineState.
func InitialState() State {
	return State{Version: 1, Plan: []Step{}, PendingProgress: []Progress{}, CompletedBoundaryRequestCounts: []int{}}
}

// safeInt reports whether v is a JSON number that is a non-negative safe integer.
func safeInt(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok || f != math.Trunc(f) || f < 0 || f > 1<<53-1 {
		return 0, false
	}
	return int(f), true
}

func finiteNonNegative(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok && !math.IsInf(f, 0) && !math.IsNaN(f) && f >= 0
}

func stringArray(v any) ([]string, bool) {
	items, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, len(items))
	for i, item := range items {
		if out[i], ok = item.(string); !ok {
			return nil, false
		}
	}
	return out, true
}

func parseProgress(v any) (Progress, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return Progress{}, false
	}
	var p Progress
	var ok1, ok2, ok3, ok4, ok5, ok6 bool
	p.StepID, ok1 = m["stepId"].(string)
	p.Goal, ok2 = m["goal"].(string)
	p.FilesChanged, ok3 = stringArray(m["filesChanged"])
	p.Verification, ok4 = stringArray(m["verification"])
	p.Decisions, ok5 = stringArray(m["decisions"])
	p.NextWork, ok6 = stringArray(m["nextWork"])
	return p, ok1 && ok2 && ok3 && ok4 && ok5 && ok6
}

// ParseState ports parseOnlineState; data is a JSON-decoded object, unknown keys are ignored.
func ParseState(data any) (State, bool) {
	m, ok := data.(map[string]any)
	if !ok {
		return State{}, false
	}
	s := InitialState()
	if v, _ := m["version"].(float64); v != 1 {
		return State{}, false
	}
	var plan []Step
	if plan, ok = ParsePlanSteps(m["plan"]); !ok {
		return State{}, false
	}
	s.Plan = plan
	progress, ok := m["pendingProgress"].([]any)
	if !ok {
		return State{}, false
	}
	for _, item := range progress {
		p, ok := parseProgress(item)
		if !ok {
			return State{}, false
		}
		s.PendingProgress = append(s.PendingProgress, p)
	}
	counts, ok := m["completedBoundaryRequestCounts"].([]any)
	if !ok {
		return State{}, false
	}
	for _, c := range counts {
		n, ok := safeInt(c)
		if !ok {
			return State{}, false
		}
		s.CompletedBoundaryRequestCounts = append(s.CompletedBoundaryRequestCounts, n)
	}
	// Snapshots written before cooldown bookkeeping omit lastCompactionRequestCount.
	if v, present := m["lastCompactionRequestCount"]; present && v != nil {
		n, ok := safeInt(v)
		if !ok {
			return State{}, false
		}
		s.LastCompactionRequestCount = &n
	}
	if v := m["lastContextTokens"]; v != nil {
		n, ok := safeInt(v)
		if !ok {
			return State{}, false
		}
		s.LastContextTokens = &n
	} else if _, present := m["lastContextTokens"]; !present {
		return State{}, false
	}
	var ok1, ok2, ok3, ok4, ok5, ok6, ok7 bool
	s.Epoch, ok1 = safeInt(m["epoch"])
	s.RequestCount, ok2 = safeInt(m["requestCount"])
	s.LastBoundaryRequestCount, ok3 = safeInt(m["lastBoundaryRequestCount"])
	s.PositiveContextDeltaTotal, ok4 = finiteNonNegative(m["positiveContextDeltaTotal"])
	s.PositiveContextDeltaCount, ok5 = safeInt(m["positiveContextDeltaCount"])
	s.NativeCompactionCount, ok6 = safeInt(m["nativeCompactionCount"])
	s.CacheDebtTokens, ok7 = finiteNonNegative(m["cacheDebtTokens"])
	var ok8 bool
	s.CacheDebtRepaymentTokens, ok8 = finiteNonNegative(m["cacheDebtRepaymentTokens"])
	if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6 && ok7 && ok8) || s.LastBoundaryRequestCount > s.RequestCount {
		return State{}, false
	}
	return s, true
}

// RestoreState ports restoreOnlineState: the newest valid state entry wins, a
// malformed one is skipped, and no valid entry yields the initial state.
func RestoreState(branch []map[string]any) State {
	for i := len(branch) - 1; i >= 0; i-- {
		e := branch[i]
		if e["type"] != "custom" || e["customType"] != StateEntryType {
			continue
		}
		if s, ok := ParseState(e["data"]); ok {
			return s
		}
	}
	return InitialState()
}

// RecordProviderRequest ports recordProviderRequest.
func (s State) RecordProviderRequest(contextTokens int) State {
	delta := 0
	if s.LastContextTokens != nil {
		delta = contextTokens - *s.LastContextTokens
	}
	debt := math.Max(0, s.CacheDebtTokens-s.CacheDebtRepaymentTokens)
	s.RequestCount++
	s.LastContextTokens = &contextTokens
	s.PositiveContextDeltaTotal += float64(max(0, delta))
	if delta > 0 {
		s.PositiveContextDeltaCount++
	}
	s.CacheDebtTokens = debt
	if debt == 0 {
		s.CacheDebtRepaymentTokens = 0
	}
	return s
}

// RecordBoundary ports recordBoundary.
func (s State) RecordBoundary(plan []Step, progress *Progress) State {
	interval := max(0, s.RequestCount-s.LastBoundaryRequestCount)
	s.Plan = slices.Clone(plan)
	if progress != nil {
		s.PendingProgress = append(slices.Clone(s.PendingProgress), *progress)
	}
	s.LastBoundaryRequestCount = s.RequestCount
	s.CompletedBoundaryRequestCounts = append(slices.Clone(s.CompletedBoundaryRequestCounts), interval)
	return s
}

// RecordCompaction ports recordCompaction. It keeps the plan and its progress
// samples: compaction changes the context representation, not completed steps.
func (s State) RecordCompaction(debt, repayment float64) State {
	count := s.RequestCount
	s.Epoch++
	s.Plan = slices.Clone(s.Plan)
	s.LastCompactionRequestCount = &count
	s.PendingProgress = []Progress{}
	s.LastContextTokens = nil // do not compare context sizes across the replacement summary
	s.NativeCompactionCount++
	s.CacheDebtTokens += math.Max(0, debt)
	s.CacheDebtRepaymentTokens += math.Max(0, repayment)
	return s
}

func (s State) resetHorizon() State {
	s.Epoch++
	s.Plan = []Step{}
	s.PendingProgress = []Progress{}
	s.LastBoundaryRequestCount = s.RequestCount
	s.CompletedBoundaryRequestCounts = []int{}
	s.LastContextTokens = nil
	s.PositiveContextDeltaTotal = 0
	s.PositiveContextDeltaCount = 0
	return s
}

// RecordCorrection ports recordCorrection: stale history and debt are dropped.
func (s State) RecordCorrection() State {
	s = s.resetHorizon()
	s.CacheDebtTokens = 0
	s.CacheDebtRepaymentTokens = 0
	return s
}

// RecordCompletedPlanHandoff ports recordCompletedPlanHandoff: a new user task
// must not inherit the completed task's request horizon, but keeps its debt.
func (s State) RecordCompletedPlanHandoff() (State, bool) {
	if len(s.Plan) == 0 || slices.ContainsFunc(s.Plan, func(p Step) bool { return p.Status != "completed" }) {
		return s, false
	}
	return s.resetHorizon(), true
}
